// SPDX-License-Identifier: AGPL-3.0-only

package bedrock

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func TestBedrock_EmitsCoreFamily(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Representative core series — all five stat suffixes emitted.
	for _, name := range []string{
		"aws_bedrock_invocations_sum",
		"aws_bedrock_invocations_average",
		"aws_bedrock_invocations_maximum",
		"aws_bedrock_invocations_minimum",
		"aws_bedrock_invocations_sample_count",
		"aws_bedrock_invocation_latency_average",
	} {
		if got := mc.Find(name); len(got) == 0 {
			t.Errorf("missing %s", name)
		}
	}

	// ⚠ tpmquota NOT tpm_quota — the mangling trap from signals/bedrock.md.
	if got := mc.Find("aws_bedrock_estimated_tpmquota_usage_average"); len(got) == 0 {
		t.Error("missing aws_bedrock_estimated_tpmquota_usage_average (check tpmquota spelling)")
	}
	// Verify the wrong spelling does NOT appear.
	for _, s := range mc.All() {
		if strings.Contains(s.Name, "tpm_quota") {
			t.Errorf("mangling trap: got tpm_quota in %q (must be tpmquota)", s.Name)
		}
	}

	// namespace label on core series.
	for _, s := range mc.Find("aws_bedrock_invocations_average") {
		if s.Labels["namespace"] != "AWS/Bedrock" {
			t.Errorf("namespace=%q want AWS/Bedrock", s.Labels["namespace"])
		}
	}

	// dimension_ModelId is present on per-model series.
	modelIDs := map[string]bool{}
	for _, s := range mc.Find("aws_bedrock_invocations_average") {
		modelIDs[s.Labels["dimension_ModelId"]] = true
	}
	if len(modelIDs) == 0 {
		t.Fatal("want dimension_ModelId on invocations_average, got none")
	}
	// Both default models should appear.
	for _, want := range defaultModels {
		if !modelIDs[want] {
			t.Errorf("missing dimension_ModelId=%q on invocations_average", want)
		}
	}
}

func TestBedrock_EmitsAgentsFamily(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := mc.Find("aws_bedrock_agents_invocation_count_sum"); len(got) == 0 {
		t.Error("missing aws_bedrock_agents_invocation_count_sum")
	}
	// namespace on agents series.
	for _, s := range mc.Find("aws_bedrock_agents_invocation_count_sum") {
		if s.Labels["namespace"] != "AWS/Bedrock/Agents" {
			t.Errorf("agents namespace=%q want AWS/Bedrock/Agents", s.Labels["namespace"])
		}
	}
}

func TestBedrock_EmitsGuardrailsFamily(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := mc.Find("aws_bedrock_guardrails_invocations_intervened_sum"); len(got) == 0 {
		t.Error("missing aws_bedrock_guardrails_invocations_intervened_sum")
	}
	// namespace on guardrails series.
	for _, s := range mc.Find("aws_bedrock_guardrails_invocations_sum") {
		if s.Labels["namespace"] != "AWS/Bedrock/Guardrails" {
			t.Errorf("guardrails namespace=%q want AWS/Bedrock/Guardrails", s.Labels["namespace"])
		}
	}
}

func TestBedrock_EmitsLogDeliveryFamily(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Log-delivery six use dim "Across all model IDs" (one series each).
	if got := mc.Find("aws_bedrock_model_invocation_logs_cloud_watch_delivery_success_sum"); len(got) == 0 {
		t.Error("missing aws_bedrock_model_invocation_logs_cloud_watch_delivery_success_sum")
	}
	for _, s := range mc.Find("aws_bedrock_model_invocation_logs_cloud_watch_delivery_success_sum") {
		if s.Labels["dimension_ModelId"] != "Across all model IDs" {
			t.Errorf("log-delivery dim_ModelId=%q want 'Across all model IDs'", s.Labels["dimension_ModelId"])
		}
	}
}

func TestBedrock_SubSignalsGate(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	// Emit only agents; core/guardrails/logs should be absent.
	c, err := Build(&Config{SubSignals: []string{"agents"}}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := mc.Find("aws_bedrock_agents_invocation_count_sum"); len(got) == 0 {
		t.Error("missing aws_bedrock_agents_invocation_count_sum when agents sub_signal enabled")
	}
	if got := mc.Find("aws_bedrock_invocations_sum"); len(got) != 0 {
		t.Error("unexpected aws_bedrock_invocations_sum when only agents sub_signal enabled")
	}
	if got := mc.Find("aws_bedrock_guardrails_invocations_sum"); len(got) != 0 {
		t.Error("unexpected aws_bedrock_guardrails_invocations_sum when only agents sub_signal enabled")
	}
}

func TestBedrock_BuildRequiresCloud(t *testing.T) {
	if _, err := Build(&Config{}, nil); err == nil {
		t.Fatal("Build with nil fixture.Set should return error")
	}
	if _, err := Build(&Config{}, &fixture.Set{}); err == nil {
		t.Fatal("Build with nil fixture.Cloud should return error")
	}
}

// seriesValueByModel returns the _average value for the given metric name and dimension_ModelId.
func seriesValueByModel(mc *coretest.MetricCapture, metricName, modelID string) float64 {
	for _, s := range mc.Find(metricName) {
		if s.Labels["dimension_ModelId"] == modelID {
			return s.Value
		}
	}
	return -1
}

func TestModelVolumeWeightDifferentiation(t *testing.T) {
	// nova-micro weight ~6.0, opus weight ~0.6 — ratio ~10x.
	// After weighting the invocation magnitudes should be meaningfully different.
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	models := []string{"amazon.nova-micro-v1:0", "anthropic.claude-opus-4-6-v1"}
	c, err := Build(&Config{Models: models}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	microInv := seriesValueByModel(mc, "aws_bedrock_invocations_average", "amazon.nova-micro-v1:0")
	opusInv := seriesValueByModel(mc, "aws_bedrock_invocations_average", "anthropic.claude-opus-4-6-v1")

	if microInv < 0 {
		t.Fatal("missing invocations_average for amazon.nova-micro-v1:0")
	}
	if opusInv < 0 {
		t.Fatal("missing invocations_average for anthropic.claude-opus-4-6-v1")
	}

	// nova-micro (weight 6.0) should be > 2x opus (weight 0.6).
	if microInv <= 2*opusInv {
		t.Errorf("nova-micro invocations %v should be >2x opus invocations %v (volume weights differ ~10x)", microInv, opusInv)
	}
}
