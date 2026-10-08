// SPDX-License-Identifier: AGPL-3.0-only

// Package ha provides terminal, process-local admission fencing. It is not a
// distributed fencing token: requests admitted before revocation can remain in flight.
package ha

import (
	"context"
	"errors"
	"sync"
)

type Role string

const (
	RoleStandby Role = "standby"
	RoleLeader  Role = "leader"
)

var ErrNotLeader = errors.New("not leader")

type Operation uint8

const (
	Mutation Operation = iota
	Delivery
)

type LeaderGate interface {
	Role() Role
	Do(context.Context, Operation, func(context.Context) error) error
}
type AlwaysLeader struct{}

func (AlwaysLeader) Role() Role { return RoleLeader }
func (AlwaysLeader) Do(ctx context.Context, _ Operation, fn func(context.Context) error) error {
	return fn(ctx)
}

type preparationKey struct{}
type capability struct {
	gate  *Gate
	valid bool
}

// Gate has one irreversible epoch. All publication and termination decisions share mu.
type Gate struct {
	mu                                            sync.Mutex
	used, preparing, active, terminating, revoked bool
	epoch                                         context.Context
	cancel                                        context.CancelFunc
	token                                         *capability
	inflight                                      int
	changed                                       chan struct{}
}

func NewGate() *Gate { return &Gate{changed: make(chan struct{})} }
func (g *Gate) Role() Role {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active && !g.revoked && g.epoch.Err() == nil {
		return RoleLeader
	}
	return RoleStandby
}
func (g *Gate) notify() { close(g.changed); g.changed = make(chan struct{}) }
func (g *Gate) finish() { g.mu.Lock(); g.inflight--; g.notify(); g.mu.Unlock() }
func (g *Gate) Do(ctx context.Context, op Operation, fn func(context.Context) error) error {
	g.mu.Lock()
	token, hasToken := ctx.Value(preparationKey{}).(*capability)
	allowed := !g.revoked && (op == Mutation || op == Delivery)
	if hasToken {
		allowed = allowed && token == g.token && token.gate == g && token.valid && g.preparing && op == Mutation && !g.terminating
	} else {
		allowed = allowed && g.active && (op != Mutation || !g.terminating)
	}
	if !allowed || g.epoch == nil || g.epoch.Err() != nil {
		g.mu.Unlock()
		return ErrNotLeader
	}
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return err
	}
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.epoch, cancel)
	// Cancellation can race AfterFunc scheduling; never invoke on an already-dead epoch.
	if g.epoch.Err() != nil {
		stop()
		cancel()
		g.mu.Unlock()
		return ErrNotLeader
	}
	g.inflight++
	g.mu.Unlock()
	defer func() { stop(); cancel(); g.finish() }()
	return fn(child)
}
func (g *Gate) Activate(leadCtx context.Context, prepare func(context.Context) error) error {
	g.mu.Lock()
	if g.used || g.terminating || g.revoked || leadCtx.Err() != nil {
		g.mu.Unlock()
		return ErrNotLeader
	}
	g.used = true
	g.preparing = true
	g.epoch, g.cancel = context.WithCancel(leadCtx)
	token := &capability{gate: g, valid: true}
	g.token = token
	g.inflight++
	prepCtx, prepCancel := context.WithCancel(context.WithValue(g.epoch, preparationKey{}, token))
	g.mu.Unlock()
	err := prepare(prepCtx)
	g.mu.Lock()
	token.valid = false
	prepCancel()
	g.inflight--
	g.notify()
	if err != nil || !g.preparing || g.terminating || g.revoked || leadCtx.Err() != nil || g.inflight != 0 {
		g.revokeLocked()
		g.mu.Unlock()
		if err != nil {
			return err
		}
		return ErrNotLeader
	}
	g.preparing = false
	g.active = true
	g.mu.Unlock()
	return nil
}
func (g *Gate) Quiesce() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.terminating = true
	if g.preparing {
		g.revokeLocked()
	}
}
func (g *Gate) revokeLocked() {
	g.revoked = true
	g.active = false
	g.preparing = false
	if g.token != nil {
		g.token.valid = false
	}
	if g.cancel != nil {
		g.cancel()
	}
	g.notify()
}
func (g *Gate) Revoke() { g.mu.Lock(); g.revokeLocked(); g.mu.Unlock() }
func (g *Gate) Wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		n, ch := g.inflight, g.changed
		g.mu.Unlock()
		if n == 0 {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
