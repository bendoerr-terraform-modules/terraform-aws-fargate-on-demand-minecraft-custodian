package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/lifecycle"
)

// runTestTimeout bounds how long a test waits for Machine.Run to return.
const runTestTimeout = 2 * time.Second

// runWithin runs m and fails the test if it doesn't return within runTestTimeout.
func runWithin(ctx context.Context, t *testing.T, m *lifecycle.Machine) int {
	t.Helper()
	done := make(chan int, 1)
	go func() { done <- m.Run(ctx) }()
	select {
	case code := <-done:
		return code
	case <-time.After(runTestTimeout):
		t.Fatalf("Run did not return within %s", runTestTimeout)
		return -1
	}
}

func TestRunIdleShutdown(t *testing.T) {
	h := newHarness()
	code := runWithin(t.Context(), t, h.machine())
	if code != lifecycle.ExitOK {
		t.Errorf("Run() = %d; want ExitOK", code)
	}
	want := []string{
		"discover", "gate:clear", "health:true", "dns:" + publicIP, "notify:start",
		"health:false", "reap", "dns:" + parkedIP, "notify:stop",
	}
	if got := h.rec.names("probe:"); !slices.Equal(got, want) {
		t.Errorf("calls =\n%v\nwant\n%v", got, want)
	}
}

func TestRunNoParkWithoutParkedIP(t *testing.T) {
	h := newHarness()
	h.settings.ParkedIP = ""
	runWithin(t.Context(), t, h.machine())
	if got := h.rec.only("dns:"); !slices.Equal(got, []string{"dns:" + publicIP}) {
		t.Errorf("dns calls = %v; want only the publish", got)
	}
}

func TestRunStartOnlyAfterReady(t *testing.T) {
	h := newHarness()
	h.settings.BootTimeout = time.Second // not under test; wide enough to absorb a scheduler stall
	h.watcher.fn = func(call int) (int, error) {
		if call <= 3 {
			return 0, errors.New("connection refused")
		}
		return 0, nil
	}
	runWithin(t.Context(), t, h.machine())
	dns := h.rec.index("dns:" + publicIP)
	firstOK := h.rec.index("probe:0")
	start := h.rec.index("notify:start")
	if firstOK != dns+4 || start != firstOK+1 {
		t.Errorf("want dns, 3 failed probes, first ok probe, then start; got %v", h.rec.names())
	}
}

func TestRunActiveInactiveTransitions(t *testing.T) {
	h := newHarness()
	h.settings.IdleTimeout = 500 * time.Millisecond // not under test; wide enough to absorb a scheduler stall
	readings := []int{0, 1, 2, 2, 0, 3, 0}
	h.watcher.fn = func(call int) (int, error) {
		if call <= len(readings) {
			return readings[call-1], nil
		}
		return 0, nil
	}
	runWithin(t.Context(), t, h.machine())
	want := []string{"notify:active", "notify:inactive", "notify:active", "notify:inactive"}
	if got := h.rec.only("notify:active", "notify:inactive"); !slices.Equal(got, want) {
		t.Errorf("transition events = %v; want %v", got, want)
	}
}

// Review Focus 2: someone joined while the server was booting.
func TestRunPlayersOnlineAtReady(t *testing.T) {
	h := newHarness()
	h.watcher.fn = func(call int) (int, error) {
		if call <= 3 {
			return 2, nil
		}
		return 0, nil
	}
	code := runWithin(t.Context(), t, h.machine())
	want := []string{"notify:active", "notify:inactive"}
	if got := h.rec.only("notify:active", "notify:inactive"); code != lifecycle.ExitOK || !slices.Equal(got, want) {
		t.Errorf("Run() = %d, events %v; want ExitOK, %v", code, got, want)
	}
	// Expected order: probe:2 (ready), notify:start, probe:2 (first watch tick), notify:active.
	if h.rec.index("notify:active") != h.rec.index("probe:2")+3 {
		t.Errorf("active not emitted on the first watch tick: %v", h.rec.names())
	}
}

