// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
)

// rateOnlyBlueprint declares one service node, one Lambda function, a database, a bare-metal
// cluster and one causal resource, then a scenario per case.
const rateOnlyBlueprint = `name: rate-only-fixture
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: us-east-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      lambda:
        functions:
          - {name: fn, invocations_per_min: 30, duration_ms: 120, error_fraction: 0.01}
    cluster:
      type: baremetal
      name: fixture-cluster
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
    databases:
      - { name: records-db, engine: postgres }
    resources:
      - { name: store, kind: dependency, latency_ms: 4 }
      - name: fn-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        demand: { off_peak_rps: 2, peak_rps: 15 }
workloads:
  - type: app
    name: fixture-app
    runs_on: fixture-cluster
    services:
      - name: api
        type: web
scenarios:
%s`

func loadRateOnly(t *testing.T, scenarios, incidents string) *blueprint.Resolved {
	t.Helper()
	text := strings.Replace(rateOnlyBlueprint, "%s", scenarios, 1) + incidents
	r, err := blueprint.Load([]byte(text), causalRegistry())
	if err != nil {
		t.Fatalf("load: %v\n%s", err, text)
	}
	return r
}

func rateOnlyWarnings(r *blueprint.Resolved) []string {
	var out []string
	for _, w := range r.Warnings {
		if strings.Contains(w, "rate-only") {
			out = append(out, w)
		}
	}
	return out
}

// A scenario that only scales request-path rates is warned, once, naming its modes; a scenario
// with a resource-axis effect, or with only cluster, database or runtime-pressure effects, is not.
func TestScenarioRateOnlyWarning(t *testing.T) {
	r := loadRateOnly(t, `  - name: rate_only
    effects:
      - { mode: latency_storm, target: api, intensity: 0.8 }
      - { mode: function_error_spike, target: fn, intensity: 0.5 }
      - { mode: latency_storm, target: api, intensity: 0.4 }
  - name: causal
    effects:
      - { mode: dependency_slow, target: store, intensity: 0.8 }
      - { mode: latency_storm, target: api, intensity: 0.8 }
  - name: causal_wildcard
    effects:
      - { mode: concurrency_reduced, target: "resource:*", intensity: 0.5 }
      - { mode: function_error_spike, target: fn, intensity: 0.5 }
  - name: cluster_only
    effects:
      - { mode: pod_crashloop, target: fixture-cluster, intensity: 0.5 }
      - { mode: node_not_ready, target: fixture-cluster, intensity: 0.5 }
  - name: database_only
    effects:
      - { mode: connection_saturation, target: records-db, intensity: 0.5 }
  - name: runtime_pressure
    effects:
      - { mode: cpu_hotspot, target: api, intensity: 0.5 }
      - { mode: memory_leak, target: api, intensity: 0.5 }
  - name: cluster_and_rate
    effects:
      - { mode: pod_crashloop, target: fixture-cluster, intensity: 0.5 }
      - { mode: error_spike, target: "service:*", intensity: 0.5 }
`, "")
	got := rateOnlyWarnings(r)
	want := []string{
		`scenario "rate_only" is rate-only: latency_storm, function_error_spike scale request-path rates`,
		`scenario "cluster_and_rate" is rate-only: error_spike scale request-path rates`,
	}
	if len(got) != len(want) {
		t.Fatalf("rate-only warnings:\n%s\nwant %d", strings.Join(got, "\n"), len(want))
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Errorf("warning %d = %q, want it to contain %q", i, got[i], w)
		}
	}
}

// A single-mode incident row follows the same rule; a scenario referenced by an incident is
// warned once, on the scenario, not again on the schedule row.
func TestIncidentRateOnlyWarning(t *testing.T) {
	r := loadRateOnly(t, `  - name: rate_only
    effects:
      - { mode: latency_storm, target: api, intensity: 0.8 }
`, `incidents:
  - { kind: latency_storm, target: api, every: 20m, for: 2m, intensity: 0.12 }
  - { kind: dependency_slow, target: store, every: 20m, for: 2m, intensity: 0.12 }
  - { kind: pod_crashloop, target: fixture-cluster, at: "10:00", for: 5m }
  - { kind: connection_saturation, target: records-db, at: "10:00", for: 5m }
  - { scenario: rate_only, at: "12:00", for: 10m }
`)
	got := rateOnlyWarnings(r)
	if len(got) != 2 {
		t.Fatalf("rate-only warnings:\n%s\nwant the scenario and the one rate-only incident row", strings.Join(got, "\n"))
	}
	if !strings.Contains(got[0], `scenario "rate_only" is rate-only`) || !strings.Contains(got[1], `incident "latency_storm" on "api" is rate-only: latency_storm`) || !strings.Contains(got[1], "define a scenario that pairs it") {
		t.Errorf("unexpected warnings:\n%s", strings.Join(got, "\n"))
	}
}

// Warnings never fail a load: the blueprint still resolves and still compiles its schedule.
func TestRateOnlyWarningIsNotAnError(t *testing.T) {
	r := loadRateOnly(t, `  - name: rate_only
    effects:
      - { mode: latency_storm, target: api, intensity: 0.8 }
`, `incidents:
  - { scenario: rate_only, at: "12:00", for: 10m }
`)
	if len(rateOnlyWarnings(r)) != 1 || len(r.Incidents) == 0 || len(r.Scenarios) != 1 {
		t.Fatalf("warnings %v incidents %v scenarios %d", r.Warnings, r.Incidents, len(r.Scenarios))
	}
}
