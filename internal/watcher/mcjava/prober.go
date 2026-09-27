package mcjava

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

// DefaultTimeout bounds one whole status exchange (spec §6).
const DefaultTimeout = 5 * time.Second

const maxResponseBytes = 1 << 20

// Prober asks a Minecraft Java server how many players are online.
type Prober struct {
	addr    string
	host    string
	port    uint16
	timeout time.Duration
	dialer  net.Dialer
}

// New returns a Prober for addr (host:port).
func New(addr string, timeout time.Duration) (*Prober, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("mcjava: invalid address %q: %w", addr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("mcjava: invalid port in %q: %w", addr, err)
	}
	if port == 0 {
		return nil, fmt.Errorf("mcjava: invalid port 0 in %q", addr)
	}
	return &Prober{addr: addr, host: host, port: uint16(port), timeout: timeout}, nil
}

// Probe performs one Server List Ping and returns players.online.
func (p *Prober) Probe(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	conn, err := p.dialer.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return 0, fmt.Errorf("mcjava: dial %s: %w", p.addr, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			return 0, fmt.Errorf("mcjava: set deadline: %w", err)
		}
	}

	request := HandshakePacket(p.host, p.port)
	request = append(request, StatusRequestPacket()...)
	if _, err = conn.Write(request); err != nil {
		return 0, fmt.Errorf("mcjava: write request: %w", err)
	}

	data, err := ReadStatusResponse(bufio.NewReader(conn), maxResponseBytes)
	if err != nil {
		return 0, err
	}
	return ParseOnline(data)
}