func TestRunProbeErrorsCountAsZero(t *testing.T) {
	h := newHarness()
	h.watcher.fn = func(call int) (int, error) {
		switch {
		case call == 1:
			return 0, nil
		case call <= 3:
			return 2, nil
		default:
			return 0, errors.New("i/o timeout")
		}
	}
	code := runWithin(t.Context(), t, h.machine())
	want := []string{"notify:active", "notify:inactive"}
	if got := h.rec.only("notify:active", "notify:inactive"); code != lifecycle.ExitOK || !slices.Equal(got, want) {
		t.Errorf("Run() = %d, events %v; want ExitOK, %v", code, got, want)
	}
	if h.rec.count("reap") != 1 {
		t.Errorf("reap count = %d; want 1", h.rec.count("reap"))
	}
}

func TestRunIdleClockResetsOnPlayers(t *testing.T) {
	h := newHarness()
	var firstWatch, lastPositive time.Time
	h.watcher.fn = func(call int) (int, error) {
		if call == 1 {
			return 0, nil
		}
		if firstWatch.IsZero() {
			firstWatch = time.Now()
		}
		if time.Since(firstWatch) < 15*time.Millisecond {
			lastPositive = time.Now()
			return 1, nil
		}
		return 0, nil
	}
	runWithin(t.Context(), t, h.machine())
	reapAt, ok := h.rec.at("reap")
	if !ok {
		t.Fatal("no reap")
	}
	if idle := reapAt.Sub(lastPositive); idle < h.settings.IdleTimeout {
		t.Errorf("reaped %s after the last player left; want at least %s", idle, h.settings.IdleTimeout)
	}
}

// Long sessions must never reap while players are online, no matter how long the session runs.
func TestRunNeverReapsDuringLongSession(t *testing.T) {
	h := newHarness()
	var firstWatch, lastPositive time.Time
	h.watcher.fn = func(call int) (int, error) {
		if call == 1 {
			return 0, nil // ready
		}
		if firstWatch.IsZero() {
			firstWatch = time.Now()
		}
		if time.Since(firstWatch) < 5*h.settings.IdleTimeout {
			lastPositive = time.Now()
			return 1, nil
		}
		return 0, nil
	}
	code := runWithin(t.Context(), t, h.machine())
	if code != lifecycle.ExitOK {
		t.Errorf("Run() = %d; want ExitOK", code)
	}
	reapAt, ok := h.rec.at("reap")
	if !ok {
		t.Fatal("no reap")
	}
	if elapsed := reapAt.Sub(firstWatch); elapsed < 5*h.settings.IdleTimeout {
		t.Errorf("reaped %s after the first watch call; want at least %s", elapsed, 5*h.settings.IdleTimeout)
	}
	if idle := reapAt.Sub(lastPositive); idle < h.settings.IdleTimeout {
		t.Errorf("reaped %s after the last positive reading; want at least %s", idle, h.settings.IdleTimeout)
	}
	want := []string{"notify:active", "notify:inactive"}
	if got := h.rec.only("notify:active", "notify:inactive"); !slices.Equal(got, want) {
		t.Errorf("transition events = %v; want %v", got, want)
	}
}

func TestRunTerminationInEachState(t *testing.T) {
	interrupted := errors.New("interrupted")
	tests := []struct {
		name     string
		setup    func(h *harness, cancel context.CancelFunc)
		wantPark bool
	}{
		{"discover", func(h *harness, cancel context.CancelFunc) {
			h.discoverer.fn = func() (lifecycle.Self, error) { cancel(); return lifecycle.Self{}, interrupted }
		}, false},
		{"gate", func(h *harness, cancel context.CancelFunc) {
			h.gate.fn = func(int) ([]string, error) { cancel(); return []string{"arn:other"}, nil }
		}, false},
		{"dns publish", func(h *harness, cancel context.CancelFunc) {
			h.dns.fn = func(ip string) error {
				if ip == publicIP {
					cancel()
					return interrupted
				}
				return nil
			}
		}, false},
		{"await ready", func(h *harness, cancel context.CancelFunc) {
			h.watcher.fn = func(int) (int, error) { cancel(); return 0, interrupted }
		}, true},
		{"watch", func(h *harness, cancel context.CancelFunc) {
			h.watcher.fn = func(call int) (int, error) {
				if call >= 3 {
					cancel()
				}
				return 1, nil
			}
		}, true},
		{"post-reap wait", func(h *harness, cancel context.CancelFunc) {
			h.settings.PostReapWait = time.Hour
			h.reaper.fn = func() error { cancel(); return nil }
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tt.setup(h, cancel)
			code := runWithin(ctx, t, h.machine())
			if code != lifecycle.ExitOK {
				t.Errorf("Run() = %d; want ExitOK on termination", code)
			}
			if h.rec.count("reap") != 1 || h.rec.count("notify:stop") != 1 {
				t.Errorf("calls = %v; want exactly one reap and one stop", h.rec.names("probe:"))
			}
			if parked := h.rec.count("dns:"+parkedIP) == 1; parked != tt.wantPark {
				t.Errorf("parked = %t; want %t (calls %v)", parked, tt.wantPark, h.rec.names("probe:"))
			}
		})
	}
}

