// SPDX-License-Identifier: AGPL-3.0-only
package ha

import (
	"context"
	"errors"
	"time"
)

// Bounded supervises a real operation and its workers, not merely the wait for it.
// Crash must not return in production. A timeout never counts as positive completion.
type Bounded struct {
	Gate            LeaderGate
	Timeout, Margin time.Duration
	Crash           func()
}

func (b Bounded) Run(ctx context.Context, op Operation, fn func(context.Context) error) error {
	if b.Timeout <= 0 {
		return b.Gate.Do(ctx, op, fn)
	}
	whole, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.Gate.Do(whole, op, fn) }()
	select {
	case err := <-done:
		if whole.Err() != nil {
			return errors.Join(err, whole.Err())
		}
		return err
	case <-whole.Done():
	}
	allowance := time.NewTimer(b.Margin)
	defer allowance.Stop()
	select {
	case err := <-done:
		return errors.Join(err, whole.Err())
	case <-allowance.C:
		b.Crash()
		panic("ha: terminal crash function returned")
	}
}
