// SPDX-License-Identifier: AGPL-3.0-only

package runner_test

// live_compose_test.go checks the live apply and remove routes against the startup kind disable
// and the opt-in readiness probe flags: both read the running set, so a live-applied blueprint
// must appear in them. Fixtures are synthetic shapes only.

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/runner"
)

// fleetOnlyYAML declares only a fleet_management feature; its AWS environment adds the cw_infra
// construct. Disabling those two kinds leaves the blueprint nothing to emit.
const fleetOnlyYAML = `name: fleet-only
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000004", region: eu-west-1, vpc_id: vpc-fleet}
features:
  fleet_management:
    enabled: true
    collectors_per_os: {linux: 2}
`

const fleetOnlyID = "synthetic/fleet-only"

// fleetOnlyKinds are every construct kind fleetOnlyYAML resolves to.
var fleetOnlyKinds = []string{fleetmgmt.Kind, "cw_infra"}

// startupOnlyKind is a registered kind no blueprint here declares: only the startup seed can make
// it valid for POST /control/kinds.
const startupOnlyKind = "rds"

// newComposeHarness boots like the composition root: boot set, startup kinds seeded into the
// store, the first ApplyControl, then a handler with the readiness flags opted in.
func newComposeHarness(t *testing.T, startupKinds []string) *liveHarness {
	t.Helper()
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
	if _, err := h.store.SeedDisabledKinds(context.Background(), startupKinds); err != nil {
		t.Fatal(err)
	}
	h.r.ApplyControl(h.store.Snapshot())
	serving := control.ReadinessReport{Running: true, HTTPReady: true, ReasonCodes: []control.ReadinessReasonCode{}}
	h.handler = control.NewHandler(h.store, h.r.ApplyControl, liveToken, h.r).
		SetBlueprintAdmin(liveAdmin{}).SetInventory(h.r).
		SetStatus(control.StatusSources{Readiness: func() control.ReadinessReport { return serving }}).
		SetReadinessBlueprintFlags(h.r)
	return h
}

func (h *liveHarness) emittingFlags() map[string]bool {
	h.t.Helper()
	_, body := h.do(http.MethodGet, "/control/readiness", nil, false)
	var probe struct {
		Flags map[string]bool `json:"blueprint_emitting"`
	}
	if err := json.Unmarshal([]byte(body), &probe); err != nil || probe.Flags == nil {
		h.t.Fatalf("readiness probe without blueprint_emitting: %v %s", err, body)
	}
	return probe.Flags
}

func (h *liveHarness) schema() control.Schema {
	h.t.Helper()
	code, body := h.do(http.MethodGet, "/control/schema", nil, true)
	if code != http.StatusOK {
		h.t.Fatalf("GET /control/schema = %d %s", code, body)
	}
	var sc control.Schema
	if err := json.Unmarshal([]byte(body), &sc); err != nil {
		h.t.Fatal(err)
	}
	return sc
}

func hasConstruct(sc control.Schema, blueprint, kind string) bool {
	return slices.ContainsFunc(sc.Constructs, func(c control.ConstructInfo) bool {
		return c.Blueprint == blueprint && c.Kind == kind
	})
}

