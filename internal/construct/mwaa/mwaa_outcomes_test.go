// SPDX-License-Identifier: AGPL-3.0-only

package mwaa

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/pipelinerun"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

type outcomeNativeCapture struct{ resources []otlp.MetricResource }

func (c *outcomeNativeCapture) Write(_ context.Context, rs []otlp.MetricResource) error {
	c.resources = append(c.resources, rs...)
	return nil
}

// Exercise the renderer's real window, export modes, sparse lifecycle and dimension contract.
func TestMWAAOutcomesActualOutput(t *testing.T) {
	cfg := pipelineConfig()
	// Two pipelines share one environment, so failures must aggregate, not overwrite.
	other := cfg.Pipelines[0]
	other.Name = "returns"
	cfg.Pipelines = append(cfg.Pipelines, other)
	fx := &fixture.Set{Seed: "outcomes", Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"}}
	rw, err := Build(cfg, fx)
	if err != nil {
		t.Fatal(err)
	}
	nativeFX := *fx
	nativeCloud := *fx.Cloud
	nativeCloud.CloudWatchExport = "otlp"
	nativeFX.Cloud = &nativeCloud
	native, err := Build(cfg, &nativeFX)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := pipelinerun.NewTracker(fx.Seed, cfg.Pipelines, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	from := p0.Add(10 * time.Minute)
	eng := shape.New("UTC", []string{
		"stage_stall@" + from.Format(time.RFC3339) + "/30m@orders.transform",
		"stage_stall@" + from.Format(time.RFC3339) + "/30m@returns.transform",
		"run_failure@" + from.Add(30*time.Minute).Format(time.RFC3339) + "/30m@orders.transform",
		"run_failure@" + from.Add(30*time.Minute).Format(time.RFC3339) + "/30m@returns.transform",
	})
	sawSuccess, sawFailure, sawMulti, sawEmptyAfter := false, false, false, false
	// A coarse post-stall window has several completions, not one synthetic average sample.
	for _, minute := range []int{1, 10, 39, 69, 70, 71, 72, 73, 74, 75, 76} {
		now := p0.Add(time.Duration(minute) * time.Minute)
		win := tracker.Advance(now, eng.Eval)
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
		w.Shape = eng
		if err := rw.Tick(context.Background(), now, w); err != nil {
			t.Fatal(err)
		}
		nc := &outcomeNativeCapture{}
		nw := coretest.World(nil, nil, nil)
		nw.Shape = eng
		nw.OTLPMetrics = nc
		if err := native.Tick(context.Background(), now, nw); err != nil {
			t.Fatal(err)
		}
		expected := map[string]map[string]float64{}
		failures := 0
		for _, p := range win.Pipelines {
			for _, s := range p.Stages {
				failures += s.Failed
			}
			for _, o := range []struct {
				name          string
				n             int
				sum, min, max float64
			}{
				{"dagduration_success", p.Succeeded, p.SucceededDurSec, p.SucceededDurMinSec, p.SucceededDurMaxSec},
				{"dagduration_failed", p.Failed, p.FailedDurSec, p.FailedDurMinSec, p.FailedDurMaxSec},
			} {
				if o.n == 0 {
					continue
				}
				if o.name == "dagduration_success" {
					sawSuccess = true
				} else {
					sawFailure = true
				}
				if o.n > 1 && o.min < o.max {
					sawMulti = true
				}
				expected[o.name+"/"+p.Name] = map[string]float64{"_sum": o.sum * 1000, "_average": o.sum * 1000 / float64(o.n), "_minimum": o.min * 1000, "_maximum": o.max * 1000, "_sample_count": float64(o.n)}
			}
		}
		if failures > 0 {
			expected["task_instance_failures/All"] = map[string]float64{"_sum": float64(failures), "_average": 1, "_minimum": 1, "_maximum": 1, "_sample_count": float64(failures)}
		}
		got := map[string]map[string]float64{}
		for _, s := range mc.All() {
			if !isOutcome(s.Name) {
				continue
			}
			if s.Labels["namespace"] != "AmazonMWAA" || s.Labels["dimension_Environment"] != "airflow-a" {
				t.Fatalf("bad identity: %v", s)
			}
			dims := map[string]string{}
			for k, v := range s.Labels {
				if strings.HasPrefix(k, "dimension_") {
					dims[k] = v
				}
			}
			wantDims := map[string]string{"dimension_Environment": "airflow-a", "dimension_DAG": s.Labels["dimension_DAG"]}
			if strings.Contains(s.Name, "task_instance_failures") {
				wantDims["dimension_DAG"] = "All"
				wantDims["dimension_Task"] = "All"
			}
			if !reflect.DeepEqual(dims, wantDims) {
				t.Fatalf("unexpected/absent dimensions: %v want %v", dims, wantDims)
			}
			for _, suffix := range []string{"_sample_count", "_average", "_minimum", "_maximum", "_sum"} {
				if strings.HasSuffix(s.Name, suffix) {
					k := strings.TrimSuffix(strings.TrimPrefix(s.Name, "aws_amazonmwaa_"), suffix) + "/" + s.Labels["dimension_DAG"]
					if got[k] == nil {
						got[k] = map[string]float64{}
					}
					got[k][suffix] = s.Value
					break
				}
			}
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("minute %d actual outcomes %v want %v", minute, got, expected)
		}
		// Compare actual native transport output with independent expected statistics/dimensions.
		nativeSeen := map[string]bool{}
		for _, r := range nc.resources {
			for _, m := range r.Metrics {
				if !strings.Contains(m.Name, "DAGDuration") && !strings.Contains(m.Name, "TaskInstanceFailures") {
					continue
				}
				names := map[string]string{"DAGDurationSuccess": "dagduration_success", "DAGDurationFailed": "dagduration_failed", "TaskInstanceFailures": "task_instance_failures"}
				for _, sp := range m.Summaries {
					raw, _ := sp.Attrs["MetricName"].(string)
					dims, _ := sp.Attrs["Dimensions"].(map[string]string)
					key := names[raw] + "/" + dims["DAG"]
					stats, ok := expected[key]
					if !ok {
						t.Fatalf("unexpected native %s %v", m.Name, sp)
					}
					unit := "ms"
					wantDims := map[string]string{"Environment": "airflow-a", "DAG": dims["DAG"]}
					if raw == "TaskInstanceFailures" {
						unit = "{Count}"
						wantDims["Task"] = "All"
					}
					if m.Unit != unit || sp.Attrs["Namespace"] != "AmazonMWAA" || !reflect.DeepEqual(dims, wantDims) || sp.Count != uint64(stats["_sample_count"]) || sp.Sum != stats["_sum"] || sp.Quantiles[0] != stats["_minimum"] || sp.Quantiles[1] != stats["_maximum"] {
						t.Fatalf("native mismatch %s %+v", m.Name, sp)
					}
					nativeSeen[key] = true
				}
			}
		}
		if len(nativeSeen) != len(expected) {
			t.Fatalf("minute %d native outcomes %v want %v", minute, nativeSeen, expected)
		}
		if minute > 69 && len(expected) == 0 {
			sawEmptyAfter = true
		}
	}
	if !sawSuccess || !sawFailure || !sawMulti || !sawEmptyAfter {
		t.Fatal(fmt.Sprintf("insufficient coverage success=%v failure=%v multi=%v empty-after=%v", sawSuccess, sawFailure, sawMulti, sawEmptyAfter))
	}
}
func isOutcome(name string) bool {
	return strings.HasPrefix(name, "aws_amazonmwaa_dagduration_") || strings.HasPrefix(name, "aws_amazonmwaa_task_instance_failures_")
}
