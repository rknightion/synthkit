// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// manifestRegistry declares lanes by signal class only; cadences are the constructs' own
// declarations, not rates. quiet_profiles is a slow lane whose sink the test leaves configured.
func manifestRegistry() *core.Registry {
	reg := core.NewRegistry()
	for _, k := range []struct {
		kind     string
		signals  []core.SignalClass
		interval time.Duration
	}{
		{"metrics_logs", []core.SignalClass{core.Metrics, core.Logs}, 0},
		{"traces_only", []core.SignalClass{core.Traces}, 0},
		{"quiet_profiles", []core.SignalClass{core.PyroscopeProfiles}, 5 * time.Minute},
	} {
		k := k
		reg.RegisterConstruct(core.ConstructReg{
			Kind: k.kind, Doc: "t", Scope: core.ScopeBlueprint,
			NewConfig:      func() any { return &struct{}{} },
			MetricProducer: fixedMetricProducer(producerPromRW),
			Build: func(any, *fixture.Set) (core.Construct, error) {
				return &fakeConstruct{kind: k.kind, signals: k.signals, interval: k.interval}, nil
			},
		})
	}
	return reg
}

func manifestBlueprint(name string, kinds ...string) *blueprint.Resolved {
	res := &blueprint.Resolved{Name: name, Label: name, Source: "name: " + name + "\n"}
	for _, kind := range kinds {
		res.Constructs = append(res.Constructs, blueprint.ConstructInstance{Kind: kind, Name: kind + "-1", Config: &struct{}{}, Fixtures: &fixture.Set{Seed: name}})
	}
	return res
}

func laneByName(m DeliveryManifest) map[string]ManifestLane {
	out := map[string]ManifestLane{}
	for _, l := range m.Lanes {
		if _, dup := out[l.Sink]; dup {
			panic("duplicate manifest lane " + l.Sink)
		}
		out[l.Sink] = l
	}
	return out
}

// A lane that never pushed, a lane with no sink and a slow lane all stay in the manifest with an
// explicit state; only expected lanes reach readiness.
func TestDeliveryManifestMissingUnsupportedAndQuietLanes(t *testing.T) {
	// No traces sink: the traces_only lane is declared but undeliverable here.
	r := New(Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}, Profiles: &captureProfileSink{}}, manifestRegistry(), Options{SendDeadline: 7 * time.Second})
	if err := r.AddBlueprint(manifestBlueprint("alpha", "metrics_logs", "traces_only", "quiet_profiles")); err != nil {
		t.Fatal(err)
	}
	m := r.DeliveryManifest() // derived before any tick: no push evidence exists yet
	got := laneByName(m)
	want := map[string]ManifestLane{
		"promrw":    {Sink: "promrw", State: LaneExpected, Freshness: FreshnessScheduled, EmissionInterval: time.Minute, FreshAfter: time.Minute + 7*time.Second},
		"loki":      {Sink: "loki", State: LaneExpected, Freshness: FreshnessScheduled, EmissionInterval: time.Minute, FreshAfter: time.Minute + 7*time.Second},
		"otlp":      {Sink: "otlp", State: LaneUnsupported, Freshness: FreshnessScheduled, EmissionInterval: time.Minute, FreshAfter: time.Minute + 7*time.Second},
		"pyroscope": {Sink: "pyroscope", State: LaneExpected, Freshness: FreshnessScheduled, EmissionInterval: 5 * time.Minute, FreshAfter: 5*time.Minute + 7*time.Second},
	}
	if len(got) != len(want) {
		t.Fatalf("manifest lanes = %+v, want %+v", m.Lanes, want)
	}
	for name, w := range want {
		if got[name] != w {
			t.Fatalf("lane %s = %+v, want %+v", name, got[name], w)
		}
	}
	// A 4-minute-old pyroscope push is inside its declared bound: quiet, not missing.
	if age := 4 * time.Minute; age >= got["pyroscope"].FreshAfter {
		t.Fatalf("quiet cadence bound %v does not admit a %v gap", got["pyroscope"].FreshAfter, age)
	}
	ready := map[string]bool{}
	for _, l := range r.DeliveryReadinessLanes() {
		ready[l.Name] = true
	}
	if ready["otlp"] || !ready["promrw"] || !ready["loki"] || !ready["pyroscope"] || len(ready) != 3 {
		t.Fatalf("readiness lanes = %v, want expected lanes only", ready)
	}
}

