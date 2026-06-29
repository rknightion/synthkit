// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAgenticFlowDecode asserts the agentic_flow: block on a service node strict-decodes into
// Config.Services[i].AgenticFlow (workflow + per-agent tool pools + omit_chat). Strict decoding
// (KnownFields) mirrors the blueprint loader so an unknown-key regression surfaces here.
func TestAgenticFlowDecode(t *testing.T) {
	const src = `
services:
  - name: backend
    type: web
    agentic_flow:
      workflow: assistant-graph
      agents:
        - { name: planner, tools: [doc_search, retriever] }
        - { name: checker, tools: [validator] }
models:
  - { model: gpt-4o, provider: azure-openai }
`
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("strict decode failed: %v", err)
	}
	if len(cfg.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(cfg.Services))
	}
	af := cfg.Services[0].AgenticFlow
	if af == nil {
		t.Fatal("AgenticFlow is nil — agentic_flow block did not decode")
	}
	if af.Workflow != "assistant-graph" {
		t.Errorf("Workflow = %q, want assistant-graph", af.Workflow)
	}
	if af.OmitChat {
		t.Error("OmitChat = true, want false (default)")
	}
	if len(af.Agents) != 2 {
		t.Fatalf("agents = %d, want 2", len(af.Agents))
	}
	if af.Agents[0].Name != "planner" || len(af.Agents[0].Tools) != 2 {
		t.Errorf("agent[0] = %+v, want planner with 2 tools", af.Agents[0])
	}
	if af.Agents[1].Name != "checker" || len(af.Agents[1].Tools) != 1 {
		t.Errorf("agent[1] = %+v, want checker with 1 tool", af.Agents[1])
	}

	// omit_chat decodes true where set.
	const src2 = `
services:
  - name: backend
    type: web
    agentic_flow:
      workflow: wf
      omit_chat: true
      agents:
        - { name: a, tools: [t] }
`
	var cfg2 Config
	dec2 := yaml.NewDecoder(strings.NewReader(src2))
	dec2.KnownFields(true)
	if err := dec2.Decode(&cfg2); err != nil {
		t.Fatalf("decode src2: %v", err)
	}
	if !cfg2.Services[0].AgenticFlow.OmitChat {
		t.Error("OmitChat = false, want true")
	}
}
