package mcjava_test

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/watcher/mcjava"
)

func TestVarIntRoundTrip(t *testing.T) {
	tests := []struct {
		v    int32
		want []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{255, []byte{0xff, 0x01}},
		{25565, []byte{0xdd, 0xc7, 0x01}},
		{2097151, []byte{0xff, 0xff, 0x7f}},
		{2147483647, []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
		{-1, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
		{-2147483648, []byte{0x80, 0x80, 0x80, 0x80, 0x08}},
	}
	for _, tt := range tests {
		if got := mcjava.AppendVarInt(nil, tt.v); !bytes.Equal(got, tt.want) {
			t.Errorf("AppendVarInt(%d) = % x; want % x", tt.v, got, tt.want)
		}
		got, err := mcjava.ReadVarInt(bytes.NewReader(tt.want))
		if err != nil || got != tt.v {
			t.Errorf("ReadVarInt(% x) = %d, %v; want %d, nil", tt.want, got, err, tt.v)
		}
	}
}

func TestReadVarIntTooLong(t *testing.T) {
	_, err := mcjava.ReadVarInt(bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01}))
	if !errors.Is(err, mcjava.ErrVarIntTooLong) {
		t.Errorf("ReadVarInt(6 bytes) error = %v; want ErrVarIntTooLong", err)
	}
}

func TestReadVarIntTruncated(t *testing.T) {
	_, err := mcjava.ReadVarInt(bytes.NewReader([]byte{0x80}))
	if !errors.Is(err, io.EOF) {
		t.Errorf("ReadVarInt(truncated) error = %v; want io.EOF", err)
	}
}

func TestHandshakePacket(t *testing.T) {
	want, _ := hex.DecodeString("1300ffffffff0f096c6f63616c686f737463dd01")
	if got := mcjava.HandshakePacket("localhost", 25565); !bytes.Equal(got, want) {
		t.Errorf("HandshakePacket() = % x; want % x", got, want)
	}
}

func TestStatusRequestPacket(t *testing.T) {
	if got := mcjava.StatusRequestPacket(); !bytes.Equal(got, []byte{0x01, 0x00}) {
		t.Errorf("StatusRequestPacket() = % x; want 01 00", got)
	}
}

// frameWith builds a length-prefixed packet from a packet id, a declared string length, and raw string bytes.
func frameWith(id, strLen int32, str string) []byte {
	body := mcjava.AppendVarInt(nil, id)
	body = mcjava.AppendVarInt(body, strLen)
	body = append(body, str...)
	return append(mcjava.AppendVarInt(nil, int32(len(body))), body...)
}

func statusFrame(json string) []byte {
	return frameWith(0x00, int32(len(json)), json)
}

func reader(b []byte) *bufio.Reader {
	return bufio.NewReader(bytes.NewReader(b))
}

func TestReadStatusResponse(t *testing.T) {
	const js = `{"players":{"max":20,"online":2}}`
	got, err := mcjava.ReadStatusResponse(reader(statusFrame(js)), 1<<20)
	if err != nil || string(got) != js {
		t.Fatalf("ReadStatusResponse() = %q, %v; want %q, nil", got, err, js)
	}
}

func TestReadStatusResponseErrors(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		max   int
	}{
		{"wrong packet id", frameWith(0x01, 2, "{}"), 1 << 20},
		{"longer than max", statusFrame(strings.Repeat("x", 64)), 16},
		{"string longer than body", frameWith(0x00, 100, "abc"), 1 << 20},
		{"zero length", []byte{0x00}, 1 << 20},
		{"truncated body", append(mcjava.AppendVarInt(nil, 10), 0x00, 0x02), 1 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := mcjava.ReadStatusResponse(reader(tt.input), tt.max); err == nil {
				t.Error("ReadStatusResponse() error = nil; want error")
			}
		})
	}
}

// Review Focus 4: negative lengths must be rejected, not passed to make().
func TestReadStatusResponseRejectsNegativeLengths(t *testing.T) {
	negativeFrame := mcjava.AppendVarInt(nil, -1)
	if _, err := mcjava.ReadStatusResponse(reader(negativeFrame), 1<<20); err == nil {
		t.Error("negative frame length: error = nil; want error")
	}
	if _, err := mcjava.ReadStatusResponse(reader(frameWith(0x00, -5, "abc")), 1<<20); err == nil {
		t.Error("negative string length: error = nil; want error")
	}
}

func TestParseOnline(t *testing.T) {
	const paper = `{"version":{"name":"Paper 26.2","protocol":0},` +
		`"players":{"max":20,"online":2,"sample":[{"name":"kid1","id":"00000000-0000-0000-0000-000000000001"}]},` +
		`"description":{"text":"Shanecraft"},"enforcesSecureChat":true}`
	n, err := mcjava.ParseOnline([]byte(paper))
	if err != nil || n != 2 {
		t.Fatalf("ParseOnline(paper) = %d, %v; want 2, nil", n, err)
	}
	for name, js := range map[string]string{
		"missing players": `{"version":{"name":"x"}}`,
		"missing online":  `{"players":{"max":20}}`,
		"negative online": `{"players":{"max":20,"online":-1}}`,
		"invalid json":    `{"players":`,
	} {
		if _, perr := mcjava.ParseOnline([]byte(js)); perr == nil {
			t.Errorf("ParseOnline(%s) error = nil; want error", name)
		}
	}
}
