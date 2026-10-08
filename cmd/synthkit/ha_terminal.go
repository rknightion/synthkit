// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"sync"

	"github.com/rknightion/synthkit/internal/ha"
)

// haTerminal orders fatal selection against handoff admission and both exit
// branches. Context cancellation bounds I/O but cannot arbitrate these decisions.
// This mutex is separate from the gate and is NEVER held during handoff I/O.
// An action admitted before fatal selection can still be in flight; its context
// is canceled and process exit remains the terminal fence.
type haTerminal struct {
	mu     sync.Mutex
	fatal  bool
	gate   *ha.Gate
	cancel context.CancelFunc
	exit   func(int) // non-returning, including in subprocess tests
}

func (t *haTerminal) crash() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fatal = true
	t.cancel()
	t.gate.Revoke()
	// Keep exit selection serialized through the non-returning call. In
	// particular, a deliberately delayed test exit must not allow exit(0).
	t.exit(1)
	panic("HA crash returned")
}

func (t *haTerminal) finish(code int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fatal {
		code = 1
	}
	t.exit(code)
	panic("HA terminal exit returned")
}

func (t *haTerminal) admit(ctx context.Context, fn func(context.Context) error) error {
	t.mu.Lock()
	if t.fatal {
		t.mu.Unlock()
		return ha.ErrNotLeader
	}
	if err := ctx.Err(); err != nil {
		t.mu.Unlock()
		return err
	}
	// This is the action's admission point, not a distributed fencing token.
	t.mu.Unlock()
	return fn(ctx)
}
