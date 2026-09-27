package retry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/retry"
)

var errBoom = errors.New("boom")

func policy(deadline time.Duration) retry.Policy {
	return retry.Policy{Initial: time.Millisecond, Max: 2 * time.Millisecond, Deadline: deadline}
}

func TestDoSucceedsFirstTry(t *testing.T) {
	calls := 0
	err := retry.Do(t.Context(), policy(time.Second), func(context.Context) error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("Do() = %v after %d calls; want nil after 1", err, calls)
	}
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	calls := 0
	err := retry.Do(t.Context(), policy(time.Second), func(context.Context) error {
		calls++
		if calls < 3 {
			return errBoom
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("Do() = %v after %d calls; want nil after 3", err, calls)
	}
}

func TestDoGivesUpAtDeadline(t *testing.T) {
	calls := 0
	err := retry.Do(t.Context(), policy(20*time.Millisecond), func(context.Context) error {
		calls++
		return errBoom
	})
	if !errors.Is(err, errBoom) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Do() error = %v; want it to wrap errBoom and context.DeadlineExceeded", err)
	}
	if calls < 3 {
		t.Errorf("calls = %d; want at least 3 attempts within the deadline", calls)
	}
}

func TestDoStopsWhenParentCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	start := time.Now()
	err := retry.Do(ctx, policy(time.Minute), func(context.Context) error {
		calls++
		cancel()
		return errBoom
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("Do() = %v after %d calls; want context.Canceled after 1", err, calls)
	}
	if time.Since(start) > time.Second {
		t.Errorf("Do() took %s after cancel; want prompt return", time.Since(start))
	}
}

func TestDoGivesFnADeadline(t *testing.T) {
	err := retry.Do(t.Context(), policy(time.Second), func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("no deadline")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
}
