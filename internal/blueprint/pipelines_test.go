// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/construct/glue"
	"github.com/rknightion/synthkit/internal/construct/mwaa"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/pipelinerun"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

func pipelineFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../e2e/fixtures/e2e-pipelines.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Duplicate the real fixture's declaration, not its incidents. Separate accounts keep the
// CloudWatch resource identities distinct even when both declarations use the same job names.
func pipelineEnvironments(t *testing.T, rename bool) string {
	t.Helper()
	text := pipelineFixture(t)
	parts := strings.SplitN(text, "incidents:\n", 2)
	env := strings.SplitN(parts[0], "environments:\n", 2)[1]
	second := strings.NewReplacer("name: prod", "name: shadow", "111122223333", "444455556666").Replace(env)
	if rename {
		second = strings.ReplaceAll(second, "orders_batch", "shadow_batch")
	}
	return parts[0] + second + "incidents:\n" + parts[1]
}

func TestPipelinesFanIntoEveryConsumer(t *testing.T) {
	r, err := blueprint.Load([]byte(pipelineFixture(t)), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, ci := range r.Constructs {
		var ps []pipelinerun.Pipeline
		switch cfg := ci.Config.(type) {
		case *glue.Config:
			ps = cfg.Pipelines
		case *mwaa.Config:
			ps = cfg.Pipelines
		default:
			continue
		}
		if len(ps) != 1 || ps[0].Name != "orders_batch" || len(ps[0].Stages) != 3 {
			t.Fatalf("%s did not receive the declaration: %+v", ci.Kind, ps)
		}
		seen++
	}
	if seen != 2 {
		t.Fatalf("expected glue and mwaa instances, saw %d", seen)
	}
	stages := map[string]bool{}
	for _, tg := range r.Targets {
		if tg.Axis == pipelinerun.AxisStage {
			stages[tg.Name] = true
		}
	}
	for _, n := range []string{"orders_batch.extract", "orders_batch.transform", "orders_batch.publish"} {
		if !stages[n] {
			t.Errorf("stage target %s missing (have %v)", n, stages)
		}
	}
	joined := strings.Join(r.Incidents, "\n")
	for _, want := range []string{"stage_stall@every60m/15m@orders_batch.transform", "run_failure@every70m/10m#0.5@orders_batch.extract"} {
		if !strings.Contains(joined, want) {
			t.Errorf("incident %q not compiled; have:\n%s", want, joined)
		}
	}
}

func TestPipelineStageWildcardExpands(t *testing.T) {
	text := strings.Replace(pipelineFixture(t), "target: orders_batch.transform", "target: \"pipeline_stage:*\"", 1)
	r, err := blueprint.Load([]byte(text), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, inc := range r.Incidents {
		if strings.HasPrefix(inc, "stage_stall@") {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("pipeline_stage:* expanded to %d entries: %v", n, r.Incidents)
	}
}

func TestPipelineDeclarationErrors(t *testing.T) {
	cases := map[string]func(string) string{
		"unknown target": func(s string) string {
			return strings.Replace(s, "target: orders_batch.transform", "target: orders_batch.nope", 1)
		},
		"needs a cloud.glue block": func(s string) string {
			return strings.Replace(s, "      glue:\n        jobs: []\n", "", 1)
		},
		"needs a cloud.mwaa block": func(s string) string {
			return strings.Replace(s, "      mwaa:\n        environments: [pipelines-airflow]\n", "", 1)
		},
		"unknown service": func(s string) string {
			return strings.Replace(s, "service: airflow", "service: kinesis", 1)
		},
	}
	for want, mut := range cases {
		_, err := blueprint.Load([]byte(mut(pipelineFixture(t))), runner.Catalog())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestPipelineNamesCannotRepeatAcrossEnvironments(t *testing.T) {
	for _, disjointStages := range []bool{false, true} {
		t.Run(fmt.Sprintf("disjoint stages %v", disjointStages), func(t *testing.T) {
			text := pipelineEnvironments(t, false)
			if disjointStages {
				parts := strings.SplitN(text, "  - name: shadow", 2)
				text = parts[0] + "  - name: shadow" + strings.NewReplacer("name: extract", "name: read", "name: transform", "name: process", "name: publish", "name: write").Replace(parts[1])
			}
			r, err := blueprint.Load([]byte(text), runner.Catalog())
			if err == nil {
				// Red proof: the inherited loader silently accepts duplicate stage targets.
				// Drive its real consumers to show the one scope affects both accounts.
				_, ages := renderPipelineEnvironments(t, r, true)
				var targets []string
				for _, tg := range r.Targets {
					if tg.Axis == pipelinerun.AxisStage {
						targets = append(targets, tg.Name)
					}
				}
				t.Fatalf("pipeline identity collision accepted: targets=%v; oldest queued task ages by env=%v", targets, ages)
			}
			for _, want := range []string{"pipeline identity collision", "orders_batch", "prod", "shadow"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
		})
	}
}

// Exercise Load -> registry Build -> real Glue/MWAA Tick -> captured metric values, using the
// compiled incident entries. A fixed epoch window covers the recurring stall and its clear.
func renderPipelineEnvironments(t *testing.T, r *blueprint.Resolved, incidents bool) (map[string]string, map[string]float64) {
	t.Helper()
	var entries []string
	if incidents {
		entries = r.Incidents
	}
	eng := shape.New("UTC", entries)
	outputs, ages := map[string]string{}, map[string]float64{}
	start := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	reg := runner.Catalog()
	for _, ci := range r.Constructs {
		if ci.Kind != "glue" && ci.Kind != "mwaa" {
			continue
		}
		cr, _ := reg.Construct(ci.Kind)
		c, err := cr.Build(ci.Config, ci.Fixtures)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for i := 1; i <= 30; i++ {
			mc := &coretest.MetricCapture{}
			w := &core.World{Shape: eng, Metrics: mc}
			if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), w); err != nil {
				t.Fatal(err)
			}
			for _, s := range mc.All() {
				lines = append(lines, fmt.Sprintf("%d %s %v %v", i, s.Name, s.Labels, s.Value))
				if s.Name == "aws_mwaa_approximate_age_of_oldest_task_maximum" && s.Value > ages[ci.Name] {
					ages[ci.Name] = s.Value
				}
			}
		}
		sort.Strings(lines)
		outputs[ci.Name+"/"+ci.Kind] = strings.Join(lines, "\n")
	}
	return outputs, ages
}

func TestPipelineEnvironmentsRemainIsolatedAndDeterministic(t *testing.T) {
	text := pipelineEnvironments(t, true)
	r, err := blueprint.Load([]byte(text), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	seeds := map[string]string{}
	for _, tg := range r.Targets {
		if tg.Axis == pipelinerun.AxisStage {
			if seen[tg.Name] {
				t.Fatalf("duplicate target %q", tg.Name)
			}
			seen[tg.Name] = true
		}
	}
	for _, ci := range r.Constructs {
		if ci.Kind == "glue" || ci.Kind == "mwaa" {
			if prev := seeds[ci.Name]; prev != "" && prev != ci.Fixtures.Seed {
				t.Fatalf("consumer seed mismatch for %s", ci.Name)
			}
			seeds[ci.Name] = ci.Fixtures.Seed
		}
	}
	if seeds["prod"] == "" || seeds["shadow"] == "" || seeds["prod"] == seeds["shadow"] {
		t.Fatalf("environment seeds not isolated: %v", seeds)
	}
	if len(seen) != 6 || !seen["shadow_batch.transform"] {
		t.Fatalf("stage inventory incomplete: %v", seen)
	}
	baseline, _ := renderPipelineEnvironments(t, r, false)
	active, ages := renderPipelineEnvironments(t, r, true)
	for _, kind := range []string{"glue", "mwaa"} {
		if active["prod/"+kind] == baseline["prod/"+kind] {
			t.Errorf("incident never affected prod %s", kind)
		}
		if active["shadow/"+kind] != baseline["shadow/"+kind] {
			t.Errorf("prod incident leaked into shadow %s", kind)
		}
	}
	if ages["prod"] <= 0 || ages["shadow"] != 0 {
		t.Fatalf("wrong backlog isolation: %v", ages)
	}
	// Reorder declarations to rule out positional identity and reconstruct every consumer.
	parts := strings.SplitN(text, "environments:\n", 2)
	envs := strings.SplitN(parts[1], "incidents:\n", 2)
	both := strings.SplitN(envs[0], "  - name: shadow", 2)
	reordered := parts[0] + "environments:\n  - name: shadow" + both[1] + both[0] + "incidents:\n" + envs[1]
	r2, err := blueprint.Load([]byte(reordered), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	again, _ := renderPipelineEnvironments(t, r2, true)
	if !reflect.DeepEqual(active, again) {
		t.Fatal("same named environments/seed/time rendered different bytes after reordering")
	}
}
