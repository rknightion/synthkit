// SPDX-License-Identifier: AGPL-3.0-only

package profiles_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/telemetryspec/profiles"
)

func TestBedrockInvocationLogRegistered(t *testing.T) {
	p, ok := profiles.Lookup("bedrock_invocation_log")
	if !ok {
		t.Fatal("profile bedrock_invocation_log not registered")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("bedrock_invocation_log Validate: %v", err)
	}
	if len(p.Logs) != 1 {
		t.Fatalf("expected 1 LogSpec, got %d", len(p.Logs))
	}

	ls := p.Logs[0]

	// source must be "bedrock_invocation" — verified in logs.go streamFor call.
	if ls.Source != "bedrock_invocation" {
		t.Errorf("expected source bedrock_invocation, got %q", ls.Source)
	}

	// Verify exact body field names from bedrockInvocationBody (logs.go).
	body := ls.Body
	for _, field := range []string{
		"operation",
		"modelId",                 // ⚠ camelCase — AWS CW schema
		"input_inputTokenCount",   // ⚠ nested CW key
		"output_outputTokenCount", // ⚠ nested CW key
		"trace_id",
		"portkey_trace_id",
	} {
		if _, ok := body[field]; !ok {
			t.Errorf("body field %q missing", field)
		}
	}

	// operation must be a const_str "InvokeModel" (the only value logs.go emits).
	op := body["operation"]
	if op.ConstStr == nil || *op.ConstStr != "InvokeModel" {
		t.Errorf("operation: expected const_str InvokeModel, got kind %q", op.Kind())
	}

	// trace_id and portkey_trace_id must be high-card refs → structured metadata.
	for _, field := range []string{"trace_id", "portkey_trace_id"} {
		vm := body[field]
		if vm.Ref == "" {
			t.Errorf("%s: expected Ref model", field)
		}
		if !vm.IsHighCardRef() {
			t.Errorf("%s: expected IsHighCardRef() true", field)
		}
	}

	// modelId must be an Enum (not a ConstStr).
	if body["modelId"].Enum == nil {
		t.Error("modelId: expected Enum model")
	}
}
