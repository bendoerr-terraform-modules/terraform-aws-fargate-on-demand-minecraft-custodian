package lifecycle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/lifecycle"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/retry"
)

const (
	selfARN  = "arn:aws:ecs:us-east-1:123456789012:task/shanecraft/self"
	publicIP = "203.0.113.10"
	parkedIP = "192.0.2.1"
)

type call struct {
	name string
	at   time.Time
}

// recorder keeps an ordered, timestamped log of every fake call.
type recorder struct {
	mu    sync.Mutex
	calls []call
}

func (r *recorder) add(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{name: name, at: time.Now()})
}

// names returns call names, dropping any that start with one of skip.
func (r *recorder) names(skip ...string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, c := range r.calls {
		if !hasPrefix(c.name, skip) {
			out = append(out, c.name)
		}
	}
	return out
}

// only returns call names that start with one of keep.
func (r *recorder) only(keep ...string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, c := range r.calls {
		if hasPrefix(c.name, keep) {
			out = append(out, c.name)
		}
	}
	return out
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c.name == name {
			n++
		}
	}
	return n
}

// index returns the position of the first call named name, or -1.
func (r *recorder) index(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, c := range r.calls {
		if c.name == name {
			return i
		}
	}
	return -1
}

// at returns the time of the first call named name.
func (r *recorder) at(name string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c.name == name {
			return c.at, true
		}
	}
	return time.Time{}, false
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

type fakeDiscoverer struct {
	rec *recorder
	fn  func() (lifecycle.Self, error)
}

func (f *fakeDiscoverer) Discover(context.Context) (lifecycle.Self, error) {
	f.rec.add("discover")
	return f.fn()
}

type fakeGate struct {
	rec   *recorder
	calls atomic.Int32
	fn    func(call int) ([]string, error)
}

func (f *fakeGate) Blocking(_ context.Context, _ string) ([]string, error) {
	blocking, err := f.fn(int(f.calls.Add(1)))
	switch {
	case err != nil:
		f.rec.add("gate:error")
	case len(blocking) > 0:
		f.rec.add("gate:blocked")
	default:
		f.rec.add("gate:clear")
	}
	return blocking, err
}

type fakeDNS struct {
	rec *recorder
	fn  func(ip string) error
}

func (f *fakeDNS) Upsert(_ context.Context, ip string) error {
	f.rec.add("dns:" + ip)
	return f.fn(ip)
}

type fakeReaper struct {
	rec *recorder
	fn  func() error
}

func (f *fakeReaper) Reap(context.Context) error {
	f.rec.add("reap")
	return f.fn()
}

type fakeNotifier struct {
	rec *recorder
	err error // returned from every Notify when set
}

func (f *fakeNotifier) Notify(_ context.Context, e lifecycle.Event) error {
	f.rec.add("notify:" + string(e))
	return f.err
}

type fakeWatcher struct {
	rec   *recorder
	calls atomic.Int32
	fn    func(call int) (int, error)
}

func (f *fakeWatcher) Probe(context.Context) (int, error) {
	n, err := f.fn(int(f.calls.Add(1)))
	if err != nil {
		f.rec.add("probe:err")
	} else {
		f.rec.add(fmt.Sprintf("probe:%d", n))
	}
	return n, err
}

type fakeHealth struct {
	rec *recorder
}

func (f *fakeHealth) SetReady(ready bool) {
	f.rec.add(fmt.Sprintf("health:%t", ready))
}

type harness struct {
	rec        *recorder
	discoverer *fakeDiscoverer
	gate       *fakeGate
	dns        *fakeDNS
	reaper     *fakeReaper
	notifier   *fakeNotifier
	watcher    *fakeWatcher
	health     *fakeHealth
	logs       *logCapture
	settings   lifecycle.Settings
}

func newHarness() *harness {
	rec := &recorder{}
	return &harness{
		rec: rec,
		discoverer: &fakeDiscoverer{rec: rec, fn: func() (lifecycle.Self, error) {
			return lifecycle.Self{TaskARN: selfARN, PublicIP: publicIP}, nil
		}},
		gate:     &fakeGate{rec: rec, fn: func(int) ([]string, error) { return nil, nil }},
		dns:      &fakeDNS{rec: rec, fn: func(string) error { return nil }},
		reaper:   &fakeReaper{rec: rec, fn: func() error { return nil }},
		notifier: &fakeNotifier{rec: rec},
		watcher:  &fakeWatcher{rec: rec, fn: func(int) (int, error) { return 0, nil }},
		health:   &fakeHealth{rec: rec},
		logs:     &logCapture{},
		settings: testSettings(),
	}
}

// testSettings uses millisecond timings so lifecycle tests run fast with real time.
func testSettings() lifecycle.Settings {
	fast := retry.Policy{Initial: time.Millisecond, Max: 2 * time.Millisecond, Deadline: 20 * time.Millisecond}
	return lifecycle.Settings{
		ParkedIP:        parkedIP,
		WatchInterval:   2 * time.Millisecond,
		IdleTimeout:     20 * time.Millisecond,
		BootTimeout:     50 * time.Millisecond,
		GateTimeout:     30 * time.Millisecond,
		GatePoll:        2 * time.Millisecond,
		PostReapWait:    10 * time.Millisecond,
		ProbeFailWarn:   3,
		DiscoverRetry:   fast,
		DNSRetry:        fast,
		ReapRetry:       fast,
		BestEffortRetry: fast,
	}
}

func (h *harness) machine() *lifecycle.Machine {
	return lifecycle.New(lifecycle.Deps{
		Discoverer: h.discoverer,
		Gate:       h.gate,
		DNS:        h.dns,
		Reaper:     h.reaper,
		Notifier:   h.notifier,
		Watcher:    h.watcher,
		Health:     h.health,
		Logger:     slog.New(slog.NewJSONHandler(h.logs, nil)),
	}, h.settings)
}

// logCapture collects the machine's JSON log lines; safe for concurrent writes.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

// record returns the attributes of the first log record with msg, or nil.
func (c *logCapture) record(msg string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range bytes.Split(c.buf.Bytes(), []byte("\n")) {
		var rec map[string]any
		if json.Unmarshal(line, &rec) == nil && rec["msg"] == msg {
			return rec
		}
	}
	return nil
}
