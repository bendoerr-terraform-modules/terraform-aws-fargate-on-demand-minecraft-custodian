// Package lifecycle drives the custodian from boot to reap (spec §4).
package lifecycle

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/retry"
)

// Event is an SNS event type (spec §3.1).
type Event string

// Events consumed by the notice Lambdas.
const (
	EventStart    Event = "start"
	EventStop     Event = "stop"
	EventActive   Event = "active"
	EventInactive Event = "inactive"
)

// Process exit codes.
const (
	ExitOK    = 0
	ExitError = 1
)

// Self identifies the running task.
type Self struct {
	TaskARN  string
	PublicIP string
}

// Discoverer finds the running task's ARN and public IP.
type Discoverer interface {
	Discover(ctx context.Context) (Self, error)
}

// Gate reports other tasks of the same service that may still hold the world.
type Gate interface {
	Blocking(ctx context.Context, selfTaskARN string) ([]string, error)
}

// DNS points the server's A record at ip.
type DNS interface {
	Upsert(ctx context.Context, ip string) error
}

// Reaper sets the ECS service's desired count to 0.
type Reaper interface {
	Reap(ctx context.Context) error
}

// Notifier publishes lifecycle events.
type Notifier interface {
	Notify(ctx context.Context, event Event) error
}

// Watcher reports how many players are online.
type Watcher interface {
	Probe(ctx context.Context) (int, error)
}

// Health reports readiness to the ECS health check.
type Health interface {
	SetReady(ready bool)
}

// Deps are the Machine's collaborators.
type Deps struct {
	Discoverer Discoverer
	Gate       Gate
	DNS        DNS
	Reaper     Reaper
	Notifier   Notifier
	Watcher    Watcher
	Health     Health
	Logger     *slog.Logger
}

// Settings are the Machine's timings and retry policies.
type Settings struct {
	ParkedIP        string
	WatchInterval   time.Duration
	IdleTimeout     time.Duration
	BootTimeout     time.Duration
	GateTimeout     time.Duration
	GatePoll        time.Duration
	PostReapWait    time.Duration
	ProbeFailWarn   int
	DiscoverRetry   retry.Policy
	DNSRetry        retry.Policy
	ReapRetry       retry.Policy
	BestEffortRetry retry.Policy
}

const (
	defaultWatchInterval = 30 * time.Second
	defaultIdleTimeout   = 10 * time.Minute
	defaultBootTimeout   = 10 * time.Minute
	defaultGateTimeout   = 3 * time.Minute
	defaultGatePoll      = 5 * time.Second
	defaultPostReapWait  = 5 * time.Minute
	defaultProbeFailWarn = 3

	lookupInitial     = time.Second
	lookupMax         = 15 * time.Second
	lookupDeadline    = 2 * time.Minute
	reapInitial       = time.Second
	reapMax           = 10 * time.Second
	reapDeadline      = time.Minute
	bestEffortInitial = 500 * time.Millisecond
	bestEffortMax     = 2 * time.Second
	bestEffortLimit   = 10 * time.Second
)

// DefaultSettings returns the spec defaults (§5, §9.1).
func DefaultSettings() Settings {
	lookup := retry.Policy{Initial: lookupInitial, Max: lookupMax, Deadline: lookupDeadline}
	return Settings{
		WatchInterval:   defaultWatchInterval,
		IdleTimeout:     defaultIdleTimeout,
		BootTimeout:     defaultBootTimeout,
		GateTimeout:     defaultGateTimeout,
		GatePoll:        defaultGatePoll,
		PostReapWait:    defaultPostReapWait,
		ProbeFailWarn:   defaultProbeFailWarn,
		DiscoverRetry:   lookup,
		DNSRetry:        lookup,
		ReapRetry:       retry.Policy{Initial: reapInitial, Max: reapMax, Deadline: reapDeadline},
		BestEffortRetry: retry.Policy{Initial: bestEffortInitial, Max: bestEffortMax, Deadline: bestEffortLimit},
	}
}

// Machine runs the custodian lifecycle.
type Machine struct {
	deps      Deps
	settings  Settings
	log       *slog.Logger
	cleanOnce sync.Once
	published bool
}

// New builds a Machine. A nil Logger discards logs.
func New(deps Deps, settings Settings) *Machine {
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Machine{deps: deps, settings: settings, log: logger}
}

// Abort runs cleanup without running the lifecycle, for setup failures after config is valid.
func (m *Machine) Abort(reason string) int {
	m.cleanup(reason)
	return ExitError
}

// cleanup reaps the service, parks DNS if it was published, and emits stop. It runs at most once.
func (m *Machine) cleanup(reason string) {
	m.cleanOnce.Do(func() {
		ctx := context.Background()
		m.log.InfoContext(ctx, "cleanup starting", slog.String("reason", reason))
		m.deps.Health.SetReady(false)

		if err := retry.Do(ctx, m.settings.ReapRetry, m.deps.Reaper.Reap); err != nil {
			m.log.ErrorContext(ctx, "reap failed; the service may keep running", slog.Any("error", err))
		} else {
			m.log.InfoContext(ctx, "service desired count set to 0")
		}

		if m.published && m.settings.ParkedIP != "" {
			park := func(callCtx context.Context) error { return m.deps.DNS.Upsert(callCtx, m.settings.ParkedIP) }
			if err := retry.Do(ctx, m.settings.BestEffortRetry, park); err != nil {
				m.log.WarnContext(ctx, "parking the DNS record failed", slog.Any("error", err))
			}
		}

		m.notify(ctx, EventStop)
		m.log.InfoContext(ctx, "cleanup finished")
	})
}

// notify publishes an event best-effort; failures are logged, never returned.
func (m *Machine) notify(ctx context.Context, event Event) {
	send := func(callCtx context.Context) error { return m.deps.Notifier.Notify(callCtx, event) }
	if err := retry.Do(ctx, m.settings.BestEffortRetry, send); err != nil {
		m.log.WarnContext(ctx, "event not delivered", slog.String("event", string(event)), slog.Any("error", err))
		return
	}
	m.log.InfoContext(ctx, "event emitted", slog.String("event", string(event)))
}
