// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"gopkg.in/yaml.v3"
)

func hostConfig(t *testing.T) *Config {
	t.Helper()
	var cfg Config
	if err := yaml.Unmarshal([]byte("mode: host\nhost_name: example-host\nservice_name: example-worker\nsource: example-agent\ndeployment_environment: example\nincrements_per_minute: 6\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}

func TestHostSelectionMatchesIndependentEnvelope(t *testing.T) {
	c, err := Build(hostConfig(t), &fixture.Set{})
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	first := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for _, now := range []time.Time{first, first.Add(time.Minute)} {
		if err := c.Tick(context.Background(), now, &core.World{OTLPMetrics: capture}); err != nil {
			t.Fatal(err)
		}
	}
	if len(capture.resources) != 2 {
		t.Fatalf("host resources=%d, want two example-only ticks", len(capture.resources))
	}
	raw, err := os.ReadFile("../../../e2e/acceptance/datadog-host-native-envelope-2026-09-09.json")
	if err != nil {
		t.Fatal(err)
	}
	var artifact capturedArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	var wants []capturedEnvelope
	for _, e := range artifact.MetricEnvelopes {
		if e.Name == exampleMetricIncrement {
			wants = append(wants, e)
		}
	}
	if len(wants) != 1 {
		t.Fatalf("captured host example shapes=%d", len(wants))
	}
	for _, r := range capture.resources {
		if err := matchesCapturedEnvelope(r, wants[0]); err != nil {
			t.Fatal(err)
		}
	}
	if got := capture.resources[1].Metrics[0].Numbers[0].Value; got != 6 {
		t.Fatalf("one-minute value=%v, want declared 6", got)
	}
	r := capture.resources[1]
	r.Metrics[0].Numbers[0].Attrs = map[string]any{"deployment.environment.name": r.Attrs["deployment.environment.name"]}
	delete(r.Attrs, "deployment.environment.name")
	if err := matchesCapturedEnvelope(r, wants[0]); err == nil {
		t.Fatal("host placement swap was accepted")
	}
}

func TestHostSelectionRejectsClusterAndMissingEnvironment(t *testing.T) {
	cfg := hostConfig(t)
	if _, err := Build(cfg, &fixture.Set{Cluster: coretest.Cluster()}); err == nil {
		t.Fatal("host mode accepted a Kubernetes fixture")
	}
	if err := yaml.Unmarshal([]byte("deployment_environment: ''\n"), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, &fixture.Set{}); err == nil {
		t.Fatal("host mode accepted missing observed environment attribute")
	}
}