// Disabling emission keeps the lanes with state disabled, changes the fingerprint, and restoring
// the configuration restores both.
func TestDeliveryManifestDisabledEmissionAndConfigChange(t *testing.T) {
	r := New(Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}, Traces: &coretest.TraceCapture{}}, manifestRegistry(), Options{})
	if err := r.AddBlueprint(manifestBlueprint("alpha", "metrics_logs")); err != nil {
		t.Fatal(err)
	}
	if err := r.AddBlueprint(manifestBlueprint("beta", "traces_only")); err != nil {
		t.Fatal(err)
	}
	r.ApplyControl(control.State{VolumeMultiplier: 1})
	base := r.DeliveryManifest()
	if base.Fingerprint == "" || len(base.Fingerprint) != 64 {
		t.Fatalf("fingerprint %q is not a SHA-256 hex digest", base.Fingerprint)
	}
	if again := r.DeliveryManifest(); again.Fingerprint != base.Fingerprint {
		t.Fatal("fingerprint is not deterministic for one configuration")
	}

	r.ApplyControl(control.State{VolumeMultiplier: 1, DisabledBlueprints: []string{"beta"}})
	disabled := r.DeliveryManifest()
	if got := laneByName(disabled)["otlp"]; got.State != LaneDisabled || got.EmissionInterval != time.Minute {
		t.Fatalf("disabled blueprint lane = %+v, want explicit disabled lane", got)
	}
	if disabled.Fingerprint == base.Fingerprint {
		t.Fatal("disabling a blueprint did not change manifest provenance")
	}

	r.ApplyControl(control.State{VolumeMultiplier: 1, DisabledKinds: []string{"metrics_logs"}})
	kinds := laneByName(r.DeliveryManifest())
	if kinds["promrw"].State != LaneDisabled || kinds["loki"].State != LaneDisabled || kinds["otlp"].State != LaneExpected {
		t.Fatalf("kind-disabled manifest = %+v", kinds)
	}

	// The volume knob does not change intended delivery, so provenance holds.
	r.ApplyControl(control.State{VolumeMultiplier: 3})
	if got := r.DeliveryManifest(); got.Fingerprint != base.Fingerprint {
		t.Fatal("volume-only change moved manifest provenance")
	}

	if err := r.AddBlueprint(manifestBlueprint("gamma", "metrics_logs")); err != nil {
		t.Fatal(err)
	}
	if got := r.DeliveryManifest(); got.Fingerprint == base.Fingerprint {
		t.Fatal("adding a blueprint did not change manifest provenance")
	}

	sup := base.Suppress(LaneStandby)
	for _, l := range sup.Lanes {
		if l.State != LaneStandby {
			t.Fatalf("standby suppression left %+v", l)
		}
	}
	if len(sup.Lanes) != len(base.Lanes) || base.Lanes[0].State != LaneExpected {
		t.Fatal("Suppress dropped lanes or mutated its receiver")
	}
}

// A workload's rum declaration is a faro lane even when no Faro sink is configured.
func TestDeliveryManifestDeclaredRUMWithoutSink(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	res := buildTestResolved("alpha")
	res.Workloads[0].RUM = true
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	if got := laneByName(r.DeliveryManifest())["faro"]; got.State != LaneUnsupported {
		t.Fatalf("faro lane = %+v, want unsupported", got)
	}
}
