package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/retry"
)

// Run executes the lifecycle. Cancelling ctx means SIGTERM. Every exit path runs cleanup.
func (m *Machine) Run(ctx context.Context) int {
	result := make(chan int, 1)
	func() {
		defer func() {
			if r := recover(); r != nil {
				m.log.ErrorContext(ctx, "custodian panicked",
					slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
				m.cleanup("panic")
				result <- ExitError
			}
		}()
		result <- m.run(ctx)
	}()
	return <-result
}

func (m *Machine) run(ctx context.Context) int {
	self, err := m.discover(ctx)
	if err != nil {
		return m.fail(ctx, "discover", err)
	}
	m.log.InfoContext(ctx, "discovered task", slog.String("task", self.TaskARN), slog.String("ip", self.PublicIP))

	if err = m.gate(ctx, self.TaskARN); err != nil {
		return m.fail(ctx, "gate", err)
	}
	m.deps.Health.SetReady(true)

	if err = m.publish(ctx, self.PublicIP); err != nil {
		return m.fail(ctx, "publish dns", err)
	}

	if err = m.awaitReady(ctx); err != nil {
		return m.fail(ctx, "await ready", err)
	}
	m.notify(ctx, EventStart)

	if err = m.watch(ctx); err != nil {
		return m.fail(ctx, "watch", err)
	}

	m.cleanup("idle")
	m.awaitTermination(ctx)
	return ExitOK
}

// fail distinguishes termination (ctx cancelled → ExitOK) from real failures (→ ExitError).
func (m *Machine) fail(ctx context.Context, phase string, err error) int {
	if ctx.Err() != nil {
		m.log.InfoContext(ctx, "terminated", slog.String("phase", phase))
		m.cleanup("terminated during " + phase)
		return ExitOK
	}
	m.log.ErrorContext(ctx, "lifecycle failed", slog.String("phase", phase), slog.Any("error", err))
	m.cleanup(phase + " failed")
	return ExitError
}

func (m *Machine) discover(ctx context.Context) (Self, error) {
	var self Self
	attempt := func(callCtx context.Context) error {
		found, err := m.deps.Discoverer.Discover(callCtx)
		if errors.Is(err, ErrIdentityMismatch) {
			m.foreign = true
			return retry.Permanent(err)
		}
		if err != nil {
			m.log.WarnContext(callCtx, "discover attempt failed", slog.Any("error", err))
			return err
		}
		self = found
		return nil
	}
	err := retry.Do(ctx, m.settings.DiscoverRetry, attempt)
	return self, err
}

func (m *Machine) gate(ctx context.Context, selfTaskARN string) error {
	ctx, cancel := context.WithTimeout(ctx, m.settings.GateTimeout)
	defer cancel()
	for {
		blocking, err := m.deps.Gate.Blocking(ctx, selfTaskARN)
		switch {
		case err != nil:
			m.log.WarnContext(ctx, "gate check failed; will retry", slog.Any("error", err))
		case len(blocking) == 0:
			return nil
		default:
			m.log.InfoContext(ctx, "waiting for previous tasks to stop", slog.Any("tasks", blocking))
		}
		if serr := sleep(ctx, m.settings.GatePoll); serr != nil {
			return fmt.Errorf("previous tasks still running after %s: %w", m.settings.GateTimeout, serr)
		}
	}
}

func (m *Machine) publish(ctx context.Context, ip string) error {
	upsert := func(callCtx context.Context) error { return m.deps.DNS.Upsert(callCtx, ip) }
	if err := retry.Do(ctx, m.settings.DNSRetry, upsert); err != nil {
		return err
	}
	m.published = true
	m.log.InfoContext(ctx, "dns record published", slog.String("ip", ip))
	return nil
}

func (m *Machine) awaitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, m.settings.BootTimeout)
	defer cancel()
	for {
		players, err := m.deps.Watcher.Probe(ctx)
		if err == nil {
			m.log.InfoContext(ctx, "server is ready", slog.Int("players", players))
			return nil
		}
		m.log.DebugContext(ctx, "server not ready yet", slog.Any("error", err))
		if serr := sleep(ctx, m.settings.WatchInterval); serr != nil {
			return fmt.Errorf("server not ready within %s: %w", m.settings.BootTimeout, errors.Join(err, serr))
		}
	}
}

// watch returns nil when the server has had no players for IdleTimeout, or ctx's error on termination.
func (m *Machine) watch(ctx context.Context) error {
	ticker := time.NewTicker(m.settings.WatchInterval)
	defer ticker.Stop()

	active := false
	lastSeen := time.Now()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		players, err := m.deps.Watcher.Probe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			failures++
			players = 0
			level := slog.LevelWarn
			if failures >= m.settings.ProbeFailWarn {
				level = slog.LevelError
			}
			m.log.Log(ctx, level, "probe failed; counting as 0 players",
				slog.Int("consecutive_failures", failures), slog.Any("error", err))
		} else {
			failures = 0
		}

		now := time.Now()
		if players > 0 {
			lastSeen = now
			if !active {
				active = true
				m.notify(ctx, EventActive)
			}
			continue
		}
		if active {
			active = false
			m.notify(ctx, EventInactive)
		}
		if idle := now.Sub(lastSeen); idle >= m.settings.IdleTimeout {
			// A string, not slog.Duration: the JSON handler would log raw nanoseconds.
			m.log.InfoContext(
				ctx,
				"idle timeout reached",
				slog.String("idle", idle.Truncate(time.Millisecond).String()),
			)
			return nil
		}
	}
}

// awaitTermination waits for ECS's SIGTERM after an idle reap, or gives up after PostReapWait.
func (m *Machine) awaitTermination(ctx context.Context) {
	if err := sleep(ctx, m.settings.PostReapWait); err == nil {
		m.log.WarnContext(ctx, "no SIGTERM after reap; exiting", slog.Duration("waited", m.settings.PostReapWait))
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
