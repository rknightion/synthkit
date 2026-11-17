// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
)

// liveKindBlueprintBase declares one dependency, one pool held across it, one concurrency and an
// environment-aliased dependency pair (store-prod is also reached by the base name "store"). The
// queue, quota, credential and rule kinds are deliberately absent so "resource:*" for those modes
// has nothing to select. The resources carry no demand or caller edge: this fixture exercises
// incident validation and wildcard expansion, not the causal model's emission.
const liveKindBlueprintBase = `
name: live-kind
label: live-kind
environments:
  - name: prod
    cluster:
      type: baremetal
      name: live-kind-cluster
      emit: false
      nodes:
        - {hostname: fixture-node, ip: 192.0.2.1, cpus: 4, memory_gib: 16, arch: x86_64}
      platform:
        os_image: Ubuntu 24.04 LTS
        os_id: ubuntu
        container_runtime: containerd://2.1.7
        kubelet_version: v1.34.1
        kubernetes_version: v1.34.1
        kernel_version: 6.8.0
    resources:
      - { name: records-dep, kind: dependency, latency_ms: 15, slow_factor: 12 }
      - name: orders-pool
        kind: pool
        size: 10
        hold_ms: 20
        held_across: records-dep
        acquire_timeout_ms: 3000
        demand: { off_peak_rps: 5, peak_rps: 60 }
      - { name: store-prod, kind: dependency, latency_ms: 4 }
      - name: enrich-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        demand: { off_peak_rps: 2, peak_rps: 15 }
`

// liveKindBlueprint adds a scenario that fans pool_exhausted over "resource:*".
const liveKindBlueprint = liveKindBlueprintBase + `
scenarios:
  - name: squeeze-pools
    effects: [{ mode: pool_exhausted, target: "resource:*" }]
`

func newLiveKindRunner(t *testing.T) *Runner {
	t.Helper()
	reg := Catalog()
	bp, err := blueprint.Load([]byte(liveKindBlueprint), reg)
	if err != nil {
		t.Fatal(err)
	}
	r := New(Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}}, reg, Options{})
	if err := r.AddBlueprint(bp); err != nil {
		t.Fatal(err)
	}
	return r
}

func liveKindIncident(mode, target string) control.RuntimeIncident {
	return control.RuntimeIncident{Blueprint: "live-kind", Mode: mode, Target: target, At: "2026-10-24T09:10", For: "10m", Intensity: 1}
}

