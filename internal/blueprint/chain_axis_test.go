// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/runner"
)

// sharedNameBlueprint declares an app service node and a Lambda function with the same name,
// as a publisher service and its publishing function often are.
func sharedNameBlueprint(t *testing.T, effects string) string {
	t.Helper()
	text := strings.Replace(chainBlueprint, "  - name: prod\n", `  - name: prod
    cluster:
      type: baremetal
      name: shared-name-cluster
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
`, 1)
	text = strings.Replace(text, "integrations:\n", `workloads:
  - type: app
    name: fixture-app
    runs_on: shared-name-cluster
    services:
      - name: response-publisher
        type: web
integrations:
`, 1)
	parts := strings.SplitN(text, "    effects:\n", 2)
	if len(parts) != 2 {
		t.Fatal("chain blueprint has no effects block")
	}
	return parts[0] + "    effects:\n" + effects
}

// A name on both the service and function axes resolves each effect on the axis that declares
// its mode, so one scenario can drive the service node and the function of the same name.
func TestSharedNameResolvesOnTheModesAxis(t *testing.T) {
	text := sharedNameBlueprint(t, `      - {mode: function_error_spike, target: response-publisher, intensity: 1}
      - {mode: latency_storm, target: response-publisher, intensity: 0.5}
`)
	r, err := blueprint.Load([]byte(text+chainSchedule), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	var axes []string
	for _, tg := range r.Targets {
		if tg.Name == "response-publisher" {
			axes = append(axes, string(tg.Axis))
		}
	}
	if strings.Join(axes, ",") != "service,function" {
		t.Fatalf("response-publisher targets: %v, want service and function", axes)
	}
	joined := strings.Join(r.Incidents, "\n")
	for _, want := range []string{"function_error_spike@2026-10-24T09:10/10m#1@response-publisher", "latency_storm@2026-10-24T09:10/10m#0.5@response-publisher"} {
		if !strings.Contains(joined, want) {
			t.Errorf("incident %q not compiled; have:\n%s", want, joined)
		}
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "response-publisher") {
			t.Errorf("a resolvable shared name is still warned: %s", w)
		}
	}
	// The function effect really moves the function's telemetry.
	base, inc := renderChain(t, r, false), renderChain(t, r, true)
	const pub = "dimension_FunctionName=response-publisher"
	if e, n := sum(inc[15], "aws_lambda_errors_sum", pub), sum(inc[15], "aws_lambda_invocations_sum", pub); n == 0 || e != n {
		t.Fatalf("function_error_spike on the shared name: errors %v invocations %v", e, n)
	}
	if sum(base[15], "aws_lambda_errors_sum", pub) >= sum(base[15], "aws_lambda_invocations_sum", pub) {
		t.Fatal("baseline publisher should not be failing")
	}
}

// A mode no axis of the name declares is still a loud error naming every axis.
func TestSharedNameModeOnNoAxisIsLoud(t *testing.T) {
	text := sharedNameBlueprint(t, "      - {mode: queue_publish_drop, target: response-publisher, intensity: 1}\n")
	_, err := blueprint.Load([]byte(text), runner.Catalog())
	if err == nil || !strings.Contains(err.Error(), "not declared on") || !strings.Contains(err.Error(), "function") || !strings.Contains(err.Error(), "service") {
		t.Fatalf("got %v", err)
	}
}
