// SPDX-License-Identifier: AGPL-3.0-only

package glue

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/pipelinerun"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

type coreSeries = promrw.Series

var p0 = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

func pipelineConfig() *Config {
	cfg := &Config{Jobs: []string{"legacy-job"}}
	cfg.UsePipelines([]pipelinerun.Pipeline{{
		Name: "orders", Every: "5m", Offset: "1m",
		Stages: []pipelinerun.Stage{
			{Name: "extract", Service: pipelinerun.ServiceGlue, Resource: "orders-extract", Duration: "1m"},
			{Name: "transform", Service: pipelinerun.ServiceGlue, Resource: "orders-transform", Duration: "2m"},
		},
	}})
	return cfg
}

func buildPipeline(t *testing.T, seed string) core.Construct {
	t.Helper()
	c, err := Build(pipelineConfig(), &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"}, Seed: seed,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// tickSeries ticks c once per minute and returns, per tick, the value of name for job.
func tickSeries(t *testing.T, c core.Construct, eng *shape.Engine, ticks int, name, job string) []float64 {
	t.Helper()
	var out []float64
	for i := 1; i <= ticks; i++ {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
		w.Shape = eng
		if err := c.Tick(context.Background(), p0.Add(time.Duration(i)*time.Minute), w); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		v, found := 0.0, false
		for _, s := range mc.Find(name) {
			if s.Labels["dimension_JobName"] == job {
				v, found = s.Value, true
			}
		}
		if !found {
			t.Fatalf("tick %d: %s missing for job %s", i, name, job)
		}
		out = append(out, v)
	}
	return out
}

func TestGluePipelineStageJobsAreEmitted(t *testing.T) {
	mc := &coretest.MetricCapture{}
	if err := buildPipeline(t, "s").Tick(context.Background(), p0, coretest.World(mc, nil, nil)); err != nil {
		t.Fatal(err)
	}
	jobs := map[string]bool{}
	for _, s := range mc.Find("aws_glue_info") {
		jobs[s.Labels["dimension_JobName"]] = true
	}
	for _, j := range []string{"legacy-job", "orders-extract", "orders-transform"} {
		if !jobs[j] {
			t.Errorf("job %s not emitted (got %v)", j, jobs)
		}
	}
}

func TestGluePipelineRunsDriveCompletedTasks(t *testing.T) {
	vals := tickSeries(t, buildPipeline(t, "s"), shape.New("UTC", nil), 30,
		"aws_glue_driver_aggregate_num_completed_tasks_sum", "orders-transform")
	idle, busy := 0, 0
	for _, v := range vals {
		if v == 0 {
			idle++
		} else {
			busy++
		}
	}
	// A 2m stage every 5m: busy in roughly 2-3 of every 5 windows, idle in the rest.
	if idle == 0 || busy == 0 {
		t.Fatalf("pipeline job must alternate busy and idle windows: %v", vals)
	}
}

func TestGlueStageStallSuppressesCompletionsUntilCleared(t *testing.T) {
	from := p0.Add(10 * time.Minute)
	eng := shape.New("UTC", []string{"stage_stall@" + from.Format(time.RFC3339) + "/30m@orders.transform"})
	vals := tickSeries(t, buildPipeline(t, "s"), eng, 70,
		"aws_glue_driver_aggregate_num_completed_tasks_sum", "orders-transform")
	for i, v := range vals {
		at := p0.Add(time.Duration(i+1) * time.Minute)
		// After in-flight work drains (one stage duration), nothing completes until cleared.
		if at.After(from.Add(3*time.Minute)) && at.Before(from.Add(30*time.Minute)) && v != 0 {
			t.Fatalf("%v: completed tasks %v under stage_stall", at, v)
		}
	}
	resumed := false
	for i := 40; i < 45; i++ {
		if vals[i] > 0 {
			resumed = true
		}
	}
	if !resumed {
		t.Fatalf("stalled backlog did not run on clear: %v", vals[38:46])
	}
	// The upstream stage keeps running throughout.
	up := tickSeries(t, buildPipeline(t, "s"), shape.New("UTC", []string{"stage_stall@" + from.Format(time.RFC3339) + "/30m@orders.transform"}), 40,
		"aws_glue_driver_aggregate_num_completed_tasks_sum", "orders-extract")
	busy := 0
	for _, v := range up[15:] {
		if v > 0 {
			busy++
		}
	}
	if busy == 0 {
		t.Fatal("upstream stage stopped under a downstream stall")
	}
}

func TestGlueRunFailureReportsFailedTasks(t *testing.T) {
	from := p0.Add(10 * time.Minute)
	eng := shape.New("UTC", []string{"run_failure@" + from.Format(time.RFC3339) + "/20m@orders.extract"})
	failed := tickSeries(t, buildPipeline(t, "s"), eng, 50,
		"aws_glue_driver_aggregate_num_failed_tasks_sum", "orders-extract")
	down := tickSeries(t, buildPipeline(t, "s"), shape.New("UTC", []string{"run_failure@" + from.Format(time.RFC3339) + "/20m@orders.extract"}), 50,
		"aws_glue_driver_aggregate_num_completed_tasks_sum", "orders-transform")
	total := 0.0
	for i, v := range failed {
		at := p0.Add(time.Duration(i+1) * time.Minute)
		inside := !at.Before(from) && at.Before(from.Add(20*time.Minute))
		if !inside && v != 0 {
			t.Fatalf("%v: failed tasks %v outside the incident", at, v)
		}
		if v != 0 && int(v)%sparkTaskMaxFailures != 0 {
			t.Fatalf("%v: failed tasks %v not a multiple of spark.task.maxFailures", at, v)
		}
		total += v
		if inside && at.After(from.Add(4*time.Minute)) && down[i] != 0 {
			t.Fatalf("%v: downstream completed %v after upstream runs failed", at, down[i])
		}
	}
	if total == 0 {
		t.Fatal("run_failure produced no failed tasks")
	}
}

func TestGlueLegacyJobUnchangedByPipelines(t *testing.T) {
	plain, err := Build(&Config{Jobs: []string{"legacy-job"}}, &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"}, Seed: "s",
	})
	if err != nil {
		t.Fatal(err)
	}
	a, b := &coretest.MetricCapture{}, &coretest.MetricCapture{}
	if err := plain.Tick(context.Background(), p0, coretest.World(a, nil, nil)); err != nil {
		t.Fatal(err)
	}
	if err := buildPipeline(t, "s").Tick(context.Background(), p0, coretest.World(b, nil, nil)); err != nil {
		t.Fatal(err)
	}
	if got, want := dump(b.All(), "legacy-job"), dump(a.All(), "legacy-job"); got != want {
		t.Fatalf("legacy job output changed:\n%s\nwant\n%s", got, want)
	}
}

func TestGluePipelineDumpIsDeterministic(t *testing.T) {
	render := func(seed string, at time.Time) (string, string) {
		mc := &coretest.MetricCapture{}
		if err := buildPipeline(t, seed).Tick(context.Background(), at, coretest.World(mc, nil, nil)); err != nil {
			t.Fatal(err)
		}
		return dump(mc.All(), ""), inventory(mc.All())
	}
	d1, i1 := render("seed-a", p0)
	d2, i2 := render("seed-a", p0)
	if d1 != d2 {
		t.Fatal("same seed and time rendered different bytes")
	}
	_, i3 := render("seed-a", p0.Add(137*time.Minute))
	if i1 != i3 || i1 != i2 {
		t.Fatal("series inventory varies with time; just dump would not be stable")
	}
}

func TestGlueRejectsInvalidPipeline(t *testing.T) {
	cfg := &Config{}
	cfg.UsePipelines([]pipelinerun.Pipeline{{Name: "bad", Every: "5m"}})
	if _, err := Build(cfg, &fixture.Set{Cloud: &fixture.Cloud{AccountID: "1", Region: "r"}}); err == nil {
		t.Fatal("pipeline without stages accepted")
	}
}

func dump(ss []coreSeries, job string) string {
	var lines []string
	for _, s := range ss {
		if job != "" && s.Labels["dimension_JobName"] != job {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %v", key(s), s.Value))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func inventory(ss []coreSeries) string {
	var lines []string
	for _, s := range ss {
		lines = append(lines, key(s))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func key(s coreSeries) string {
	ks := make([]string, 0, len(s.Labels))
	for k, v := range s.Labels {
		ks = append(ks, k+"="+v)
	}
	sort.Strings(ks)
	return s.Name + "{" + strings.Join(ks, ",") + "}"
}