// A live control-plane incident is held to the loader's resource-kind rule, with the loader's own
// error text: a mode aimed at a resource of another kind, or at "resource:*" when none of its
// kind is declared, is refused through the HTTP surface and by the validation entry point alike.
func TestLiveIncidentRejectsWrongKindResource(t *testing.T) {
	r := newLiveKindRunner(t)
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	h := control.NewHandler(store, r.ApplyControl, "", r)

	cases := []struct {
		name         string
		mode, target string
		wantErr      string // empty: accepted
	}{
		{"dependency mode on a pool", "dependency_slow", "orders-pool",
			`target "orders-pool" resolves to resource "orders-pool" of kind "pool"; the mode acts only on "dependency" resources (accepted kind: dependency)`},
		{"pool mode on a dependency", "pool_exhausted", "records-dep",
			`target "records-dep" resolves to resource "records-dep" of kind "dependency"; the mode acts only on "pool" resources (accepted kind: pool)`},
		{"pool mode on a base alias of dependencies", "pool_exhausted", "store",
			`target "store" resolves to resource "store-prod" of kind "dependency"; the mode acts only on "pool" resources (accepted kind: pool)`},
		{"wildcard with no quota declared", "quota_reduced", "resource:*",
			`target "resource:*" matches no quota resource; the mode acts only on quota resources (accepted kind: quota)`},
		{"wildcard with no rule declared", "rule_regression", "resource:*",
			`target "resource:*" matches no rule resource; the mode acts only on rule resources (accepted kind: rule)`},
		// Valid incidents are unchanged.
		{"dependency mode on a dependency", "dependency_slow", "records-dep", ""},
		{"dependency mode on a base alias", "dependency_slow", "store", ""},
		{"pool mode on a pool", "pool_exhausted", "orders-pool", ""},
		{"concurrency mode on a concurrency", "concurrency_reduced", "enrich-concurrency", ""},
		{"dependency mode on a wildcard with dependencies", "dependency_slow", "resource:*", ""},
		{"pool mode on a wildcard with a pool", "pool_exhausted", "resource:*", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := r.ValidateRuntimeIncident(liveKindIncident(c.mode, c.target))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, c.wantErr)
			}
			// The same text the loader gives the same declared incident.
			decl := liveKindBlueprintBase + "incidents:\n  - { kind: " + c.mode + ", target: \"" + c.target + "\", at: \"10:00\", for: 10m }\n"
			_, loadErr := blueprint.Load([]byte(decl), Catalog())
			if loadErr == nil || !strings.HasSuffix(loadErr.Error(), err.Error()) {
				t.Fatalf("load error %v does not end with the live error %q", loadErr, err)
			}
		})
	}

	// The control surface: a wrong-kind incident is a 400 and is not stored.
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/control/incidents", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := post(`{"blueprint":"live-kind","mode":"dependency_slow","target":"orders-pool","at":"2026-10-24T09:10","for":"10m","intensity":1}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `of kind "pool"`) {
		t.Fatalf("wrong-kind POST = %d %q, want 400 naming the pool kind", rec.Code, rec.Body.String())
	}
	if n := len(store.Snapshot().RuntimeIncidents); n != 0 {
		t.Fatalf("a rejected incident was stored: %d runtime incidents", n)
	}
	rec = post(`{"blueprint":"live-kind","mode":"dependency_slow","target":"records-dep","at":"2026-10-24T09:10","for":"10m","intensity":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid POST = %d %q, want 200", rec.Code, rec.Body.String())
	}
	if n := len(store.Snapshot().RuntimeIncidents); n != 1 {
		t.Fatalf("valid incident stored %d times, want 1", n)
	}
}

// A live "resource:*" (an ad-hoc failure or an active scenario effect) selects the declared
// resources of the mode's kind alone, as the loader's scheduled expansion does: no other kind
// and no environment base alias.
func TestLiveResourceWildcardSelectsModeKind(t *testing.T) {
	r := newLiveKindRunner(t)
	scopes := func(mode string) []string {
		var out []string
		for _, lf := range r.bps[0].eng.Live(mode) {
			out = append(out, lf.Scope)
		}
		slices.Sort(out)
		return out
	}

	r.ApplyControl(control.State{
		VolumeMultiplier: 1,
		Failures:         map[string]control.FailureSetting{"dependency_slow": {Enabled: true, Intensity: 0.5, Scope: "resource:*"}},
		ActiveScenarios:  []string{"live-kind/squeeze-pools"},
	})
	if got, want := scopes("dependency_slow"), []string{"records-dep", "store-prod"}; !slices.Equal(got, want) {
		t.Errorf("dependency_slow on resource:* live scopes = %v, want %v", got, want)
	}
	if got, want := scopes("pool_exhausted"), []string{"orders-pool"}; !slices.Equal(got, want) {
		t.Errorf("pool_exhausted scenario effect on resource:* live scopes = %v, want %v", got, want)
	}

	// A named target and a mode outside the resource vocabulary are untouched.
	r.ApplyControl(control.State{
		VolumeMultiplier: 1,
		Failures: map[string]control.FailureSetting{
			"dependency_slow":     {Enabled: true, Intensity: 1, Scope: "store"},
			"concurrency_reduced": {Enabled: true, Intensity: 1, Scope: "resource:*"},
		},
	})
	if got, want := scopes("dependency_slow"), []string{"store"}; !slices.Equal(got, want) {
		t.Errorf("a named target live scopes = %v, want %v", got, want)
	}
	if got, want := scopes("concurrency_reduced"), []string{"enrich-concurrency"}; !slices.Equal(got, want) {
		t.Errorf("concurrency_reduced on resource:* live scopes = %v, want %v", got, want)
	}
}
