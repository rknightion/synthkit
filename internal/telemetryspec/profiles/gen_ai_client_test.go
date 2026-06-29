// SPDX-License-Identifier: AGPL-3.0-only

package profiles

import "testing"

func TestGenAIClientProfileRegistered(t *testing.T) {
	p, ok := Lookup("gen_ai_client")
	if !ok {
		t.Fatal("gen_ai_client profile not registered")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("gen_ai_client profile invalid: %v", err)
	}
}

func TestGenAIClientProfileShape(t *testing.T) {
	p, _ := Lookup("gen_ai_client")

	// Four metrics (2 core + 2 streaming chunk histograms) + one span.
	if len(p.Metrics) != 4 {
		t.Fatalf("expected 4 metrics, got %d", len(p.Metrics))
	}
	if len(p.Spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(p.Spans))
	}
	if len(p.Logs) != 0 {
		t.Errorf("expected 0 logs, got %d", len(p.Logs))
	}

	// Check metric names (must be EXACT — from internal/genai constants).
	wantMetrics := []string{
		"gen_ai_client_token_usage",
		"gen_ai_client_operation_duration_seconds",
		"gen_ai_client_operation_time_to_first_chunk_seconds",
		"gen_ai_client_operation_time_per_output_chunk_seconds",
	}
	for i, want := range wantMetrics {
		if p.Metrics[i].Name != want {
			t.Errorf("metrics[%d]: want name=%q, got %q", i, want, p.Metrics[i].Name)
		}
		if p.Metrics[i].Instrument != "histogram" {
			t.Errorf("metrics[%d] %q: want histogram, got %q", i, want, p.Metrics[i].Instrument)
		}
		if len(p.Metrics[i].Buckets) == 0 {
			t.Errorf("metrics[%d] %q: missing buckets", i, want)
		}
		if p.Metrics[i].LEStyle != "dotzero" {
			t.Errorf("metrics[%d] %q: want le_style=dotzero, got %q", i, want, p.Metrics[i].LEStyle)
		}
	}

	// token_usage must have gen_ai_token_type label with input|output enum.
	tokenUsage := p.Metrics[0]
	if _, ok := tokenUsage.Labels["gen_ai_token_type"]; !ok {
		t.Error("token_usage: missing label gen_ai_token_type")
	}

	// token_usage must have gen_ai_request_model with a bounded enum (§6.1 G9). response.model is
	// span-only (opt-in on the metric; omitted to avoid the request×response cardinality blow-up).
	if _, ok := tokenUsage.Labels["gen_ai_response_model"]; ok {
		t.Error("token_usage: gen_ai_response_model must NOT be a metric label (span-only)")
	}
	for _, lk := range []string{"gen_ai_request_model"} {
		vm, ok := tokenUsage.Labels[lk]
		if !ok {
			t.Errorf("token_usage: missing label %q", lk)
			continue
		}
		if len(vm.Enum) == 0 {
			t.Errorf("token_usage label %q: empty enum — must be bounded (source: internal/genai/models.go)", lk)
		}
		for _, e := range vm.Enum {
			if e.Value == "" {
				t.Errorf("token_usage label %q: blank enum value", lk)
			}
		}
	}

	// operation_duration must have gen_ai_operation_name and gen_ai_provider_name labels.
	opDur := p.Metrics[1]
	for _, lk := range []string{"gen_ai_operation_name", "gen_ai_provider_name", "error_type"} {
		if _, ok := opDur.Labels[lk]; !ok {
			t.Errorf("operation_duration: missing label %q", lk)
		}
	}

	// operation_duration must also carry gen_ai_request_model (§6.1 G9). response.model is span-only.
	if _, ok := opDur.Labels["gen_ai_response_model"]; ok {
		t.Error("operation_duration: gen_ai_response_model must NOT be a metric label (span-only)")
	}
	for _, lk := range []string{"gen_ai_request_model"} {
		vm, ok := opDur.Labels[lk]
		if !ok {
			t.Errorf("operation_duration: missing label %q", lk)
			continue
		}
		if len(vm.Enum) == 0 {
			t.Errorf("operation_duration label %q: empty enum — must be bounded (source: internal/genai/models.go)", lk)
		}
		for _, e := range vm.Enum {
			if e.Value == "" {
				t.Errorf("operation_duration label %q: blank enum value", lk)
			}
		}
	}

	// Span checks.
	s := p.Spans[0]
	if s.Kind != "client" {
		t.Errorf("span kind: want client, got %q", s.Kind)
	}

	// Required gen_ai span attributes (exact keys from internal/genai constants).
	requiredAttrs := []string{
		"gen_ai.operation.name",
		"gen_ai.provider.name",
		"gen_ai.request.model",
		"gen_ai.response.model",
		"gen_ai.usage.input_tokens",
		"gen_ai.usage.output_tokens",
		"gen_ai.conversation.id",
	}
	for _, k := range requiredAttrs {
		if _, ok := s.Attributes[k]; !ok {
			t.Errorf("span: missing attribute %q", k)
		}
	}

	// gen_ai.provider.name must be Ref:"provider".
	if s.Attributes["gen_ai.provider.name"].Ref != "provider" {
		t.Errorf("span gen_ai.provider.name: want Ref=provider, got %q", s.Attributes["gen_ai.provider.name"].Ref)
	}

	// gen_ai.request.model must be Ref:"model".
	if s.Attributes["gen_ai.request.model"].Ref != "model" {
		t.Errorf("span gen_ai.request.model: want Ref=model, got %q", s.Attributes["gen_ai.request.model"].Ref)
	}
}

func TestGenAIClientBucketsStrictlyAscending(t *testing.T) {
	p, _ := Lookup("gen_ai_client")
	for i, m := range p.Metrics {
		for j := 1; j < len(m.Buckets); j++ {
			if m.Buckets[j] <= m.Buckets[j-1] {
				t.Errorf("metric[%d] %q: buckets not strictly ascending at index %d (%v <= %v)",
					i, m.Name, j, m.Buckets[j], m.Buckets[j-1])
			}
		}
	}
}