// The readiness flags, the startup-kinds schema wrapper and the kind gate all follow the running
// set: live-applied blueprints join them on apply and leave them on removal.
func TestLiveApplyComposesWithStartupKindsAndReadinessFlags(t *testing.T) {
	h := newComposeHarness(t, append([]string{startupOnlyKind}, fleetOnlyKinds...))

	if flags := h.emittingFlags(); len(flags) != 1 || !flags["base"] {
		t.Fatalf("boot readiness flags = %v, want only base emitting", flags)
	}

	for _, b := range []liveBody{
		liveApplyBody("operator-7"),
		{Namespace: "Synthetic", Name: "fleet-only", YAML: fleetOnlyYAML},
	} {
		if code, body := h.apply(b); code != http.StatusOK {
			t.Fatalf("apply %s = %d %s", b.Name, code, body)
		}
	}

	// The readiness probe lists live-applied blueprints. One whose only kind is startup-disabled
	// is loaded but not emitting.
	flags := h.emittingFlags()
	if want := map[string]bool{"base": true, liveID: true, fleetOnlyID: false}; !mapsEqual(flags, want) {
		t.Fatalf("readiness flags after live apply = %v, want %v", flags, want)
	}

	// The schema behind the startup-kinds wrapper carries the live blueprints' constructs and
	// still adds the startup kinds.
	sc := h.schema()
	if !hasConstruct(sc, liveID, "k8s_cluster") || !hasConstruct(sc, fleetOnlyID, fleetmgmt.Kind) {
		t.Fatalf("schema constructs omit the live blueprints: %+v", sc.Constructs)
	}
	for _, k := range []string{fleetmgmt.Kind, startupOnlyKind, "k8s_cluster"} {
		if !slices.Contains(sc.Kinds, k) {
			t.Fatalf("schema kinds %v missing %s", sc.Kinds, k)
		}
	}
	keep := map[string]any{"disabled_kinds": append([]string{startupOnlyKind}, fleetOnlyKinds...)}
	if code, body := h.do(http.MethodPost, "/control/kinds", keep, true); code != http.StatusOK {
		t.Fatalf("re-posting the startup kinds after live apply = %d %s", code, body)
	}

	// The fleet-only blueprint emits nothing while its kinds are disabled; the other live one does.
	got := h.tick(tickBase)
	if n := countSeries(got, "job", "integrations/alloy") + countSeries(got, "blueprint", fleetOnlyID); n != 0 {
		t.Fatalf("startup-disabled kinds emitted %d series after live apply", n)
	}
	if countSeries(got, "blueprint", liveID) == 0 || countSeries(got, "blueprint", "base") == 0 {
		t.Fatal("enabled blueprints stopped emitting")
	}
	if h.r.FleetRosterCount() != 2 {
		t.Fatalf("fleet roster = %d, want the live blueprint's 2 collectors", h.r.FleetRosterCount())
	}

	// Removal takes both out of the probe, the schema and the roster.
	for _, name := range []string{liveID, fleetOnlyID} {
		if code, body := h.remove(name); code != http.StatusOK {
			t.Fatalf("remove %s = %d %s", name, code, body)
		}
	}
	if flags := h.emittingFlags(); len(flags) != 1 || !flags["base"] {
		t.Fatalf("readiness flags after removal = %v, want only base", flags)
	}
	if sc := h.schema(); hasConstruct(sc, liveID, "k8s_cluster") || hasConstruct(sc, fleetOnlyID, fleetmgmt.Kind) ||
		!slices.Contains(sc.Kinds, startupOnlyKind) || !slices.Contains(sc.Kinds, fleetmgmt.Kind) {
		t.Fatalf("schema after removal = kinds %v constructs %+v", sc.Kinds, sc.Constructs)
	}
	if h.r.FleetRosterCount() != 0 {
		t.Fatalf("fleet roster after removal = %d, want 0", h.r.FleetRosterCount())
	}
}

// Enabling the kinds again lets the live fleet-only blueprint emit, so the zero above is the kind
// gate and not a blueprint that never emits.
func TestLiveStartupDisabledKindEmitsWhenEnabled(t *testing.T) {
	h := newComposeHarness(t, fleetOnlyKinds)
	if code, body := h.apply(liveBody{Namespace: "Synthetic", Name: "fleet-only", YAML: fleetOnlyYAML}); code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	got := h.tick(tickBase)
	if n := countSeries(got, "job", "integrations/alloy") + countSeries(got, "blueprint", fleetOnlyID); n != 0 {
		t.Fatalf("disabled kinds emitted %d series", n)
	}
	if code, body := h.do(http.MethodPost, "/control/kinds", map[string]any{"disabled_kinds": []string{}}, true); code != http.StatusOK {
		t.Fatalf("enable kinds = %d %s", code, body)
	}
	if countSeries(h.tick(tickBase.Add(time.Minute)), "job", "integrations/alloy") == 0 {
		t.Fatal("fleet-only live blueprint did not emit once its kind was enabled")
	}
	if flags := h.emittingFlags(); !flags[fleetOnlyID] {
		t.Fatalf("readiness flags = %v, want %s emitting once enabled", flags, fleetOnlyID)
	}
}

func mapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if got, ok := a[k]; !ok || got != v {
			return false
		}
	}
	return true
}
