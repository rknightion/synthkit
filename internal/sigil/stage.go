// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import "context"

// GenerationStage identifies the generation at a stage boundary. All IDs come
// from the existing ledger/conversation builder; a stage must never mint them.
// BeforeToolResult identifies the producing generation, not a future call that
// has not yet consumed the result. Model is the request model, not a judge.
type GenerationStage struct {
	GenerationID, ConversationID string
	TraceID, SpanID              string
	AgentName, AgentVersion      string
	Provider, Model              string
}

// ToolResultDecision is fail-closed by default. On allow, nil Transformed keeps
// the original result; a non-nil value replaces its model-visible content.
// Implementations must preserve the original call ID/name. On deny, no result
// reaches the next generation and the caller terminates that stage.
type ToolResultDecision struct {
	Allowed     bool
	Transformed *ToolResult
}

// GenerationStages is a per-generation control seam, not an exporter or a
// conversation-wide callback. No runtime implementation is installed by this
// package. The caller owns fail-open policy and honors context cancellation.
// Implementations must not emit backend-only guard/evaluation telemetry.
type GenerationStages interface {
	// BeforeToolResult runs exactly once per produced tool result, before it is
	// copied into any next-generation input, span content or export payload.
	BeforeToolResult(context.Context, GenerationStage, ToolResult) (ToolResultDecision, error)
	// AfterGeneration runs once on the completed generation, before publication
	// or delegation. An error is not silently converted into successful delivery.
	AfterGeneration(context.Context, Generation) error
}
