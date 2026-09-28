// Package retry runs an operation with exponential backoff under a deadline.
package retry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// backoffMultiplier is the factor the delay is scaled by after each failed attempt.
const backoffMultiplier = 2

// Policy bounds a retried operation.
type Policy struct {
	Initial  time.Duration // wait after the first failure
	Max      time.Duration // cap for the doubling wait
	Deadline time.Duration // total time allowed, including attempts
}

// permanentError marks an error that retrying cannot fix.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent wraps err so Do returns it immediately instead of retrying.
func Permanent(err error) error {
	return permanentError{err: err}
}

// Do calls fn until it succeeds, fn returns a Permanent error, the deadline passes, or ctx is cancelled.
func Do(ctx context.Context, p Policy, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, p.Deadline)
	defer cancel()

	delay := p.Initial
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		var perm permanentError
		if errors.As(err, &perm) {
			return perm.err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("gave up after %d attempts: %w", attempt, errors.Join(err, ctx.Err()))
		case <-timer.C:
		}
		delay = min(delay*backoffMultiplier, p.Max)
	}
}
