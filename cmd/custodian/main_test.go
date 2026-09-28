package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/config"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/health"
)

func emptyEnv(string) string { return "" }

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run(t.Context(), []string{"version"}, emptyEnv, &out); code != 0 {
		t.Errorf("run(version) = %d; want 0", code)
	}
	if out.String() != "dev\n" {
		t.Errorf("version output = %q; want %q", out.String(), "dev\n")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	if code := run(t.Context(), []string{"frobnicate"}, emptyEnv, &out); code != 2 {
		t.Errorf("run(frobnicate) = %d; want 2", code)
	}
}

func TestRunInvalidConfig(t *testing.T) {
	var out bytes.Buffer
	if code := run(t.Context(), nil, emptyEnv, &out); code != 1 {
		t.Errorf("run() with empty env = %d; want 1", code)
	}
	for _, want := range []string{"invalid configuration", "CUSTODIAN_CLUSTER"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("log output %q; want it to contain %q", out.String(), want)
		}
	}
}

func TestHealthcheckCommand(t *testing.T) {
	s := health.New()
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	t.Cleanup(func() { s.Close() })
	_, port, _ := strings.Cut(s.Addr(), ":")
	env := func(k string) string {
		if k == config.EnvHealthPort {
			return port
		}
		return ""
	}
	var out bytes.Buffer
	if code := run(t.Context(), []string{"healthcheck"}, env, &out); code != 1 {
		t.Errorf("healthcheck before ready = %d; want 1", code)
	}
	s.SetReady(true)
	if code := run(t.Context(), []string{"healthcheck"}, env, &out); code != 0 {
		t.Errorf("healthcheck when ready = %d; want 0", code)
	}
}

func TestSettingsFromConfig(t *testing.T) {
	cfg := config.Config{
		DNSParkedIP:   "192.0.2.1",
		WatchInterval: 15 * time.Second,
		IdleTimeout:   20 * time.Minute,
		BootTimeout:   5 * time.Minute,
		GateTimeout:   4 * time.Minute,
		ProbeFailWarn: 7,
	}
	s := settings(cfg)
	if s.ParkedIP != "192.0.2.1" || s.WatchInterval != 15*time.Second || s.IdleTimeout != 20*time.Minute ||
		s.BootTimeout != 5*time.Minute || s.GateTimeout != 4*time.Minute || s.ProbeFailWarn != 7 {
		t.Errorf("settings() = %+v", s)
	}
	if s.GatePoll != 5*time.Second || s.PostReapWait != 5*time.Minute {
		t.Errorf("settings() lost defaults: %+v", s)
	}
}
