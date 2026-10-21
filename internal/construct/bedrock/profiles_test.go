// SPDX-License-Identifier: AGPL-3.0-only

package bedrock

import (
	"bytes"
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"gopkg.in/yaml.v3"
)

// Decode the same public YAML node consumed by the registry without importing the loader.
func profileFixtureConfig(t *testing.T) *Config {
	t.Helper()
	data, err := os.ReadFile("testdata/blueprints/application-profiles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Environments []struct {
			Cloud struct {
				Bedrock yaml.Node `yaml:"bedrock"`
			} `yaml:"cloud"`
		} `yaml:"environments"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	data, err = yaml.Marshal(doc.Environments[0].Cloud.Bedrock)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}

func TestApplicationProfilesPublicFixture(t *testing.T) {
	cfg := profileFixtureConfig(t)
	c, err := Build(cfg, &fixture.Set{Cloud: &fixture.Cloud{AccountID: "000000000000", Region: "eu-west-1"}})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"anthropic.claude-haiku-4-5-20251001-v1:0", "anthropic.claude-sonnet-4-6"} {
		p := cfg.ApplicationProfiles[i]
		if p.Provider != "bedrock" || p.Model != want {
			t.Fatalf("pricing identity rewritten to profile ID: %+v", p)
		}
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	// Repeated periods must replace, not accumulate, the five per-period gauges.
	for range 2 {
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, coretest.World(mc, nil, nil)); err != nil {
			t.Fatal(err)
		}
		wantMeans := map[string]map[string]float64{
			"synsource01":  {"invocations": 1, "input_token_count": 22, "output_token_count": 4, "invocation_latency": 578, "estimated_tpmquota_usage": 42},
			"syndrafter01": {"invocations": 1, "input_token_count": 22, "output_token_count": 5, "invocation_latency": 675, "estimated_tpmquota_usage": 47},
		}
		if len(mc.All()) != 50 {
			t.Fatalf("got %d series, want only five bases × five stats × two profiles", len(mc.All()))
		}
		seen := map[string]bool{}
		for _, s := range mc.All() {
			id := s.Labels["dimension_ModelId"]
			means, ok := wantMeans[id]
			if !ok {
				t.Fatalf("unexpected model dimension %q", id)
			}
			if s.Kind != promrw.KindGauge {
				t.Errorf("%s is not a gauge", s.Name)
			}
			wantLabels := map[string]string{"account_id": "000000000000", "region": "eu-west-1", "namespace": "AWS/Bedrock", "job": "cloud/aws/bedrock", "name": "global", "dimension_ModelId": id}
			if len(s.Labels) != len(wantLabels) {
				t.Errorf("unexpected labels: %v", s.Labels)
			}
			for k, v := range wantLabels {
				if s.Labels[k] != v {
					t.Errorf("%s: %s=%q want %q", s.Name, k, s.Labels[k], v)
				}
			}
			matched := false
			for base, mean := range means {
				for suffix, want := range map[string]float64{"_sum": mean * 2, "_average": mean, "_minimum": mean, "_maximum": mean, "_sample_count": 2} {
					if s.Name == "aws_bedrock_"+base+suffix {
						matched = true
						key := id + "/" + s.Name
						if seen[key] {
							t.Errorf("duplicate %s", key)
						}
						seen[key] = true
						if s.Value != want {
							t.Errorf("%s/%s=%v want %v", id, s.Name, s.Value, want)
						}
					}
				}
			}
			if !matched {
				t.Errorf("uncaptured profile metric %s", s.Name)
			}
		}
		now = now.Add(time.Minute)
	}
}

func TestApplicationProfilesAdmission(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, mode, want string }{
		{name: "otlp unproven", mode: "otlp", want: "PENDING"},
		{name: "duplicate id", old: "id: syndrafter01", replacement: "id: synsource01", want: "duplicate"},
		{name: "arn is not id", old: "id: synsource01", replacement: "id: arn:aws:bedrock:eu-west-1:000000000000:application-inference-profile/synsource01", want: "id"},
		{name: "missing agent", old: "agent: source", replacement: "agent: ''", want: "agent"},
		{name: "profile not pricing model", old: "model: anthropic.claude-sonnet-4-6", replacement: "model: syndrafter01", want: "model"},
		{name: "wrong provider", old: "provider: bedrock", replacement: "provider: openai", want: "provider"},
		{name: "wrong platform model", old: "model: anthropic.claude-sonnet-4-6", replacement: "model: gpt-4o", want: "model"},
		{name: "missing rate", old: "requests_per_min: 2", replacement: "requests_per_min: 0", want: "requests_per_min"},
		{name: "nonfinite rate", old: "requests_per_min: 2", replacement: "requests_per_min: .inf", want: "requests_per_min"},
		{name: "invalid tokens", old: "input_tokens_per_request: 22", replacement: "input_tokens_per_request: -1", want: "input_tokens_per_request"},
		{name: "missing quota", old: "estimated_tpm_quota_usage_per_request: 42", replacement: "estimated_tpm_quota_usage_per_request: 0", want: "estimated_tpm_quota_usage_per_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := profileFixtureConfig(t)
			data, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Replace(string(data), tc.old, tc.replacement, 1)
			if tc.old != "" && text == string(data) {
				t.Fatalf("test replacement not found: %s", tc.old)
			}
			if err := yaml.Unmarshal([]byte(text), cfg); err != nil {
				t.Fatal(err)
			}
			_, err = Build(cfg, &fixture.Set{Cloud: &fixture.Cloud{CloudWatchExport: tc.mode}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Build error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestApplicationProfilesSubSignalGate(t *testing.T) {
	cfg := profileFixtureConfig(t)
	cfg.SubSignals = []string{"agents"}
	c, err := Build(cfg, &fixture.Set{Cloud: &fixture.Cloud{}})
	if err != nil {
		t.Fatal(err)
	}
	mc := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), coretest.World(mc, nil, nil)); err != nil {
		t.Fatal(err)
	}
	if len(mc.Find("aws_bedrock_agents_invocation_count_sum")) == 0 {
		t.Fatal("legacy Agents gate emitted nothing")
	}
	for _, s := range mc.All() {
		if s.Labels["dimension_ModelId"] == "synsource01" || s.Labels["dimension_ModelId"] == "syndrafter01" {
			t.Fatalf("excluded profile leaked into %s", s.Name)
		}
	}
	// Exclusion is not a bypass of the OTLP admission barrier.
	if _, err := Build(cfg, &fixture.Set{Cloud: &fixture.Cloud{CloudWatchExport: "otlp"}}); err == nil || !strings.Contains(err.Error(), "PENDING") {
		t.Fatalf("excluded profile bypassed OTLP gate: %v", err)
	}
}

func TestApplicationProfilesLowVolumeAndIdle(t *testing.T) {
	cfg := profileFixtureConfig(t)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// A realistic low rate must not be floored up to one request per tick.
	text := strings.ReplaceAll(string(data), "requests_per_min: 2", "requests_per_min: 0.25")
	if err := yaml.Unmarshal([]byte(text), cfg); err != nil {
		t.Fatal(err)
	}
	c, err := Build(cfg, &fixture.Set{Cloud: &fixture.Cloud{}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	total := 0.0
	active, idle := 0, 0
	for i := 0; i < 8; i++ {
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now.Add(time.Duration(i)*time.Minute), coretest.World(mc, nil, nil)); err != nil {
			t.Fatal(err)
		}
		if len(mc.All()) == 0 {
			idle++
			continue
		}
		active++
		for _, s := range mc.Find("aws_bedrock_invocations_sum") {
			total += s.Value
			if s.Value != math.Trunc(s.Value) {
				t.Errorf("fractional invocation count %v", s.Value)
			}
		}
	}
	if total != 4 || active != 2 || idle != 6 {
		t.Fatalf("total=%v active=%d idle=%d; want 4 requests across two profiles, two active periods, six idle periods", total, active, idle)
	}
}
