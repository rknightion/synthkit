// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"bytes"
	"log"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/runner"
)

// mustLoad loads a blueprint YAML via blueprint.Load against runner.Catalog().
// Fails the test immediately if loading fails.
func mustLoad(t *testing.T, data string) *blueprint.Resolved {
	t.Helper()
	res, err := blueprint.Load([]byte(data), runner.Catalog())
	if err != nil {
		t.Fatalf("mustLoad: blueprint.Load failed: %v", err)
	}
	return res
}

// TestProjectCardinalityPositive: a minimal host blueprint must project > 0 distinct series.
func TestProjectCardinalityPositive(t *testing.T) {
	res := mustLoad(t, miniBlueprint)
	n, estimated := projectCardinality(runner.Catalog(), res)
	if n <= 0 {
		t.Fatalf("expected >0 projected series, got %d (estimated=%v)", n, estimated)
	}
	if estimated {
		t.Fatalf("expected exact projection (estimated=false), got estimated=true")
	}
}

// TestProjectCardinalityNoGoroutineLeak verifies that repeated projectCardinality calls do not
// accumulate goroutines. Without the deferred DrainQueues each call leaks ~24 sender goroutines
// (one per queue shard per signal type); with the fix the goroutine count must stay stable.
//
// Strategy: capture a baseline after one warm-up call + drain settle, then run N more calls,
// settle again, and assert the goroutine count has not grown beyond baseline+slack. A small
// slack (10) tolerates Go runtime background goroutine jitter without allowing true leaks
// (N=5 calls × ~24 goroutines/call = ~120 leaking goroutines without the fix).
func TestProjectCardinalityNoGoroutineLeak(t *testing.T) {
	res := mustLoad(t, miniBlueprint)
	reg := runner.Catalog()

	// Warm-up call: prime any one-time runtime allocations that are not leaks.
	_, _ = projectCardinality(reg, res)
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	baseline := runtime.NumGoroutine()

	const calls = 5
	for i := 0; i < calls; i++ {
		n, estimated := projectCardinality(reg, res)
		if n <= 0 {
			t.Fatalf("call %d: expected >0 series, got %d (estimated=%v)", i, n, estimated)
		}
	}

	// Allow sender goroutines to exit and GC to run.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	after := runtime.NumGoroutine()
	const slack = 10
	if after > baseline+slack {
		t.Fatalf("goroutine leak: baseline=%d after %d calls=%d (delta=%d, slack=%d)",
			baseline, calls, after, after-baseline, slack)
	}
	t.Logf("goroutines: baseline=%d after=%d delta=%d (slack=%d)", baseline, after, after-baseline, slack)
}

// TestProjectCardinalityQuiet verifies the throwaway dry runner's sinks are silenced — a Validate
// /save click must NOT spew "[dry-run …]" inventory lines into the live process log.
func TestProjectCardinalityQuiet(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	n, _ := projectCardinality(runner.Catalog(), mustLoad(t, miniBlueprint))
	if n <= 0 {
		t.Fatalf("expected >0 series, got %d", n)
	}
	if strings.Contains(buf.String(), "[dry-run") {
		t.Fatalf("projectCardinality leaked dry-run log lines:\n%s", buf.String())
	}
}

// TestValidateReportsCardinality: Manager.Validate must return OK=true, Cardinality>0, Estimated=false.
func TestValidateReportsCardinality(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Config: &fakeConfig{}})
	vr := m.Validate([]byte(miniBlueprint))
	if !vr.OK {
		t.Fatalf("expected OK=true, got diagnostics: %v", vr.Diagnostics)
	}
	if vr.Cardinality <= 0 {
		t.Fatalf("expected Cardinality>0, got %d", vr.Cardinality)
	}
	if vr.Estimated {
		t.Fatalf("expected Estimated=false, got true")
	}
}
