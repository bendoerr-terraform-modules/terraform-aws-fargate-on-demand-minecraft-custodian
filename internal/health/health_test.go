package health_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/health"
)

func status(t *testing.T, h http.Handler, method string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, "/healthz", nil))
	return rec.Code
}

func TestHandlerReportsReadiness(t *testing.T) {
	s := health.New()
	if got := status(t, s.Handler(), http.MethodGet); got != http.StatusServiceUnavailable {
		t.Errorf("initial status = %d; want 503", got)
	}
	s.SetReady(true)
	if got := status(t, s.Handler(), http.MethodGet); got != http.StatusOK {
		t.Errorf("ready status = %d; want 200", got)
	}
	s.SetReady(false)
	if got := status(t, s.Handler(), http.MethodGet); got != http.StatusServiceUnavailable {
		t.Errorf("not-ready status = %d; want 503", got)
	}
	if got := status(t, s.Handler(), http.MethodPost); got != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d; want 405", got)
	}
}

func TestCheckAgainstServer(t *testing.T) {
	s := health.New()
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	t.Cleanup(func() { s.Close() })

	if err := health.Check(t.Context(), s.Addr()); err == nil {
		t.Error("Check() before ready = nil; want error")
	}
	s.SetReady(true)
	if err := health.Check(t.Context(), s.Addr()); err != nil {
		t.Errorf("Check() when ready = %v; want nil", err)
	}
}

func TestCheckUnreachable(t *testing.T) {
	if err := health.Check(t.Context(), "127.0.0.1:1"); err == nil {
		t.Error("Check(unreachable) = nil; want error")
	}
}

func TestServeWithoutListenReturnsError(t *testing.T) {
	if err := health.New().Serve(); err == nil {
		t.Error("Serve() before Listen() = nil; want error")
	}
}
