// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/inventory"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"gopkg.in/yaml.v3"
)

func remainingFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../e2e/fixtures/e2e-cw-remaining.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Independent expectations from the admitted source/config contract: no reflection
// on renderer constants, no dump union used to certify dimension alternatives or values.
func remainingExpected(selector string) map[string]float64 {
	out := map[string]float64{}
	stocks := map[string][2]float64{
		"cpuutilization": {25, 0}, "database_connections": {7, 0}, "health_status": {1, 0},
		"maintenance_mode": {0, 1}, "percentage_disk_space_used": {40, 0}, "total_table_count": {12, 0},
	}
	add := func(base, resource, profile string, stats [5]float64) {
		for i, suffix := range []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"} {
			out[selector+"|"+base+suffix+"|"+resource+"|"+profile] = stats[i]
		}
	}
	for root, values := range stocks {
		for i, resource := range []string{"fixture-warehouse", "fixture-idle-warehouse"} {
			v := values[i]
			add("aws_redshift_"+root, resource, "cluster", [5]float64{v, v, v, v, 1})
		}
	}
	add("aws_redshift_read_latency", "fixture-warehouse", "cluster", [5]float64{15, .125, .125, .125, 120})
	add("aws_redshift_write_latency", "fixture-warehouse", "cluster", [5]float64{7.5, .25, .25, .25, 30})
	add("aws_redshift_wlmqueue_length", "fixture-warehouse", "queue", [5]float64{3, 3, 3, 3, 1})
	add("aws_redshift_wlmrunning_queries", "fixture-warehouse", "queue", [5]float64{2, 2, 2, 2, 1})
	add("aws_logs_incoming_bytes", "/fixture/intake", "loggroup", [5]float64{30720, 256, 256, 256, 120})
	add("aws_logs_incoming_log_events", "/fixture/intake", "loggroup", [5]float64{120, 1, 1, 1, 120})
	return out
}

