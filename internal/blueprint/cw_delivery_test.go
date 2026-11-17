// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func deliveryFixture(t *testing.T) string {
	t.Helper()
	b, e := os.ReadFile("../../e2e/fixtures/e2e-cw-delivery.yaml")
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func deliveryNames() map[string]bool {
	names := map[string]bool{}
	for k := range remainingExpected("remaining-selector") {
		names[strings.Split(k, "|")[1]] = true
	}
	for _, base := range []string{"delivery_errors", "delivery_throttling", "forwarded_log_events", "forwarded_bytes"} {
		for _, s := range []string{"sum", "average", "maximum", "minimum", "sample_count"} {
			names["aws_logs_"+base+"_"+s] = true
		}
	}
	return names
}

// Public loader + real registry/runner/scoped writer: exact values and P21 labels,
// including a zero-intake group's independently authored zero observations.
func TestLogsDeliveryPublicValues(t *testing.T) {
	reg := runner.Catalog()
	res, e := blueprint.Load([]byte(deliveryFixture(t)), reg)
	if e != nil {
		t.Fatal(e)
	}
	mc := &coretest.MetricCapture{}
	r := runner.New(runner.Sinks{Metrics: mc}, reg, runner.Options{SendShards: 1, SendDrainDeadline: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer r.DrainQueues(ctx)
	if e = r.AddBlueprint(res); e != nil {
		t.Fatal(e)
	}
	if e = r.RunOnce(ctx, time.Unix(1800000000, 0)); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, s := range mc.All() {
		var value float64
		var base string
		for b, v := range map[string]float64{"delivery_errors": 3, "delivery_throttling": 5, "forwarded_log_events": 7, "forwarded_bytes": 11} {
			if strings.HasPrefix(s.Name, "aws_logs_"+b+"_") {
				value, base = v, b
				break
			}
		}
		if base == "" {
			continue
		}
		group := s.Labels["dimension_LogGroupName"]
		filter := "fixture-forward"
		if group == "/fixture/idle" {
			value = 0
			filter = "fixture-zero"
		} else if group != "/fixture/intake" {
			t.Fatal(s)
		}
		if strings.HasSuffix(s.Name, "_sample_count") {
			value = 1
		}
		labels := map[string]string{"account_id": "111122223333", "region": "us-east-1", "namespace": "AWS/Logs", "job": "cloud/aws/logs", "name": "global", "blueprint": "delivery-selector", "dimension_LogGroupName": group, "dimension_FilterName": filter, "dimension_DestinationType": "Lambda"}
		key := group + "|" + s.Name
		if seen[key] || s.Value != value || s.Kind != promrw.KindGauge || !reflect.DeepEqual(s.Labels, labels) {
			t.Fatalf("unexpected delivery %+v", s)
		}
		seen[key] = true
	}
	if len(seen) != 40 {
		t.Fatalf("got %d delivery series, want 40", len(seen))
	}
}

// Build the real public command, with a credential-free allowlist and isolated
// state. No mock parser or renderer inventory stands in for CLI acceptance.
func TestLogsDeliveryCLI(t *testing.T) {
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(t.TempDir(), "synthkit")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/synthkit")
	build.Dir = root
	if b, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v\n%s", e, b)
	}
	run := func(text string, args ...string) ([]byte, error) {
		t.Helper()
		dir := t.TempDir()
		fixtures := filepath.Join(dir, "fixtures")
		if e := os.Mkdir(fixtures, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(fixtures, "delivery.yaml"), []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, append([]string{"-env", "/dev/null"}, args...)...)
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "DRY_RUN=true", "SELFOBS_ENABLED=false", "BLUEPRINTS=" + fixtures, "BLUEPRINT_NAMES=e2e-cw-delivery", "BLUEPRINT_DATA_DIR=" + filepath.Join(dir, "data"), "CONFIG_SNAPSHOT_PATH=" + filepath.Join(dir, "state.json"), "JSON_HTTP_ADDR=127.0.0.1:0"}
		return cmd.CombinedOutput()
	}
	t.Run("legacy inventory", func(t *testing.T) {
		b, e := run(strings.Replace(remainingFixture(t), "name: e2e-cw-remaining", "name: e2e-cw-delivery", 1), "-once", "-inventory-json")
		if e != nil {
			t.Fatalf("legacy CLI: %v\n%s", e, b)
		}
		at := strings.Index(string(b), "{\n")
		if at < 0 {
			t.Fatalf("missing JSON: %s", b)
		}
		var inv inventory.Schema
		if e := json.Unmarshal(b[at:], &inv); e != nil {
			t.Fatal(e)
		}
		names := map[string]bool{}
		for _, m := range inv.Metrics {
			names[m.Name] = true
		}
		want := map[string]bool{}
		for k := range remainingExpected("remaining-selector") {
			want[strings.Split(k, "|")[1]] = true
		}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("legacy inventory changed: %v", names)
		}
		t.Logf("actual legacy CLI inventory: %d names; all 20 new delivery names absent", len(names))
	})
	fixture := deliveryFixture(t)
	for _, args := range [][]string{{"-validate"}, {"-once", "-dump"}, {"-once", "-inventory-json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			b, e := run(fixture, args...)
			if e != nil {
				t.Fatalf("CLI: %v\n%s", e, b)
			}
			// CLI logs go to stderr; inventory JSON is the trailing JSON document.
			if args[len(args)-1] == "-inventory-json" {
				at := strings.Index(string(b), "{\n")
				if at < 0 {
					t.Fatalf("missing JSON: %s", b)
				}
				var inv inventory.Schema
				if e := json.Unmarshal(b[at:], &inv); e != nil {
					t.Fatalf("%v: %s", e, b)
				}
				names := map[string]bool{}
				for _, m := range inv.Metrics {
					names[m.Name] = true
				}
				if !reflect.DeepEqual(names, deliveryNames()) {
					t.Fatalf("got %d names, want exact 80: %v", len(names), names)
				}
				t.Logf("actual CLI combined inventory: %d/80 names", len(names))
			} else if args[len(args)-1] == "-dump" {
				for name := range deliveryNames() {
					if !strings.Contains(string(b), name) {
						t.Fatalf("missing %s", name)
					}
				}
			}
		})
	}
	for _, tc := range []struct{ old, new, want string }{
		{"destination_type: Lambda", "destination_type: Firehose", "destination_type must be Lambda"},
		{"forwarded_bytes: 11", "forwarded_bytes: 1.5", "finite whole"},
		{"delivery_errors: 3, ", "", "all four period observations required"},
		{"delivery_errors: 3", "delivery_errors: .nan", "finite whole"},
		{"forwarded_log_events: 7", "forwarded_log_events: -1", "finite whole"},
		{"name: fixture-forward", "name: ' padded '", "unique unpadded filter name"},
		{"forwarded_bytes: 11", "forwarded_bytes: 11, compression_ratio: 0.5", "field compression_ratio"},
	} {
		text := strings.Replace(fixture, tc.old, tc.new, 1)
		if text == fixture {
			t.Fatal("mutation not applied", tc.old)
		}
		arg := "-once"
		if strings.HasPrefix(tc.want, "field ") {
			arg = "-validate"
		}
		b, e := run(text, arg)
		if e == nil || !strings.Contains(string(b), tc.want) {
			t.Fatalf("expected %q, got %v\n%s", tc.want, e, b)
		}
	}
	t.Run("native OTLP refusal", func(t *testing.T) {
		var doc map[string]any
		if e := yaml.Unmarshal([]byte(fixture), &doc); e != nil {
			t.Fatal(e)
		}
		cloud := doc["environments"].([]any)[0].(map[string]any)["cloud"].(map[string]any)
		// Keep the delivery-enabled Logs declaration, but remove Redshift so
		// its earlier Build refusal cannot mask a missing Logs native guard.
		delete(cloud, "redshift")
		cloud["cloudwatch_export"] = "otlp"
		text, e := yaml.Marshal(doc)
		if e != nil {
			t.Fatal(e)
		}
		b, e := run(string(text), "-once")
		if e == nil || !strings.Contains(string(b), "cloudwatch_logs:") || !strings.Contains(string(b), "remote_write") {
			t.Fatalf("expected cloudwatch_logs-specific remote_write refusal, got %v\n%s", e, b)
		}
	})
}
