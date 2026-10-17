// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"testing"

	"github.com/rknightion/synthkit/internal/control"
)

const liveWarnYAML = `name: warn-svc
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000003", region: eu-west-1, vpc_id: vpc-warn}
    cluster:
      type: eks
      name: warn-cluster
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
workloads:
  - type: web_service
    name: warn-api
    runs_on: warn-cluster
`

// A removed live blueprint's shape warnings leave with it, so repeated apply and remove cannot
// grow the list, while another blueprint's warnings are kept.
func TestLiveRemoveDropsShapeWarnings(t *testing.T) {
	r := New(Sinks{}, Catalog(), Options{})
	r.shapeWarnings = []ShapeWarning{{Blueprint: "base", Message: "kept"}}
	lb := control.LiveBlueprint{Name: "synthetic/warn-svc", Namespace: "synthetic", YAML: liveWarnYAML}
	for i := 0; i < 3; i++ {
		if err := r.ApplyLiveBlueprint(lb, nil); err != nil {
			t.Fatal(err)
		}
		r.shapeMu.Lock()
		r.shapeWarnings = append(r.shapeWarnings, ShapeWarning{Blueprint: lb.Name, Message: "skipped entry"})
		r.shapeMu.Unlock()
		if err := r.RemoveLiveBlueprint(lb.Name, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := r.ShapeWarnings()
	if len(got) != 1 || got[0].Blueprint != "base" {
		t.Fatalf("shape warnings after apply/remove cycles = %+v, want only the base entry", got)
	}
}
