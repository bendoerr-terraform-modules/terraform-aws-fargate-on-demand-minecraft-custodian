package mcjava_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/watcher/mcjava"
)

func readFrame(r *bufio.Reader) []byte {
	n, err := mcjava.ReadVarInt(r)
	if err != nil || n < 0 {
		return nil
	}
	b := make([]byte, n)
	if _, err = io.ReadFull(r, b); err != nil {
		return nil
	}
	return b
}

// fakeServer accepts one connection, reads the handshake and status request, then calls respond.
// It returns the listen address and a channel that receives the handshake frame body.
func fakeServer(t *testing.T, respond func(conn net.Conn)) (string, <-chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	handshakes := make(chan []byte, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		handshakes <- readFrame(r)
		_ = readFrame(r)
		respond(conn)
	}()
	return ln.Addr().String(), handshakes
}

func newProber(t *testing.T, addr string, timeout time.Duration) *mcjava.Prober {
	t.Helper()
	p, err := mcjava.New(addr, timeout)
	if err != nil {
		t.Fatalf("New(%q) error = %v", addr, err)
	}
	return p
}

func TestProbeReturnsOnlinePlayers(t *testing.T) {
	addr, handshakes := fakeServer(t, func(conn net.Conn) {
		conn.Write(statusFrame(`{"players":{"max":20,"online":3}}`))
	})
	n, err := newProber(t, addr, time.Second).Probe(t.Context())
	if err != nil || n != 3 {
		t.Fatalf("Probe() = %d, %v; want 3, nil", n, err)
	}
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	want := mcjava.HandshakePacket(host, uint16(port))[1:] // strip the 1-byte length prefix
	if got := <-handshakes; !bytes.Equal(got, want) {
		t.Errorf("handshake = % x; want % x", got, want)
	}
}

// Review Focus 1: servers with server-icon.png send a large base64 favicon.
func TestProbeLargeFavicon(t *testing.T) {
	js := `{"players":{"max":20,"online":4},"favicon":"data:image/png;base64,` + strings.Repeat("A", 40000) + `"}`
	addr, _ := fakeServer(t, func(conn net.Conn) { conn.Write(statusFrame(js)) })
	n, err := newProber(t, addr, time.Second).Probe(t.Context())
	if err != nil || n != 4 {
		t.Fatalf("Probe() = %d, %v; want 4, nil", n, err)
	}
}

// Review Focus 3: something other than Minecraft on the port.
func TestProbeNonMinecraftResponse(t *testing.T) {
	addr, _ := fakeServer(t, func(conn net.Conn) {
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
	})
	if _, err := newProber(t, addr, time.Second).Probe(t.Context()); err == nil {
		t.Fatal("Probe() error = nil; want error for a non-Minecraft response")
	}
}

func TestProbeServerClosesWithoutResponse(t *testing.T) {
	addr, _ := fakeServer(t, func(net.Conn) {})
	if _, err := newProber(t, addr, time.Second).Probe(t.Context()); err == nil {
		t.Fatal("Probe() error = nil; want error")
	}
}

func TestProbeTimesOut(t *testing.T) {
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	addr, _ := fakeServer(t, func(net.Conn) { <-done })
	start := time.Now()
	if _, err := newProber(t, addr, 100*time.Millisecond).Probe(t.Context()); err == nil {
		t.Fatal("Probe() error = nil; want timeout error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Probe() took %s; want about 100ms", elapsed)
	}
}

// A cancelled parent context (SIGTERM) must interrupt a stalled read, not wait out the probe timeout.
func TestProbeReturnsPromptlyWhenCancelled(t *testing.T) {
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	addr, _ := fakeServer(t, func(net.Conn) { <-done })
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	if _, err := newProber(t, addr, 5*time.Second).Probe(ctx); err == nil {
		t.Fatal("Probe() error = nil; want cancellation error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Probe() took %s after cancel; want prompt return", elapsed)
	}
}

func TestProbeConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if _, err = newProber(t, addr, time.Second).Probe(t.Context()); err == nil {
		t.Fatal("Probe() error = nil; want connection refused")
	}
}

func TestProbeOversizedResponse(t *testing.T) {
	addr, _ := fakeServer(t, func(conn net.Conn) {
		conn.Write(mcjava.AppendVarInt(nil, 2<<20))
	})
	if _, err := newProber(t, addr, time.Second).Probe(t.Context()); err == nil {
		t.Fatal("Probe() error = nil; want error for a response over 1 MiB")
	}
}

func TestNewRejectsBadAddress(t *testing.T) {
	for _, addr := range []string{"nope", "host:99999", "host:0", "host:abc"} {
		if _, err := mcjava.New(addr, time.Second); err == nil {
			t.Errorf("New(%q) error = nil; want error", addr)
		}
	}
}
