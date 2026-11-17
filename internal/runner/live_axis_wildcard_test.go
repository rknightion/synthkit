// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
)

// liveAxisBlueprint declares two targets on each of the service, function, queue and resource
// axes (two dependencies and one pool), so an "<axis>:*" runtime incident has more than one
// target to reach and a neighbour on another axis or kind to leave alone. The rates are modest
// steady streams: this fixture exercises incident expansion, not emission volume.
const liveAxisBlueprint = `
name: live-axis
label: live-axis
environments:
  - name: prod
    cluster:
      type: baremetal
      name: live-axis-cluster
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
    cloud:
      provider: aws
      account_id: "111122223333"
      region: us-east-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      lambda:
        functions:
          - {name: fn-a, invocations_per_min: 30, duration_ms: 120, error_fraction: 0.01}
          - {name: fn-b, invocations_per_min: 30, duration_ms: 120, error_fraction: 0.01}
    resources:
      - { name: dep-a, kind: dependency, latency_ms: 15, slow_factor: 12 }
      - { name: dep-b, kind: dependency, latency_ms: 4 }
      - name: pool-a
        kind: pool
        size: 10
        hold_ms: 20
        held_across: dep-a
        acquire_timeout_ms: 3000
        demand: { off_peak_rps: 5, peak_rps: 60 }
workloads:
  - type: app
    name: fixture-app
    runs_on: live-axis-cluster
    services:
      - name: svc-a
        type: web
        entry: true
      - name: svc-b
        type: web
integrations:
  solace:
    brokers:
      - name: broker-a
        vpns:
          - name: vpn-1
            queues:
              - name: q-a
              - name: q-b
`

// A live "<axis>:*" runtime incident reaches every target on that axis through the shape engine's
// exact-scope match, as a scheduled "<axis>:*" incident does after load: "resource:*" selects the
// resources of the mode's kind alone, and a target on another axis is never reached. An
// exact-target incident is unchanged, and deleting the incident clears every expanded scope.
func TestLiveRuntimeAxisWildcardMovesEveryTarget(t *testing.T) {
	reg := Catalog()
	bp, err := blueprint.Load([]byte(liveAxisBlueprint), reg)
	if err != nil {
		t.Fatal(err)
	}
	r := New(Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}}, reg, Options{})
	if err := r.AddBlueprint(bp); err != nil {
		t.Fatal(err)
	}
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	h := control.NewHandler(store, r.ApplyControl, "", r)
	rt := r.bps[0]
	now := time.Date(2026, 10, 24, 9, 15, 0, 0, rt.eng.Loc())

	post := func(t *testing.T, mode, target string, intensity float64) string {
		t.Helper()
		body := `{"blueprint":"live-axis","mode":"` + mode + `","target":"` + target + `","at":"2026-10-24T09:10","for":"10m","intensity":` +
			strconv.FormatFloat(intensity, 'g', -1, 64) + `}`
		req := httptest.NewRequest(http.MethodPost, "/control/incidents", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s on %s = %d %q, want 200", mode, target, rec.Code, rec.Body.String())
		}
		var st control.State
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		return st.RuntimeIncidents[len(st.RuntimeIncidents)-1].ID
	}
	del := func(t *testing.T, id string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, "/control/incidents/"+id, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE %s = %d %q, want 200", id, rec.Code, rec.Body.String())
		}
	}
	// eval reads the mode through the real engine for every scope, after the per-tick control
	// capture the runner takes at the start of a tick.
	eval := func(at time.Time, mode string, scopes ...string) map[string]float64 {
		r.prepareGPUTick(rt, at)
		out := map[string]float64{}
		for _, s := range scopes {
			if active, inten := rt.eng.Eval(at, mode, s); active {
				out[s] = inten
			}
		}
		return out
	}
	all := []string{"svc-a", "svc-b", "fn-a", "fn-b", "q-a", "q-b", "dep-a", "dep-b", "pool-a"}

	cases := []struct {
		name         string
		mode, target string
		want         []string // scopes that move; every other scope in all stays still
	}{
		{"service wildcard", "error_spike", "service:*", []string{"svc-a", "svc-b"}},
		{"function wildcard", "function_error_spike", "function:*", []string{"fn-a", "fn-b"}},
		{"queue wildcard", "queue_publish_drop", "queue:*", []string{"q-a", "q-b"}},
		{"resource wildcard of the dependency kind", "dependency_slow", "resource:*", []string{"dep-a", "dep-b"}},
		{"resource wildcard of the pool kind", "pool_exhausted", "resource:*", []string{"pool-a"}},
		// Unchanged: an exact target moves that target alone.
		{"exact function", "function_error_spike", "fn-a", []string{"fn-a"}},
		{"exact resource", "dependency_slow", "dep-b", []string{"dep-b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := post(t, c.mode, c.target, 0.5)
			got := eval(now, c.mode, all...)
			if len(got) != len(c.want) {
				t.Errorf("%s on %s moved %v, want exactly %v", c.mode, c.target, got, c.want)
			}
			for _, s := range c.want {
				if got[s] != 0.5 {
					t.Errorf("%s on %s: scope %q intensity = %v, want 0.5 (moved: %v)", c.mode, c.target, s, got[s], got)
				}
			}
			// Outside its window nothing moves: the expansion keeps the incident's own window.
			if got := eval(now.Add(30*time.Minute), c.mode, all...); len(got) != 0 {
				t.Errorf("%s on %s moved %v after its window closed, want nothing", c.mode, c.target, got)
			}
			// Deleting the incident clears every expanded scope.
			del(t, id)
			if got := eval(now, c.mode, all...); len(got) != 0 {
				t.Errorf("%s on %s still moved %v after the incident was deleted, want nothing", c.mode, c.target, got)
			}
		})
	}
}
