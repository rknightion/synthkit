// SPDX-License-Identifier: AGPL-3.0-only

package fleetstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/fleethook"
)

func fixedNow(ms int64) func() time.Time {
	return func() time.Time { return time.UnixMilli(ms) }
}

func TestRegisterHeartbeatUnregister(t *testing.T) {
	s := NewStore()
	s.now = fixedNow(1000)
	obs := s.Observer()
	ctx := context.Background()

	// Two collectors register OK.
	obs(ctx, fleethook.Event{Collector: "a", Op: fleethook.OpRegister})
	obs(ctx, fleethook.Event{Collector: "b", Op: fleethook.OpRegister})
	// Heartbeats: a OK, b fails.
	s.now = fixedNow(2000)
	obs(ctx, fleethook.Event{Collector: "a", Op: fleethook.OpHeartbeat})
	s.now = fixedNow(3000)
	obs(ctx, fleethook.Event{Collector: "b", Op: fleethook.OpHeartbeat, Err: errors.New("503")})

	g := s.Snapshot()
	if g.Registered != 2 {
		t.Fatalf("Registered = %d, want 2", g.Registered)
	}
	if g.Heartbeats != 2 {
		t.Fatalf("Heartbeats = %d, want 2", g.Heartbeats)
	}
	if g.Failures != 1 {
		t.Fatalf("Failures = %d, want 1", g.Failures)
	}
	if g.LastOKMs != 2000 {
		t.Fatalf("LastOKMs = %d, want 2000", g.LastOKMs)
	}
	if g.LastErrorMs != 3000 || g.LastError != "503" {
		t.Fatalf("last error wrong: ms=%d err=%q", g.LastErrorMs, g.LastError)
	}

	// b unregisters → registered drops to 1.
	obs(ctx, fleethook.Event{Collector: "b", Op: fleethook.OpUnregister})
	if g := s.Snapshot(); g.Registered != 1 {
		t.Fatalf("Registered after unregister = %d, want 1", g.Registered)
	}
}

func TestRegisterFailureNotCountedAsRegistered(t *testing.T) {
	s := NewStore()
	s.now = fixedNow(1000)
	obs := s.Observer()
	obs(context.Background(), fleethook.Event{Collector: "a", Op: fleethook.OpRegister, Err: errors.New("401")})
	g := s.Snapshot()
	if g.Registered != 0 {
		t.Fatalf("Registered = %d, want 0 (register failed)", g.Registered)
	}
	if g.Failures != 1 {
		t.Fatalf("Failures = %d, want 1", g.Failures)
	}
}

func TestDryRunFlag(t *testing.T) {
	s := NewStore()
	s.Observer()(context.Background(), fleethook.Event{Collector: "a", Op: fleethook.OpRegister, DryRun: true})
	if !s.Snapshot().DryRun {
		t.Fatal("DryRun flag not propagated")
	}
}
