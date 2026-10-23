// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"gopkg.in/yaml.v3"
)

func cwConsumerFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../e2e/fixtures/e2e-cw-consumers.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The public fixture traverses strict Load, actual registrations, Build and scoped
// queue writers. Account fields must not be multiplied by resource populations.
func TestCapturedCWConsumersPublicInventory(t *testing.T) {
	reg := runner.Catalog()
	res, err := blueprint.Load(cwConsumerFixture(t), reg)
	if err != nil {
		t.Fatal(err)
	}
	mc := &coretest.MetricCapture{}
	r := runner.New(runner.Sinks{Metrics: mc}, reg, runner.Options{SendShards: 1, SendDrainDeadline: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer r.DrainQueues(ctx)
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(ctx, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	expected := cwEmittedProfiles(t)
	bases := map[string]bool{}
	accounts := map[string]int{}
	identities := map[string]bool{}
	profiles := map[string]bool{}
	for _, s := range mc.All() {
		if !strings.HasPrefix(s.Name, "aws_lambda_") && !strings.HasPrefix(s.Name, "aws_sqs_") && !strings.HasPrefix(s.Name, "aws_dynamodb_") {
			t.Fatalf("unexpected family %s", s.Name)
		}
		base := s.Name
		found := false
		for _, suffix := range []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"} {
			if strings.HasSuffix(base, suffix) {
				base = strings.TrimSuffix(base, suffix)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("unsuffixed/withheld family %s", s.Name)
		}
		bases[s.Name] = true
		if s.Labels["blueprint"] != "e2e-cw-consumers" {
			t.Fatalf("wrong scoped selector: %v", s.Labels)
		}
		keys := []string{}
		for k := range s.Labels {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		identity, _ := yaml.Marshal(s.Labels)
		key := s.Name + string(identity)
		if identities[key] {
			t.Fatalf("duplicate series %s", key)
		}
		identities[key] = true
		common := "account_id,blueprint,job,name,namespace,region"
		dims := []string{}
		for _, k := range keys {
			if strings.HasPrefix(k, "dimension_") {
				dims = append(dims, k)
			}
		}
		nonDims := []string{}
		for _, k := range keys {
			if !strings.HasPrefix(k, "dimension_") {
				nonDims = append(nonDims, k)
			}
		}
		if strings.Join(nonDims, ",") != common {
			t.Fatalf("invented enrichment: %v", s.Labels)
		}
		profile := strings.Join(dims, ",")
		allowed, ok := expected[s.Name]
		if !ok {
			t.Fatalf("name absent from canonical emitted subset: %s", s.Name)
		}
		if _, ok := allowed[profile]; !ok {
			t.Fatalf("%s has unsupported alternative %q", s.Name, profile)
		}
		allowed[profile] = true
		profiles[profile] = true
		switch s.Labels["namespace"] {
		case "AWS/Lambda":
			account := base == "aws_lambda_claimed_account_concurrency" || base == "aws_lambda_unreserved_concurrent_executions"
			if account {
				accounts[s.Name]++
				if profile != "" || s.Labels["name"] != "global" {
					t.Fatal("function identity leaked to account", s)
				}
			} else if profile != "dimension_FunctionName" || s.Labels["name"] != "global" {
				t.Fatal("wrong Lambda alternative", s)
			}
		case "AWS/SQS":
			if profile != "dimension_QueueName" || s.Labels["name"] == "global" {
				t.Fatal("wrong SQS alternative", s)
			}
		case "AWS/DynamoDB":
			if strings.HasPrefix(base, "aws_dynamodb_account_") || strings.HasPrefix(base, "aws_dynamodb_max_provisioned_table_") {
				accounts[s.Name]++
				if profile != "" || s.Labels["name"] != "global" {
					t.Fatal("table identity leaked to account", s)
				}
			} else {
				if !slices.Contains([]string{"dimension_TableName", "dimension_Source,dimension_TableName", "dimension_Operation,dimension_TableName", "dimension_Operation,dimension_StreamLabel,dimension_TableName"}, profile) || s.Labels["name"] == "global" {
					t.Fatal("wrong DynamoDB alternative", s)
				}
				if s.Labels["dimension_StreamLabel"] != "" && s.Labels["dimension_Operation"] != "GetRecords" {
					t.Fatal("stream operation inferred", s)
				}
			}
		default:
			t.Fatal("unexpected namespace", s)
		}
	}
	for name, alternatives := range expected {
		for profile, seen := range alternatives {
			if !seen {
				t.Fatalf("missing canonical name/profile %s {%s}", name, profile)
			}
		}
	}
	if len(bases) != 175 {
		t.Fatalf("distinct final names=%d, want 35 bases x 5 stats", len(bases))
	}
	if len(accounts) != 50 {
		t.Fatalf("account stat names=%d, want (2 Lambda + 8 DynamoDB) x 5", len(accounts))
	}
	for name, n := range accounts {
		if n != 1 {
			t.Fatalf("%s emitted %d times", name, n)
		}
	}
	for _, profile := range []string{"", "dimension_FunctionName", "dimension_QueueName", "dimension_TableName", "dimension_Source,dimension_TableName", "dimension_Operation,dimension_TableName", "dimension_Operation,dimension_StreamLabel,dimension_TableName"} {
		if !profiles[profile] {
			t.Fatalf("missing alternative %s", profile)
		}
	}
}

// Expectations come from the explicit emitted sections, not the renderer's list
// or the larger positive-source tables. Each statistic must exercise every exact
// alternative; a union-filled StreamLabel/Source must not satisfy the check.
func cwEmittedProfiles(t *testing.T) map[string]map[string]bool {
	t.Helper()
	b, err := os.ReadFile("../../signals/cw.md")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
	for _, match := range regexp.MustCompile("(?s)```yaml signals\\n(.*?)\\n```").FindAllSubmatch(b, -1) {
		var block struct {
			Family            string              `yaml:"family"`
			Stats             []string            `yaml:"stats"`
			DimensionProfiles map[string][]string `yaml:"dimension_profiles"`
			Metrics           []struct {
				Root     string   `yaml:"root"`
				Profiles []string `yaml:"profiles"`
			} `yaml:"metrics"`
		}
		if err := yaml.Unmarshal(match[1], &block); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains([]string{"aws_lambda", "aws_sqs", "aws_dynamodb"}, block.Family) {
			continue
		}
		for _, m := range block.Metrics {
			for _, stat := range block.Stats {
				name := block.Family + "_" + m.Root + stat
				out[name] = map[string]bool{}
				for _, p := range m.Profiles {
					keys, ok := block.DimensionProfiles[p]
					if !ok {
						t.Fatalf("unknown canonical profile %s", p)
					}
					keys = slices.Clone(keys)
					slices.Sort(keys)
					out[name][strings.Join(keys, ",")] = false
				}
			}
		}
	}
	if len(out) != 175 {
		t.Fatalf("canonical initial name set=%d, want 175", len(out))
	}
	return out
}

// Isolate each optional block without importing any component model into the loader.
func cwConsumerDecl(t *testing.T, kind string) blueprint.Decl {
	t.Helper()
	var d blueprint.Decl
	if err := yaml.Unmarshal(cwConsumerFixture(t), &d); err != nil {
		t.Fatal(err)
	}
	c := d.Environments[0].Cloud
	if kind != "lambda" {
		c.Lambda = yaml.Node{}
	}
	if kind != "sqs" {
		c.SQS = yaml.Node{}
	}
	if kind != "dynamodb" {
		c.DynamoDB = yaml.Node{}
	}
	return d
}
func cwConsumerLoad(t *testing.T, d blueprint.Decl) (*blueprint.Resolved, error) {
	t.Helper()
	b, err := yaml.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	// Zero raw nodes mean absent authoring blocks, not explicit YAML null blocks.
	// Round-tripping the typed Decl must preserve that distinction in this fixture.
	var document map[string]any
	if err := yaml.Unmarshal(b, &document); err != nil {
		t.Fatal(err)
	}
	for _, rawEnv := range document["environments"].([]any) {
		cloud := rawEnv.(map[string]any)["cloud"].(map[string]any)
		for key, value := range cloud {
			if value == nil {
				delete(cloud, key)
			}
		}
	}
	b, err = yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return blueprint.Load(b, runner.Catalog())
}

func TestCapturedCWConsumerAdmission(t *testing.T) {
	// Reusing an account in a different blueprint is legal: writer scoping, not
	// environment naming, distinguishes these families. Do not broaden the guard.
	first := cwConsumerDecl(t, "lambda")
	second := first
	second.Name, second.Label = "independent-consumers", "independent-consumers"
	a, err := cwConsumerLoad(t, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cwConsumerLoad(t, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := blueprint.ValidateSet([]*blueprint.Resolved{a, b}); err != nil {
		t.Fatal(err)
	}
	r := runner.New(runner.Sinks{}, runner.Catalog(), runner.Options{})
	for _, res := range []*blueprint.Resolved{a, b} {
		if err := r.AddBlueprint(res); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"lambda", "sqs", "dynamodb"} {
		t.Run(kind, func(t *testing.T) {
			d := cwConsumerDecl(t, kind)
			d.Environments[0].Cloud.CloudWatchExport = "otlp"
			res, err := cwConsumerLoad(t, d)
			if err != nil {
				t.Fatal(err)
			}
			r := runner.New(runner.Sinks{}, runner.Catalog(), runner.Options{})
			if err := r.AddBlueprint(res); err == nil || !strings.Contains(err.Error(), "remote_write") {
				t.Fatalf("native admission must fail explicitly: %v", err)
			}

			for _, tc := range []struct{ name, old, replacement, want string }{
				{"same account region", "us-east-1", "us-east-1", "declaration collision"},
				{"different region", "us-east-1", "us-west-2", ""},
				{"different account", "111122223333", "444455556666", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					d := cwConsumerDecl(t, kind)
					b, err := yaml.Marshal(d.Environments[0])
					if err != nil {
						t.Fatal(err)
					}
					var second blueprint.EnvDecl
					text := strings.NewReplacer(tc.old, tc.replacement, "fixture-", "shadow-", "declared-", "shadow-declared-").Replace(string(b))
					if err := yaml.Unmarshal([]byte(text), &second); err != nil {
						t.Fatal(err)
					}
					second.Name = "shadow"
					d.Environments = append(d.Environments, second)
					res, err := cwConsumerLoad(t, d)
					if tc.want == "" {
						if err != nil {
							t.Fatal(err)
						}
						r := runner.New(runner.Sinks{}, runner.Catalog(), runner.Options{})
						if err := r.AddBlueprint(res); err != nil {
							t.Fatal(err)
						}
					} else if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("duplicate declaration admitted/wrong error: %v", err)
					}
				})
			}
		})
	}
	for _, tc := range []struct{ old, replacement, want string }{
		{"reserved_concurrency: 10", "unsupported_lambda_field: true", "unsupported_lambda_field"},
		{"message_bytes: 128", "fair_queue: true", "fair_queue"},
		{"provisioned_reads: 10", "unsupported_dynamodb_field: true", "unsupported_dynamodb_field"},
	} {
		b := cwConsumerFixture(t)
		altered := strings.Replace(string(b), tc.old, tc.replacement, 1)
		if altered == string(b) {
			t.Fatal("fixture mutation missing")
		}
		if _, err := blueprint.Load([]byte(altered), runner.Catalog()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("unknown field admitted/wrong error: %v", err)
		}
	}
}