func TestRunFailuresExitError(t *testing.T) {
	failure := errors.New("denied")
	tests := []struct {
		name     string
		setup    func(h *harness)
		wantPark bool
	}{
		{"discover fails", func(h *harness) {
			h.discoverer.fn = func() (lifecycle.Self, error) { return lifecycle.Self{}, failure }
		}, false},
		{"gate never clears", func(h *harness) {
			h.gate.fn = func(int) ([]string, error) { return []string{"arn:other"}, nil }
		}, false},
		{"dns publish fails", func(h *harness) {
			h.dns.fn = func(ip string) error {
				if ip == publicIP {
					return failure
				}
				return nil
			}
		}, false},
		{"server never ready", func(h *harness) {
			h.watcher.fn = func(int) (int, error) { return 0, errors.New("connection refused") }
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness()
			tt.setup(h)
			code := runWithin(t.Context(), t, h.machine())
			if code != lifecycle.ExitError {
				t.Errorf("Run() = %d; want ExitError", code)
			}
			if h.rec.count("reap") != 1 || h.rec.count("notify:stop") != 1 || h.rec.count("notify:start") != 0 {
				t.Errorf("calls = %v; want one reap, one stop, no start", h.rec.names("probe:", "gate:"))
			}
			if parked := h.rec.count("dns:"+parkedIP) == 1; parked != tt.wantPark {
				t.Errorf("parked = %t; want %t", parked, tt.wantPark)
			}
		})
	}
}

func TestRunPanicInWatcher(t *testing.T) {
	h := newHarness()
	h.watcher.fn = func(call int) (int, error) {
		if call == 2 {
			panic("boom")
		}
		return 0, nil
	}
	code := runWithin(t.Context(), t, h.machine())
	if code != lifecycle.ExitError {
		t.Errorf("Run() = %d; want ExitError after panic", code)
	}
	if h.rec.count("reap") != 1 || h.rec.count("dns:"+parkedIP) != 1 || h.rec.count("notify:stop") != 1 {
		t.Errorf("calls = %v; want reap, park, stop once each", h.rec.names("probe:"))
	}
}

func TestRunHealthReadyOnlyAfterGateClears(t *testing.T) {
	h := newHarness()
	h.settings.GateTimeout = time.Second // not under test; wide enough to absorb a scheduler stall
	h.gate.fn = func(call int) ([]string, error) {
		switch {
		case call == 1:
			return nil, errors.New("throttled")
		case call <= 4:
			return []string{"arn:previous"}, nil
		default:
			return nil, nil
		}
	}
	code := runWithin(t.Context(), t, h.machine())
	if code != lifecycle.ExitOK {
		t.Errorf("Run() = %d; want ExitOK", code)
	}
	if h.rec.count("gate:blocked") != 3 || h.rec.count("gate:error") != 1 {
		t.Errorf("gate calls = %v; want 1 error then 3 blocked", h.rec.only("gate:"))
	}
	if h.rec.index("health:true") < h.rec.index("gate:clear") {
		t.Errorf("health became ready before the gate cleared: %v", h.rec.names("probe:"))
	}
}

// A config that names a different cluster/service must never be reaped: it is not this task's.
func TestRunIdentityMismatchRefusesToReap(t *testing.T) {
	h := newHarness()
	h.discoverer.fn = func() (lifecycle.Self, error) {
		return lifecycle.Self{}, fmt.Errorf("%w: task service minecraft, configured foundry",
			lifecycle.ErrIdentityMismatch)
	}
	code := runWithin(t.Context(), t, h.machine())
	if code != lifecycle.ExitError {
		t.Errorf("Run() = %d; want ExitError", code)
	}
	want := []string{"discover", "health:false"}
	if got := h.rec.names(); !slices.Equal(got, want) {
		t.Errorf("calls = %v; want %v (no retry, no reap, no park, no stop)", got, want)
	}
}
