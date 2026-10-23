// SPDX-License-Identifier: AGPL-3.0-only

package mwaa

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

var p0 = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

func pipelineConfig() *Config {
	cfg := &Config{Environments: []string{"airflow-a", "airflow-plain"}}
	cfg.UsePipelines([]pipelinerun.Pipeline{{
		Name: "orders", Environment: "airflow-a", Every: "5m", Offset: "1m",
		Stages: []pipelinerun.Stage{
			{Name: "extract", Service: pipelinerun.ServiceGlue, Resource: "orders-extract", Duration: "1m"},
			{Name: "transform", Service: pipelinerun.ServiceGlue, Resource: "orders-transform", Duration: "2m"},
			{Name: "publish", Service: pipelinerun.ServiceAirflow, Duration: "30s"},
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

func tickEnv(t *testing.T, c core.Construct, eng *shape.Engine, ticks int, name, env string) []float64 {
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
			if s.Labels["dimension_Environment"] == env {
				v, found = s.Value, true
			}
		}
		if !found {
			t.Fatalf("tick %d: %s missing for %s", i, name, env)
		}
		out = append(out, v)
	}
	return out
}

func TestMWAAStageStallRaisesOldestTaskAge(t *testing.T) {
	from := p0.Add(10 * time.Minute)
	stall := []string{"stage_stall@" + from.Format(time.RFC3339) + "/30m@orders.transform"}
	age := tickEnv(t, buildPipeline(t, "s"), shape.New("UTC", stall), 60,
		"aws_mwaa_approximate_age_of_oldest_task_maximum", "airflow-a")
	queued := tickEnv(t, buildPipeline(t, "s"), shape.New("UTC", stall), 60,
		"aws_mwaa_queued_tasks_maximum", "airflow-a")
	var last float64
	rose := false
	for i, v := range age {
		at := p0.Add(time.Duration(i+1) * time.Minute)
		switch {
		case at.Before(from):
			if v != 0 || queued[i] != 0 {
				t.Fatalf("%v: age %v queued %v before the stall", at, v, queued[i])
			}
		case at.Before(from.Add(30 * time.Minute)):
			if v < last {
				t.Fatalf("%v: age fell from %v to %v under the stall", at, last, v)
			}
			if v > last {
				rose = true
			}
			last = v
		case at.After(from.Add(31 * time.Minute)):
			if v != 0 {
				t.Fatalf("%v: age %v after the stall cleared", at, v)
			}
		}
	}
	if !rose || last < 20*60 {
		t.Fatalf("age never climbed under the stall (last %v)", last)
	}
	if queued[38] < 5 {
		t.Fatalf("queued tasks %v at the end of a 30m stall at a 5m cadence", queued[38])
	}
}

func TestMWAAPipelineEnvironmentWithoutIncidentHasNoBacklog(t *testing.T) {
	age := tickEnv(t, buildPipeline(t, "s"), shape.New("UTC", nil), 30,
		"aws_mwaa_approximate_age_of_oldest_task_maximum", "airflow-a")
	running := tickEnv(t, buildPipeline(t, "s"), shape.New("UTC", nil), 30,
		"aws_mwaa_running_tasks_maximum", "airflow-a")
	sum := 0.0
	for i := range age {
		if age[i] != 0 {
			t.Fatalf("tick %d: age %v with no incident", i, age[i])
		}
		sum += running[i]
	}
	if sum == 0 {
		t.Fatal("declared pipeline never ran a task")
	}
}

func TestMWAAEnvironmentWithoutPipelineKeepsLegacyValues(t *testing.T) {
	plain, err := Build(&Config{Environments: []string{"airflow-a", "airflow-plain"}}, &fixture.Set{
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
	if got, want := dump(b.All(), "airflow-plain"), dump(a.All(), "airflow-plain"); got != want {
		t.Fatalf("environment with no pipeline changed:\n%s\nwant\n%s", got, want)
	}
	if inventory(a.All()) != inventory(b.All()) {
		t.Fatal("pipelines changed the series inventory")
	}
}

func TestMWAAPipelineDumpIsDeterministic(t *testing.T) {
	render := func(at time.Time) (string, string) {
		mc := &coretest.MetricCapture{}
		if err := buildPipeline(t, "seed-a").Tick(context.Background(), at, coretest.World(mc, nil, nil)); err != nil {
			t.Fatal(err)
		}
		return dump(mc.All(), ""), inventory(mc.All())
	}
	d1, i1 := render(p0)
	d2, _ := render(p0)
	_, i3 := render(p0.Add(211 * time.Minute))
	if d1 != d2 {
		t.Fatal("same seed and time rendered different bytes")
	}
	if i1 != i3 {
		t.Fatal("series inventory varies with time")
	}
}

func TestMWAARejectsPipelineForUnknownEnvironment(t *testing.T) {
	cfg := pipelineConfig()
	cfg.Pipelines[0].Environment = "nope"
	_, err := Build(cfg, &fixture.Set{Cloud: &fixture.Cloud{AccountID: "1", Region: "r"}})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("got %v", err)
	}
}

func dump(ss []promrw.Series, env string) string {
	var lines []string
	for _, s := range ss {
		if env != "" && s.Labels["dimension_Environment"] != env {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %v", key(s), s.Value))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func inventory(ss []promrw.Series) string {
	var lines []string
	for _, s := range ss {
		lines = append(lines, key(s))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func key(s promrw.Series) string {
	ks := make([]string, 0, len(s.Labels))
	for k, v := range s.Labels {
		ks = append(ks, k+"="+v)
	}
	sort.Strings(ks)
	return s.Name + "{" + strings.Join(ks, ",") + "}"
}
