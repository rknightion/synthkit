// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Disabling cluster-derived telemetry must not discard its declared GPU identity
// or suppress other environments and workloads. Monitoring enablement is separate.
func TestClusterEmitRetainsGPUDeclaration(t *testing.T) {
	data, err := os.ReadFile("../../e2e/fixtures/ai-factory-fixture.yaml")
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Replace(string(data), "type: baremetal", "type: baremetal\n      emit: false", 1)
	r, err := Load([]byte(input), testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Constructs) != 0 {
		t.Fatalf("disabled cluster constructed telemetry: %v", r.Constructs)
	}
	if r.GPU == nil || len(r.GPU.Nodes) != 20 {
		t.Fatal("canonical declared inventory lost")
	}
	count := 0
	for _, n := range r.GPU.Nodes {
		if n.Node == nil || n.KubernetesCluster != "compute-user" {
			t.Fatalf("binding lost: %s", n.Key)
		}
		count += len(n.GPUs)
	}
	if count != 80 {
		t.Fatalf("declared GPU inventory=%d", count)
	}
}

func TestClusterEmitKeepsOtherEnvironmentAndWorkloads(t *testing.T) {
	const input = `name: cluster-emission
shape: business_hours_plateau
environments:
  - name: first
    cloud: {provider: aws, account_id: "100000000001", region: eu-west-1, vpc_id: vpc-first}
    cluster:
      type: eks
      name: first-cluster
      %s
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
  - name: second
    cloud: {provider: aws, account_id: "100000000002", region: eu-west-1, vpc_id: vpc-second}
    cluster:
      type: eks
      name: second-cluster
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
workloads:
  - type: web_service
    name: service
    for_each_env: true
`
	for _, flag := range []string{"", "emit: true", "emit: false"} {
		t.Run(flag, func(t *testing.T) {
			r, err := Load([]byte(fmt.Sprintf(input, flag)), testRegistry(t))
			if err != nil {
				t.Fatal(err)
			}
			want := 4 // K8s + EC2 for each cluster, unchanged default.
			if flag == "emit: false" {
				want = 2
			}
			count := 0
			for _, ci := range r.Constructs {
				if ci.Kind == KindK8sCluster || ci.Kind == KindEC2 {
					count++
				}
				if flag == "emit: false" && (ci.Kind == KindK8sCluster || ci.Kind == KindEC2) && ci.Fixtures.Cluster != nil && ci.Fixtures.Cluster.Name == "first-cluster" {
					t.Fatalf("disabled cluster still constructs %s", ci.Kind)
				}
			}
			if count != want {
				t.Fatalf("cluster lanes=%d want%d", count, want)
			}
			if len(r.Workloads) != 2 {
				t.Fatalf("independent workloads lost: %d", len(r.Workloads))
			}
		})
	}
}