func TestRemainingCWPublicValuesAndScope(t *testing.T) {
	reg := runner.Catalog()
	mc := &coretest.MetricCapture{}
	r := runner.New(runner.Sinks{Metrics: mc}, reg, runner.Options{SendShards: 1, SendDrainDeadline: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer r.DrainQueues(ctx)
	expected := map[string]float64{}
	var resolved []*blueprint.Resolved
	for _, selector := range []string{"remaining-selector", "second-selector"} {
		text := strings.NewReplacer("name: e2e-cw-remaining", "name: "+selector, "label: remaining-selector", "label: "+selector).Replace(remainingFixture(t))
		res, err := blueprint.Load([]byte(text), reg)
		if err != nil {
			t.Fatal(err)
		}
		resolved = append(resolved, res)
		ownership := map[string]int{}
		for _, ci := range res.Constructs {
			ownership[ci.Kind]++
		}
		if !reflect.DeepEqual(ownership, map[string]int{"cw_infra": 1, "redshift": 1, "cloudwatch_logs": 1}) {
			t.Fatalf("unexpected ownership %v", ownership)
		}
		if err := r.AddBlueprint(res); err != nil {
			t.Fatal(err)
		}
		for k, v := range remainingExpected(selector) {
			expected[k] = v
		}
	}
	if err := blueprint.ValidateSet(resolved); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(ctx, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range mc.All() {
		wantLabels := map[string]string{"account_id": "111122223333", "region": "us-east-1", "blueprint": s.Labels["blueprint"]}
		resource, profile := s.Labels["dimension_ClusterIdentifier"], "cluster"
		if strings.HasPrefix(s.Name, "aws_redshift_") {
			wantLabels["namespace"], wantLabels["job"] = "AWS/Redshift", "cloud/aws/redshift"
			wantLabels["dimension_ClusterIdentifier"] = resource
			wantLabels["name"] = "declared-warehouse"
			if resource == "fixture-idle-warehouse" {
				wantLabels["name"] = "declared-idle-warehouse"
			}
			if strings.HasPrefix(s.Name, "aws_redshift_wlm") {
				profile = "queue"
				wantLabels["dimension_QueueName"] = "fixture-queue"
			}
		} else if strings.HasPrefix(s.Name, "aws_logs_") {
			resource, profile = s.Labels["dimension_LogGroupName"], "loggroup"
			wantLabels["namespace"], wantLabels["job"], wantLabels["name"] = "AWS/Logs", "cloud/aws/logs", "global"
			wantLabels["dimension_LogGroupName"] = "/fixture/intake"
		} else {
			t.Fatalf("unrelated/withheld family %s", s.Name)
		}
		key := s.Labels["blueprint"] + "|" + s.Name + "|" + resource + "|" + profile
		value, ok := expected[key]
		if !ok || seen[key] {
			t.Fatalf("unexpected/duplicate series %s", key)
		}
		seen[key] = true
		if !reflect.DeepEqual(s.Labels, wantLabels) || s.Kind != promrw.KindGauge || s.Value != value {
			t.Fatalf("%s: got %+v labels=%v; want %g labels=%v", key, s, s.Labels, value, wantLabels)
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("observed %d of %d exact series", len(seen), len(expected))
	}
	for _, kind := range []string{"lambda", "sqs", "dynamodb", "redshift", "cloudwatch_logs"} {
		cr, ok := reg.Construct(kind)
		if !ok || cr.MetricProducer(nil) != "unlabelled" || cr.OTLPMetricProducer != nil {
			t.Fatalf("incorrect producer admission %s", kind)
		}
	}
}

// The actual CLI, in an isolated directory with an allowlisted credential-free
// environment. This catches strict-loader and fatal Build failures absent from a
// construct-only test. Inventory union is only name coverage, never value proof.
func TestRemainingCWCLI(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "synthkit")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/synthkit")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	run := func(text string, args ...string) ([]byte, []byte, error) {
		t.Helper()
		dir := t.TempDir()
		fixtures := filepath.Join(dir, "fixtures")
		if err := os.Mkdir(fixtures, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixtures, "remaining.yaml"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, append([]string{"-env", "/dev/null"}, args...)...)
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "DRY_RUN=true", "BLUEPRINTS=" + fixtures, "BLUEPRINT_NAMES=e2e-cw-remaining", "BLUEPRINT_DATA_DIR=" + filepath.Join(dir, "data"), "CONFIG_SNAPSHOT_PATH=" + filepath.Join(dir, "state.json"), "JSON_HTTP_ADDR=127.0.0.1:0"}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		return out, []byte(stderr.String()), err
	}
	fixture := remainingFixture(t)
	for _, kind := range []string{"lambda", "sqs", "dynamodb", "redshift", "cloudwatch_logs"} {
		for _, route := range []string{"mixed", "addons", "crossenv"} {
			t.Run(kind+" "+route+" CLI ceiling", func(t *testing.T) {
				out, stderr, err := run(string(capturedCWRoute(t, kind, route, "")), "-validate")
				if err == nil || !strings.Contains(string(out)+string(stderr), kind+" declaration collision") {
					t.Fatalf("duplicate CLI admission: %v\n%s\n%s", err, out, stderr)
				}
			})
		}
	}
	for _, args := range [][]string{{"-validate"}, {"-once", "-dump"}, {"-once", "-inventory-json"}} {
		out, stderr, err := run(fixture, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s\n%s", args, err, out, stderr)
		}
		if args[0] == "-validate" {
			var v struct {
				OK         bool  `json:"ok"`
				Blueprints []any `json:"blueprints"`
			}
			if err := json.Unmarshal(out, &v); err != nil || !v.OK || len(v.Blueprints) != 1 {
				t.Fatalf("validate: %s", out)
			}
		} else if args[1] == "-inventory-json" {
			var inv inventory.Schema
			if err := json.Unmarshal(out, &inv); err != nil {
				t.Fatal(err)
			}
			names := map[string]bool{}
			for _, m := range inv.Metrics {
				names[m.Name] = true
			}
			expected := map[string]bool{}
			for k := range remainingExpected("remaining-selector") {
				expected[strings.Split(k, "|")[1]] = true
			}
			if !reflect.DeepEqual(names, expected) || len(names) != 60 {
				t.Fatalf("CLI names=%v, want exact 60", names)
			}
		} else if !strings.Contains(string(out), "aws_redshift_read_latency_sample_count") || !strings.Contains(string(out), "aws_logs_incoming_bytes_sum") {
			t.Fatalf("missing CLI dump families: %s", out)
		}
	}
	for _, tc := range []struct{ name, old, replacement, want string }{
		{"unknown redshift nested", "cpu_percent: 25", "node_id: x", "field node_id"},
		{"unknown logs nested", "events_per_second: 2", "destination_type: x", "field destination_type"},
		{"missing snapshot field", "healthy: true, ", "", "all snapshot fields required"},
		{"fractional stock", "database_connections: 7", "database_connections: 7.5", "whole counts"},
		{"nonfinite percent", "cpu_percent: 25", "cpu_percent: .nan", "finite percents"},
		{"missing event rate", "events_per_second: 2, ", "", "events_per_second and uncompressed_bytes_per_event required"},
		{"fractional bytes", "uncompressed_bytes_per_event: 256", "uncompressed_bytes_per_event: 1.5", "finite whole bytes"},
		{"infinite event rate", "events_per_second: 2", "events_per_second: .inf", "finite in"},
		{"global redshift collector", "collector_name: declared-warehouse", "collector_name: global", "non-global collector_name"},
		{"native redshift", "redshift:", "cloudwatch_export: otlp\n      redshift:", "remote_write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Replace(fixture, tc.old, tc.replacement, 1)
			if text == fixture {
				t.Fatal("mutation did not apply")
			}
			// Validate produces source diagnostics for strict decode; runtime surfaces Build refusals.
			args := []string{"-once"}
			if strings.HasPrefix(tc.name, "unknown") {
				args = []string{"-validate"}
			}
			out, stderr, err := run(text, args...)
			if err == nil || !strings.Contains(string(out)+string(stderr), tc.want) {
				t.Fatalf("refusal %q: err=%v\n%s\n%s", tc.want, err, out, stderr)
			}
		})
	}
	// All five captured-service consumers remain RW-only at the real CLI boundary.
	for _, kind := range []string{"lambda", "sqs", "dynamodb"} {
		t.Run(kind+" native CLI refusal", func(t *testing.T) {
			var doc map[string]any
			text := strings.ReplaceAll(string(cwConsumerFixture(t)), "e2e-cw-consumers", "e2e-cw-remaining")
			if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
				t.Fatal(err)
			}
			cloud := doc["environments"].([]any)[0].(map[string]any)["cloud"].(map[string]any)
			for _, other := range []string{"lambda", "sqs", "dynamodb"} {
				if other != kind {
					delete(cloud, other)
				}
			}
			cloud["cloudwatch_export"] = "otlp"
			b, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			out, stderr, err := run(string(b), "-once")
			if err == nil || !strings.Contains(string(out)+string(stderr), kind) || !strings.Contains(string(out)+string(stderr), "remote_write") {
				t.Fatalf("native accepted: %v %s %s", err, out, stderr)
			}
		})
	}
	for _, kind := range []string{"redshift", "cloudwatch_logs"} {
		t.Run(kind+" native and duplicate declaration", func(t *testing.T) {
			var doc map[string]any
			if err := yaml.Unmarshal([]byte(fixture), &doc); err != nil {
				t.Fatal(err)
			}
			envs := doc["environments"].([]any)
			env := envs[0].(map[string]any)
			cloud := env["cloud"].(map[string]any)
			for _, other := range []string{"redshift", "cloudwatch_logs"} {
				if other != kind {
					delete(cloud, other)
				}
			}
			cloud["cloudwatch_export"] = "otlp"
			b, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			out, stderr, err := run(string(b), "-once")
			if err == nil || !strings.Contains(string(out)+string(stderr), kind) || !strings.Contains(string(out)+string(stderr), "remote_write") {
				t.Fatalf("native accepted: %v %s %s", err, out, stderr)
			}
			delete(cloud, "cloudwatch_export")
			// Disjoint resources still cannot own another declaration in the same account/region.
			secondBytes, err := yaml.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			var second map[string]any
			if err := yaml.Unmarshal([]byte(strings.ReplaceAll(string(secondBytes), "fixture", "shadow")), &second); err != nil {
				t.Fatal(err)
			}
			second["name"] = "shadow"
			doc["environments"] = append(envs, second)
			b, err = yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			out, stderr, err = run(string(b), "-validate")
			if err == nil || !strings.Contains(string(out)+string(stderr), "declaration collision") {
				t.Fatalf("duplicate admitted: %v %s %s", err, out, stderr)
			}
			// A distinct region is a distinct admitted owner.
			second["cloud"].(map[string]any)["region"] = "us-west-2"
			b, err = yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			out, stderr, err = run(string(b), "-once")
			if err != nil {
				t.Fatalf("distinct region rejected: %v %s %s", err, out, stderr)
			}
		})
	}
}

