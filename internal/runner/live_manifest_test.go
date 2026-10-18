// SPDX-License-Identifier: AGPL-3.0-only

package runner_test

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
)

// A live apply adds the applied blueprint's lanes to the manifest and a live remove takes them
// out again, each through the real control routes, with provenance following the running set.
func TestDeliveryManifestFollowsLiveApplyAndRemove(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{})
	empty := h.r.DeliveryManifest()
	if len(empty.Lanes) != 0 {
		t.Fatalf("empty running set intends lanes: %+v", empty.Lanes)
	}
	if code, body := h.apply(liveApplyBody("operator-7")); code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	applied := h.r.DeliveryManifest()
	states := map[string]runner.LaneState{}
	for _, l := range applied.Lanes {
		states[l.Sink] = l.State
	}
	for _, sink := range []string{"promrw", "loki", "otlp"} {
		if states[sink] != runner.LaneExpected {
			t.Fatalf("live-applied manifest lanes = %v, want %s expected", states, sink)
		}
	}
	if applied.Fingerprint == empty.Fingerprint {
		t.Fatal("live apply did not change manifest provenance")
	}
	if code, body := h.remove(liveID); code != http.StatusOK {
		t.Fatalf("remove = %d %s", code, body)
	}
	removed := h.r.DeliveryManifest()
	if len(removed.Lanes) != 0 || removed.Fingerprint != empty.Fingerprint {
		t.Fatalf("live remove left manifest %+v (fingerprint restored=%v)", removed.Lanes, removed.Fingerprint == empty.Fingerprint)
	}
}

// sigilDiscard accepts Sigil exports; the manifest test never ticks.
type sigilDiscard struct{}

func (sigilDiscard) Write(context.Context, []nativesigil.Export) error { return nil }

// agentManifestYAML declares one ai_agent at the realistic coding-agent rate grafana-ai-o11y uses
// (sessions_per_min 0.5, turns_p50 41, drawn from captures). The rate is irrelevant to the
// manifest; it is real so the fixture stays plausible.
const agentManifestYAML = `name: agent-manifest
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000004", region: eu-west-1, vpc_id: vpc-agent}
    cluster:
      type: eks
      name: agent-cluster
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
workloads:
  - type: ai_agent
    name: agent-svc
    runs_on: agent-cluster
    agents:
      - name: coder
        archetype: coding_claude_code
        sdk: sdk-go
        provider: anthropic
        models: [claude-sonnet-4-6]
        tools: [Read, Bash]
        capture_mode: full
        activity: { sessions_per_min: 0.5, turns_p50: 41, turns_p95: 282 }
`

// An ai_agent pushes Sigil only from ProjectBatch, which returns early on an empty batch, so its
// lane is request_driven; Tick-driven metric lanes stay scheduled.
func TestDeliveryManifestClassifiesRequestDrivenAgentLane(t *testing.T) {
	r := runner.New(runner.Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}, Traces: &coretest.TraceCapture{}, Sigil: sigilDiscard{}}, runner.Catalog(), runner.Options{})
	if err := r.AddBlueprint(loadBoot(t, agentManifestYAML, "")); err != nil {
		t.Fatal(err)
	}
	lanes := map[string]runner.ManifestLane{}
	for _, l := range r.DeliveryManifest().Lanes {
		lanes[l.Sink] = l
	}
	if l := lanes["sigil"]; l.State != runner.LaneExpected || l.Freshness != runner.FreshnessRequestDriven {
		t.Fatalf("aiagent-only sigil lane = %+v, want expected request_driven", l)
	}
	if l := lanes["promrw"]; l.Freshness != runner.FreshnessScheduled {
		t.Fatalf("metric lane = %+v, want scheduled", l)
	}
}

// The manifest is read concurrently with live apply, remove and control changes (run under -race).
func TestDeliveryManifestConcurrentWithLiveAndControl(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = h.r.DeliveryManifest()
				_ = h.r.DeliveryReadinessLanes()
			}
		}
	}()
	for i := 0; i < 5; i++ {
		if code, body := h.apply(liveApplyBody("operator-7")); code != http.StatusOK {
			t.Fatalf("apply = %d %s", code, body)
		}
		h.r.ApplyControl(control.State{VolumeMultiplier: 1, DisabledBlueprints: []string{"base"}})
		h.r.ApplyControl(control.State{VolumeMultiplier: 1})
		if code, body := h.remove(liveID); code != http.StatusOK {
			t.Fatalf("remove = %d %s", code, body)
		}
	}
	close(done)
	wg.Wait()
}
