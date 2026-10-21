// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// Reuse the public CLI fixture, adding a second environment with independent value
// assumptions. Environment and VPC names do not distinguish CloudWatch profile series.
func bedrockProfileEnvironments(t *testing.T, changeSecond func(string) string) []byte {
	t.Helper()
	data, err := os.ReadFile("../construct/bedrock/testdata/blueprints/application-profiles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "environments:\n", 2)
	if len(parts) != 2 {
		t.Fatal("fixture has no environments section")
	}
	second := strings.Replace(parts[1], "- name: prod", "- name: shadow", 1)
	second = strings.ReplaceAll(second, "input_tokens_per_request: 22", "input_tokens_per_request: 33")
	return []byte(string(data) + changeSecond(second))
}

func runBedrockProfileBlueprint(t *testing.T, res *blueprint.Resolved, reg *core.Registry) []promrw.Series {
	t.Helper()
	mc := &coretest.MetricCapture{}
	r := runner.New(runner.Sinks{Metrics: mc}, reg, runner.Options{SendShards: 1, SendDrainDeadline: time.Second, TickTimeout: time.Second})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.DrainQueues(ctx)
	}()
	if err := r.AddBlueprint(res); err != nil {
		t.Fatalf("registered runner admission: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.RunOnce(ctx, time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return mc.All()
}

func bedrockSeriesIdentity(s promrw.Series) string {
	keys := make([]string, 0, len(s.Labels))
	for key := range s.Labels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var out strings.Builder
	fmt.Fprintf(&out, "%q", s.Name)
	for _, key := range keys {
		fmt.Fprintf(&out, "/%q=%q", key, s.Labels[key])
	}
	return out.String()
}

func TestBedrockProfileIdentityAcrossEnvironments(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(string) string
		reject bool
	}{
		{"same account region and profile", func(s string) string { return s }, true},
		{"distinct profile IDs", func(s string) string {
			return strings.NewReplacer("synsource01", "synsource02", "syndrafter01", "syndrafter02").Replace(s)
		}, false},
		{"distinct region", func(s string) string { return strings.ReplaceAll(s, "eu-west-1", "eu-central-1") }, false},
		{"distinct account", func(s string) string { return strings.ReplaceAll(s, "000000000000", "000000000001") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := runner.Catalog()
			res, err := blueprint.Load(bedrockProfileEnvironments(t, tc.change), reg)
			if tc.reject {
				if err == nil {
					// On the unfixed loader, traverse real registered Build/Tick/queue writers:
					// this proves the accepted declarations actually produce conflicting series.
					samples := runBedrockProfileBlueprint(t, res, reg)
					identities := map[string]bool{}
					for _, s := range samples {
						identities[bedrockSeriesIdentity(s)] = true
					}
					t.Fatalf("duplicate profile identity accepted by Load and registered runner: %d samples, %d unique series", len(samples), len(identities))
				}
				for _, want := range []string{"profile identity collision", "prod", "shadow", "000000000000", "eu-west-1", "synsource01"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q missing %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			samples := runBedrockProfileBlueprint(t, res, reg)
			if len(samples) != 100 {
				t.Fatalf("got %d samples, want two environments × two profiles × five bases × five stats", len(samples))
			}
			identities := map[string]bool{}
			allowed := map[string]bool{"account_id": true, "region": true, "namespace": true, "job": true, "name": true, "dimension_ModelId": true, "blueprint": true}
			for _, s := range samples {
				key := bedrockSeriesIdentity(s)
				if identities[key] {
					t.Errorf("duplicate series %s", key)
				}
				identities[key] = true
				if len(s.Labels) != len(allowed) {
					t.Errorf("unexpected profile label set: %v", s.Labels)
				}
				for key := range s.Labels {
					if !allowed[key] {
						t.Errorf("invented profile label %q", key)
					}
				}
			}
		})
	}
}

func TestBedrockProfileClaimPreservesDeclarationValidation(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, want string }{
		{"excluded profiles still claim identity", "sub_signals: [application_profiles]", "sub_signals: [agents]", "profile identity collision"},
		{"unknown field on excluded profiles", "sub_signals: [application_profiles]", "sub_signals: [agents]\n        unsupported_profile_field: true", "unsupported_profile_field"},
		{"invalid model on excluded profiles", "model: anthropic.claude-sonnet-4-6", "model: syndrafter01", "model"},
		{"malformed profile list", "application_profiles:", "application_profiles: invalid\n        ignored_profiles:", "cannot unmarshal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := bedrockProfileEnvironments(t, func(s string) string {
				if tc.name == "excluded profiles still claim identity" {
					return s
				}
				return strings.NewReplacer("synsource01", "synsource02", "syndrafter01", "syndrafter02").Replace(s)
			})
			text := strings.ReplaceAll(string(data), tc.old, tc.replacement)
			if text == string(data) {
				t.Fatal("test replacement did not change the fixture")
			}
			if tc.name == "invalid model on excluded profiles" {
				text = strings.ReplaceAll(text, "sub_signals: [application_profiles]", "sub_signals: [agents]")
			}
			reg := runner.Catalog()
			res, err := blueprint.Load([]byte(text), reg)
			if err == nil {
				r := runner.New(runner.Sinks{}, reg, runner.Options{})
				err = r.AddBlueprint(res)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("declaration accepted or wrong error: %v; want %q", err, tc.want)
			}
		})
	}
}
