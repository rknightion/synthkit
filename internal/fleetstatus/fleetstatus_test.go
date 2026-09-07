// SPDX-License-Identifier: AGPL-3.0-only

package fleetstatus

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/operationalerr"
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
	obs(ctx, fleethook.Event{Collector: "b", Op: fleethook.OpHeartbeat, ErrorCode: operationalerr.CodeRejected})

	g := s.Snapshot()
	if g.Registered != 2 {
		t.Fatalf("Registered = %d, want 2", g.Registered)
	}
	if g.Heartbeats != 2 {
		t.Fatalf("Heartbeats = %d, want 2", g.Heartbeats)
	}
	if g.HeartbeatHealthy != 1 || g.LastHeartbeatOKMs != 2000 {
		t.Fatalf("heartbeat health = %+v", g)
	}
	if g.Failures != 1 {
		t.Fatalf("Failures = %d, want 1", g.Failures)
	}
	if g.LastOKMs != 2000 {
		t.Fatalf("LastOKMs = %d, want 2000", g.LastOKMs)
	}
	if g.LastErrorMs != 3000 || g.LastError != "request rejected" || g.LastErrorCode != operationalerr.CodeRejected {
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
	obs(context.Background(), fleethook.Event{Collector: "a", Op: fleethook.OpRegister, ErrorCode: operationalerr.CodeAuthentication})
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

func TestInvalidErrorCodeNormalizesWithoutLeaking(t *testing.T) {
	s := NewStore()
	s.Observer()(context.Background(), fleethook.Event{Collector: "external-id", Op: fleethook.OpRegister, ErrorCode: operationalerr.Code("Bearer raw-secret")})
	encoded, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "raw-secret") || strings.Contains(string(encoded), "external-id") {
		t.Fatalf("status leaked canary: %s", encoded)
	}
	if got := s.Snapshot(); got.LastErrorCode != operationalerr.CodeInternal || got.LastError != "internal error" {
		t.Fatalf("status = %+v", got)
	}
}

func TestReceiptObserverKeepsOnlyBoundedPerCollectorReceiptEvidence(t *testing.T) {
	s := NewStore()
	s.now = fixedNow(1000)
	obs := s.Observer()
	receipts := s.ReceiptObserver()
	ctx := context.Background()
	obs(ctx, fleethook.Event{Collector: "collector-a", Op: fleethook.OpRegister})

	content := "logging { level = \"info\" }"
	sum := sha256.Sum256([]byte(content))
	wantDigest := fmt.Sprintf("%x", sum)
	receipts(ctx, "collector-a", fleet.Receipt{State: fleet.ReceiptReceived, Digest: wantDigest})

	s.now = fixedNow(2000)
	receipts(ctx, "collector-a", fleet.Receipt{State: fleet.ReceiptStale})
	s.now = fixedNow(3000)
	receipts(ctx, "collector-a", fleet.Receipt{State: fleet.ReceiptReceived, Digest: "raw-secret"})
	if middle := s.Snapshot().Collectors[0]; middle.ReceiptState != fleet.ReceiptUnavailable || middle.ConfigDigest != wantDigest {
		t.Fatalf("malformed receipt = %+v, want unavailable with prior digest retained", middle)
	}
	s.now = fixedNow(4000)
	receipts(ctx, "collector-a", fleet.Receipt{State: fleet.ReceiptError})

	g := s.Snapshot()
	if len(g.Collectors) != 1 {
		t.Fatalf("collector statuses = %+v, want one", g.Collectors)
	}
	got := g.Collectors[0]
	if got.ID != "collector-a" || !got.Registered || got.ReceiptState != fleet.ReceiptError || got.ConfigDigest != wantDigest || got.LastConfigReceiptMs != 1000 || got.LastReceiptObservedMs != 4000 {
		t.Fatalf("collector receipt = %+v", got)
	}
	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), content) || strings.Contains(string(encoded), "raw-secret") {
		t.Fatalf("receipt status leaked unbounded input: %s", encoded)
	}
}
