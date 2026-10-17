// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"testing"

	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/control"
)

const liveFleetYAML = `name: fleet-svc
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000005", region: eu-west-1, vpc_id: vpc-fleet}
features:
  fleet_management:
    enabled: true
    collectors_per_os: {linux: 2}
`

// The Fleet controller's provider, built once at start, reads the running set on every call: a
// live-applied blueprint's collectors join it and leave on removal, and a disabled
// fleet_management kind pauses the controller without changing what the provider reports.
func TestLiveFleetProviderFollowsRunningSet(t *testing.T) {
	r := New(Sinks{}, Catalog(), Options{})
	r.ApplyControl(control.State{DisabledKinds: []string{fleetmgmt.Kind}})
	if !r.fleetKindDisabled() {
		t.Fatal("fleet_management kind not reported disabled")
	}
	provider := r.selectedFleetProvider()
	if n := len(provider()); n != 0 {
		t.Fatalf("provider before apply = %d collectors, want 0", n)
	}
	lb := control.LiveBlueprint{Name: "synthetic/fleet-svc", Namespace: "synthetic", YAML: liveFleetYAML}
	if err := r.ApplyLiveBlueprint(lb, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(provider()); n != 2 {
		t.Fatalf("provider after live apply = %d collectors, want 2", n)
	}
	if !r.fleetKindDisabled() {
		t.Fatal("live apply cleared the disabled fleet_management kind")
	}
	if err := r.RemoveLiveBlueprint(lb.Name, false, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(provider()); n != 0 {
		t.Fatalf("provider after live removal = %d collectors, want 0", n)
	}
}
