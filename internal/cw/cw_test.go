// SPDX-License-Identifier: AGPL-3.0-only

package cw_test

import (
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/state"
)

// collect flattens a fresh state after one EmitStats call into name→value.
func collect(t *testing.T, base string, lbls map[string]string, s cw.StatSet) (map[string]float64, map[string]map[string]string) {
	t.Helper()
	st := state.NewState()
	cw.EmitStats(st, base, lbls, s)
	vals := map[string]float64{}
	labels := map[string]map[string]string{}
	for _, ser := range st.Collect(time.Unix(0, 0)) {
		vals[ser.Name] = ser.Value
		labels[ser.Name] = ser.Labels
	}
	return vals, labels
}

func TestStatSuffixesAreCanonical(t *testing.T) {
	want := []string{"_average", "_maximum", "_minimum", "_sample_count", "_sum"}
	got := append([]string(nil), cw.StatSuffixes...)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("StatSuffixes has %d entries, want %d: %v", len(got), len(want), cw.StatSuffixes)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("StatSuffixes mismatch: got sorted %v, want %v", got, want)
		}
	}
}

func TestEmitStatsProducesExactlyFiveSeries(t *testing.T) {
	vals, _ := collect(t, "aws_rds_cpuutilization", map[string]string{"x": "1"},
		cw.StatSet{Sum: 10, Average: 2, Maximum: 3, Minimum: 1, SampleCount: 60})
	if len(vals) != 5 {
		t.Fatalf("EmitStats wrote %d series, want exactly 5: %v", len(vals), vals)
	}
	cases := map[string]float64{
		"aws_rds_cpuutilization_sum":          10,
		"aws_rds_cpuutilization_average":      2,
		"aws_rds_cpuutilization_maximum":      3,
		"aws_rds_cpuutilization_minimum":      1,
		"aws_rds_cpuutilization_sample_count": 60,
	}
	for name, want := range cases {
		if got, ok := vals[name]; !ok {
			t.Errorf("missing series %q", name)
		} else if got != want {
			t.Errorf("%q = %v, want %v", name, got, want)
		}
	}
}

// TestEmitStatsSeriesAreGauges proves the I5 rule: a second EmitStats with new values
// OVERWRITES (Set semantics), never accumulates (Add would double the sum).
func TestEmitStatsSeriesAreGauges(t *testing.T) {
	st := state.NewState()
	lbls := map[string]string{"dimension_InstanceId": "i-abc"}
	cw.EmitStats(st, "aws_ec2_networkin", lbls, cw.StatSet{Sum: 100, Average: 5, Maximum: 6, Minimum: 4, SampleCount: 60})
	cw.EmitStats(st, "aws_ec2_networkin", lbls, cw.StatSet{Sum: 200, Average: 9, Maximum: 9, Minimum: 9, SampleCount: 60})
	for _, ser := range st.Collect(time.Unix(0, 0)) {
		switch ser.Name {
		case "aws_ec2_networkin_sum":
			if ser.Value != 200 {
				t.Errorf("_sum = %v after re-emit, want 200 (gauge Set, not 300 accumulate)", ser.Value)
			}
		case "aws_ec2_networkin_average":
			if ser.Value != 9 {
				t.Errorf("_average = %v after re-emit, want 9", ser.Value)
			}
		}
	}
}

// TestEmitStatsClonesLabelsPerSuffix proves I17 safety: mutating the caller's map after
// EmitStats does not leak into any emitted series, and the five series do not alias one map.
func TestEmitStatsClonesLabelsPerSuffix(t *testing.T) {
	st := state.NewState()
	lbls := map[string]string{"dimension_VolumeId": "vol-1"}
	cw.EmitStats(st, "aws_ebs_volume_read_bytes", lbls, cw.StatSet{Sum: 1, Average: 1, Maximum: 1, Minimum: 1, SampleCount: 1})
	// Mutate the caller's map after the call — must not affect emitted series.
	lbls["dimension_VolumeId"] = "MUTATED"
	lbls["injected"] = "bad"

	series := st.Collect(time.Unix(0, 0))
	maps := map[string]string{}
	for _, ser := range series {
		if ser.Labels["dimension_VolumeId"] != "vol-1" {
			t.Errorf("%s leaked post-call mutation: %v", ser.Name, ser.Labels)
		}
		if _, bad := ser.Labels["injected"]; bad {
			t.Errorf("%s leaked injected label: %v", ser.Name, ser.Labels)
		}
		maps[ser.Name] = ser.Labels["dimension_VolumeId"]
	}
	// Mutating one emitted series' map must not bleed into the others (no shared map).
	for _, ser := range series {
		ser.Labels["probe"] = ser.Name // poke each map independently
	}
	for _, ser := range series {
		if len(ser.Labels) == 0 {
			continue
		}
		// Each map should carry exactly its own poke, not all five.
		if ser.Labels["probe"] != ser.Name {
			t.Errorf("%s shares a label map with another series (probe=%q)", ser.Name, ser.Labels["probe"])
		}
	}
}
