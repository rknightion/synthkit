// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
)

// sharedNameRuntimeBlueprint declares an app service node and a Lambda function with the same
// name, as a publisher service and its publishing function often are, plus one Solace queue.
// Rates are modest steady streams, not tuned to force emission.
const sharedNameRuntimeBlueprint = `
name: shared-name
label: shared-name
environments:
  - name: prod
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
    cloud:
      provider: aws
      account_id: "111122223333"
      region: us-east-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      lambda:
        functions:
          - {name: response-publisher, invocations_per_min: 30, duration_ms: 120, error_fraction: 0.01}
workloads:
  - type: app
    name: fixture-app
    runs_on: shared-name-cluster
    services:
      - name: response-publisher
        type: web
integrations:
  solace:
    brokers:
      - name: broker-a
        vpns:
          - name: vpn-1
            queues:
              - name: responses
`

// ValidateRuntimeIncident resolves a target name shared by an app service node and a Lambda
// function on the axis that declares the mode, mirroring the loader: a function mode and a
// mode both axes declare are accepted, a queue mode no axis of the name declares is rejected.
func TestValidateRuntimeIncidentSharedNameResolvesOnModeAxis(t *testing.T) {
	reg := Catalog()
	bp, err := blueprint.Load([]byte(sharedNameRuntimeBlueprint), reg)
	if err != nil {
		t.Fatal(err)
	}
	var axes []string
	for _, tg := range bp.Targets {
		if tg.Name == "response-publisher" {
			axes = append(axes, string(tg.Axis))
		}
	}
	if strings.Join(axes, ",") != "service,function" {
		t.Fatalf("response-publisher targets: %v, want service and function", axes)
	}
	rn := New(Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}}, reg, Options{})
	if err := rn.AddBlueprint(bp); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		mode    string
		target  string
		wantErr string // empty: accepted
	}{
		{"function_error_spike", "response-publisher", ""},
		{"latency_storm", "response-publisher", ""},
		{"queue_publish_drop", "response-publisher", "not declared on any of target"},
		{"function_error_spike", "responses", "not declared on target"},
	} {
		err := rn.ValidateRuntimeIncident(control.RuntimeIncident{Blueprint: "shared-name", Mode: c.mode, Target: c.target, At: "2026-10-24T09:10", For: "10m", Intensity: 1})
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s on %s: rejected: %v", c.mode, c.target, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s on %s: got %v, want error containing %q", c.mode, c.target, err, c.wantErr)
		}
	}
}
