// Package retry runs an operation with exponential backoff under a deadline.
package retry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Policy bounds a retried operation.
type Policy struct {
	Initial  time.Duration // wait after the first failure
	Max      time.Duration // cap for the doubling wait
	Deadline time.Duration // total time allowed, including attempts
}

// Do calls fn until it succeeds, the deadline passes, or ctx is cancelled.
func Do(ctx context.Context, p Policy, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, p.Deadline)
	defer cancel()

	delay := p.Initial
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("gave up after %d attempts: %w", attempt, errors.Join(err, ctx.Err()))
		case <-timer.C:
		}
		delay = min(delay*2, p.Max)
	}
}
