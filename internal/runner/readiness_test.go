// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
)

func TestDeliveryReadinessLanesFollowActiveBlueprints(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("active")); err != nil {
		t.Fatal(err)
	}
	if err := r.AddBlueprint(buildTestResolved("disabled")); err != nil {
		t.Fatal(err)
	}
	r.ApplyControl(control.State{VolumeMultiplier: 1, DisabledBlueprints: []string{"disabled"}})

	if got := r.ActiveBlueprintCount(); got != 1 {
		t.Fatalf("ActiveBlueprintCount = %d, want 1", got)
	}
	got := r.DeliveryReadinessLanes()
	want := []DeliveryReadinessLane{
		{Name: "loki", EmissionInterval: 60 * time.Second},
		{Name: "otlp", EmissionInterval: 60 * time.Second},
		{Name: "promrw", EmissionInterval: 60 * time.Second},
	}
	if len(got) != len(want) {
		t.Fatalf("lanes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lane[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
