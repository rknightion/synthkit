// SPDX-License-Identifier: AGPL-3.0-only

package cw

import (
	"reflect"
	"testing"
)

func TestMWAAOutcomeNativeContracts(t *testing.T) {
	for _, tt := range []struct {
		base, raw, unit string
		task            bool
	}{
		{"aws_amazonmwaa_dagduration_success", "DAGDurationSuccess", "ms", false},
		{"aws_amazonmwaa_dagduration_failed", "DAGDurationFailed", "ms", false},
		{"aws_amazonmwaa_task_instance_failures", "TaskInstanceFailures", "{Count}", true},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			e, ok := Lookup(tt.base)
			if !ok || e.Namespace != "AmazonMWAA" || e.MetricName != tt.raw || e.Unit != tt.unit {
				t.Fatalf("native entry: %+v found=%v", e, ok)
			}
			if got := cloudWatchMangledBase("AmazonMWAA", tt.raw); got != tt.base {
				t.Fatalf("naming law gives %s", got)
			}
			labels := map[string]string{"dimension_Environment": "env", "dimension_DAG": "orders", "dimension_Function": "Scheduler", "dimension_Task": "invented", "dimension_State": "failed"}
			want := map[string]string{"Environment": "env", "DAG": "orders"}
			if tt.task {
				labels["dimension_DAG"] = "All"
				labels["dimension_Task"] = "All"
				want["DAG"] = "All"
				want["Task"] = "All"
			}
			if got := dimensions(tt.base, labels); !reflect.DeepEqual(got, want) {
				t.Fatalf("dimensions %v want %v", got, want)
			}
			delete(labels, "dimension_DAG")
			delete(want, "DAG")
			if got := dimensions(tt.base, labels); !reflect.DeepEqual(got, want) {
				t.Fatalf("absent dimension invented: %v", got)
			}
		})
	}
}
