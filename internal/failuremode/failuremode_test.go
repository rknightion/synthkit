// SPDX-License-Identifier: AGPL-3.0-only

package failuremode

import "testing"

func TestMultiAxis(t *testing.T) {
	modes := []Mode{
		{Name: "latency_spike", Axis: AxisWorkload},
		{Name: "latency_spike", Axis: AxisDatabase},
		{Name: "oom_kill", Axis: AxisCluster},
	}
	got := MultiAxis(modes)
	if !got["latency_spike"] {
		t.Errorf("latency_spike should be multi-axis")
	}
	if got["oom_kill"] {
		t.Errorf("oom_kill is single-axis, must not be flagged")
	}
}
