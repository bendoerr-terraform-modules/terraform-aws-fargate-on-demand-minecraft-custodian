package lifecycle_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/lifecycle"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/retry"
)

func TestAbortReapsThenNotifiesStop(t *testing.T) {
	h := newHarness()
	if code := h.machine().Abort("setup failed"); code != lifecycle.ExitError {
		t.Errorf("Abort() = %d; want ExitError", code)
	}
	want := []string{"health:false", "reap", "notify:stop"}
	if got := h.rec.names(); !slices.Equal(got, want) {
		t.Errorf("calls = %v; want %v", got, want)
	}
}

func TestAbortRunsCleanupOnce(t *testing.T) {
	h := newHarness()
	m := h.machine()
	m.Abort("first")
	m.Abort("second")
	if h.rec.count("reap") != 1 || h.rec.count("notify:stop") != 1 {
		t.Errorf("calls = %v; want exactly one reap and one stop", h.rec.names())
	}
}

func TestCleanupContinuesWhenReapFails(t *testing.T) {
	h := newHarness()
	h.reaper.fn = func() error { return errors.New("throttled") }
	h.machine().Abort("setup failed")
	if h.rec.count("reap") < 2 {
		t.Errorf("reap attempts = %d; want retries", h.rec.count("reap"))
	}
	calls := h.rec.names()
	if calls[len(calls)-1] != "notify:stop" {
		t.Errorf("last call = %q; want notify:stop even after reap failure (calls %v)", calls[len(calls)-1], calls)
	}
}

func TestAbortDoesNotParkUnpublishedDNS(t *testing.T) {
	h := newHarness()
	h.machine().Abort("setup failed")
	if got := h.rec.only("dns:"); len(got) != 0 {
		t.Errorf("dns calls = %v; want none before the record was published", got)
	}
}

func TestDefaultSettingsMatchSpec(t *testing.T) {
	s := lifecycle.DefaultSettings()
	want := lifecycle.Settings{
		WatchInterval: 30 * time.Second,
		IdleTimeout:   10 * time.Minute,
		BootTimeout:   10 * time.Minute,
		GateTimeout:   3 * time.Minute,
		GatePoll:      5 * time.Second,
		PostReapWait:  5 * time.Minute,
		ProbeFailWarn: 3,
		DiscoverRetry: retry.Policy{Initial: time.Second, Max: 15 * time.Second, Deadline: 2 * time.Minute},
		DNSRetry:      retry.Policy{Initial: time.Second, Max: 15 * time.Second, Deadline: 2 * time.Minute},
		ReapRetry:     retry.Policy{Initial: time.Second, Max: 10 * time.Second, Deadline: time.Minute},
		BestEffortRetry: retry.Policy{
			Initial:  500 * time.Millisecond,
			Max:      2 * time.Second,
			Deadline: 10 * time.Second,
		},
	}
	if s != want {
		t.Errorf("DefaultSettings() =\n%+v\nwant\n%+v", s, want)
	}
}