// Build alternate public authoring routes from the same valid resource configs.
// No component imports or invented identities: addons receive the environment fixture.
func capturedCWRoute(t *testing.T, kind, route, separation string) []byte {
	t.Helper()
	text := remainingFixture(t)
	if kind == "lambda" || kind == "sqs" || kind == "dynamodb" {
		text = strings.ReplaceAll(string(cwConsumerFixture(t)), "e2e-cw-consumers", "e2e-cw-remaining")
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	env := doc["environments"].([]any)[0].(map[string]any)
	cloud := env["cloud"].(map[string]any)
	config := cloud[kind].(map[string]any)
	for _, k := range []string{"lambda", "sqs", "dynamodb", "redshift", "cloudwatch_logs"} {
		delete(cloud, k)
	}
	addon := map[string]any{"name": kind}
	for k, v := range config {
		addon[k] = v
	}
	cluster := func(name string, addons ...any) map[string]any {
		return map[string]any{"name": name, "type": "eks", "node_groups": []any{map[string]any{"name": "main", "instance_type": "t3.medium", "desired": 1}}, "addons": addons}
	}
	env["cluster"] = cluster("fixture-cluster", addon)
	switch route {
	case "mixed":
		cloud[kind] = config
	case "addons":
		env["cluster"] = cluster("fixture-cluster", addon, addon)
	case "crossenv":
		secondCloud := map[string]any{}
		for k, v := range cloud {
			secondCloud[k] = v
		}
		if separation == "region" {
			secondCloud["region"] = "us-west-2"
		} else if separation == "account" {
			secondCloud["account_id"] = "444455556666"
		}
		doc["environments"] = []any{env, map[string]any{"name": "shadow", "cloud": secondCloud, "cluster": cluster("shadow-cluster", addon)}}
	case "single":
	default:
		t.Fatalf("unknown test route %q", route)
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCapturedCWAllRouteCeiling(t *testing.T) {
	for _, kind := range []string{"lambda", "sqs", "dynamodb", "redshift", "cloudwatch_logs"} {
		for _, tc := range []struct {
			route, separation string
			collision         bool
		}{
			{"mixed", "", true}, {"addons", "", true}, {"crossenv", "", true},
			{"single", "", false}, {"crossenv", "region", false}, {"crossenv", "account", false},
		} {
			t.Run(kind+"/"+tc.route+"/"+tc.separation, func(t *testing.T) {
				reg := runner.Catalog()
				res, err := blueprint.Load(capturedCWRoute(t, kind, tc.route, tc.separation), reg)
				if err != nil {
					if tc.collision && strings.Contains(err.Error(), kind+" declaration collision") {
						return
					}
					t.Fatal(err)
				}
				mc := &coretest.MetricCapture{}
				r := runner.New(runner.Sinks{Metrics: mc, Logs: &coretest.LogCapture{}}, reg, runner.Options{SendShards: 1, SendDrainDeadline: time.Second})
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				defer r.DrainQueues(ctx)
				if err := r.AddBlueprint(res); err != nil {
					t.Fatal(err)
				}
				if err := r.RunOnce(ctx, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)); err != nil {
					t.Fatal(err)
				}
				if tc.collision {
					t.Fatalf("duplicate %s declarations admitted through %s and executed (%d series)", kind, tc.route, len(mc.All()))
				}
			})
		}
	}
}

func TestRemainingCWCanonicalNameProfiles(t *testing.T) {
	// Parse only emitted blocks (never the larger source-positive tables).
	b, err := os.ReadFile("../../signals/cw.md")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{}
	for _, text := range strings.Split(string(b), "```yaml signals\n")[1:] {
		text = strings.SplitN(text, "```", 2)[0]
		var block struct {
			Family            string
			Stats             []string
			DimensionProfiles map[string][]string `yaml:"dimension_profiles"`
			Metrics           []struct {
				Root     string
				Profiles []string
			}
		}
		if err := yaml.Unmarshal([]byte(text), &block); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains([]string{"aws_redshift", "aws_logs"}, block.Family) {
			continue
		}
		for _, m := range block.Metrics {
			for _, p := range m.Profiles {
				keys, ok := block.DimensionProfiles[p]
				if !ok {
					t.Fatalf("unknown canonical profile %s", p)
				}
				slices.Sort(keys)
				for _, stat := range block.Stats {
					expected[block.Family+"_"+m.Root+stat] = strings.Join(keys, ",")
				}
			}
		}
	}
	if len(expected) != 60 {
		t.Fatalf("canonical subset has %d names, want 60", len(expected))
	}
	for key := range remainingExpected("remaining-selector") {
		parts := strings.Split(key, "|")
		want := "dimension_ClusterIdentifier"
		if parts[3] == "queue" {
			want += ",dimension_QueueName"
		} else if parts[3] == "loggroup" {
			want = "dimension_LogGroupName"
		}
		if got, ok := expected[parts[1]]; !ok || got != want {
			t.Fatalf("canonical %s profile=%q; want %q", parts[1], got, want)
		}
	}
}
