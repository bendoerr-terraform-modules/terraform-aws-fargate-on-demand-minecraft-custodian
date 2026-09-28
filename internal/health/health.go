// Package health serves the loopback readiness endpoint that gates the Minecraft container (spec §4.2).
package health

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	path              = "/healthz"
	readHeaderTimeout = 2 * time.Second
)

// Server reports readiness over HTTP.
type Server struct {
	ready atomic.Bool
	srv   *http.Server
	ln    net.Listener
}

// New returns a Server that is not ready and not listening.
func New() *Server {
	s := &Server{}
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: readHeaderTimeout}
	return s
}

// SetReady sets what the endpoint reports.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

// Handler serves GET /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
		if s.ready.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok\n")
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "not ready\n")
	})
	return mux
}

// Listen binds addr (use 127.0.0.1:<port>).
func (s *Server) Listen(addr string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("health: listen %s: %w", addr, err)
	}
	s.ln = ln
	return nil
}

// Addr returns the bound address, or "" before Listen.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Serve serves until Close; it returns nil after Close.
func (s *Server) Serve() error {
	if s.ln == nil {
		return errors.New("health: Serve called before Listen")
	}
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("health: serve: %w", err)
	}
	return nil
}

// Close stops the server.
func (s *Server) Close() error {
	return s.srv.Close()
}

// Check returns nil when http://addr/healthz answers 200.
func Check(ctx context.Context, addr string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return fmt.Errorf("health: build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("health: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: status %d", resp.StatusCode)
	}
	return nil
}
