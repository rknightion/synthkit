// SPDX-License-Identifier: AGPL-3.0-only

// Package agento11yprovision provides only the evaluator and hook management
// transport. It does not establish ownership, attest destinations, or activate
// provisioning. Wire types follow grafana/sigil bc963fb09ab214fa73364678f1de180cafe9e90c
// (eval/control/request_validation.go, eval/types.go, eval/hooks/http.go).
package agento11yprovision

import "time"

// OutputKey describes an evaluator's typed score. Optional scalar pointers
// distinguish omission from explicit zero or false.
type OutputKey struct {
	Key           string   `json:"key" yaml:"key"`
	Type          string   `json:"type" yaml:"type"`
	Description   string   `json:"description,omitempty" yaml:"description"`
	Unit          string   `json:"unit,omitempty" yaml:"unit"`
	PassThreshold *float64 `json:"pass_threshold,omitempty" yaml:"pass_threshold"`
	Enum          []string `json:"enum" yaml:"enum"`
	Min           *float64 `json:"min,omitempty" yaml:"min"`
	Max           *float64 `json:"max,omitempty" yaml:"max"`
	PassMatch     []string `json:"pass_match" yaml:"pass_match"`
	PassValue     *bool    `json:"pass_value,omitempty" yaml:"pass_value"`
}

// EvaluatorSpec creates an explicit immutable evaluator version. There is no
// evaluator enabled flag. Config is a kind-specific JSON object, not a JSON string.
type EvaluatorSpec struct {
	EvaluatorID string         `json:"evaluator_id" yaml:"evaluator_id"`
	Version     string         `json:"version" yaml:"version"`
	Kind        string         `json:"kind" yaml:"kind"`
	Description string         `json:"description,omitempty" yaml:"description"`
	Config      map[string]any `json:"config" yaml:"config"`
	OutputKeys  []OutputKey    `json:"output_keys" yaml:"output_keys"`
}

// Evaluator is an observed version row. GetEvaluator selects the latest row;
// ListEvaluators returns all nondeleted versions in backend pagination order.
type Evaluator struct {
	EvaluatorSpec
	TenantID              string     `json:"tenant_id"`
	IsPredefined          bool       `json:"is_predefined"`
	SourceTemplateID      string     `json:"source_template_id,omitempty"`
	SourceTemplateVersion string     `json:"source_template_version,omitempty"`
	CreatedBy             string     `json:"created_by"`
	UpdatedBy             string     `json:"updated_by"`
	DeletedAt             *time.Time `json:"deleted_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// HookToolFilter blocks tool names matching these globs.
type HookToolFilter struct {
	BlockedNames []string `json:"blocked_names" yaml:"blocked_names"`
}

// HookRedactPattern is a regex redaction step. Replacement is backend-owned.
type HookRedactPattern struct {
	ID    string `json:"id,omitempty" yaml:"id"`
	Regex string `json:"regex" yaml:"regex"`
}

// HookRedactConfig preserves redaction pattern order.
type HookRedactConfig struct {
	Patterns []HookRedactPattern `json:"patterns" yaml:"patterns"`
}

// HookRuleSpec is a create or full replacement, never a patch. Scalar pointers
// preserve backend defaults when omitted and false/zero when supplied. Empty
// match/evaluator/filter/redact containers are not omitted during replacement.
// A nil ToolFilter or Redact omits that optional field; a PUT still replaces it.
type HookRuleSpec struct {
	RuleID       string            `json:"rule_id,omitempty" yaml:"rule_id"`
	Enabled      *bool             `json:"enabled,omitempty" yaml:"enabled"`
	Phase        string            `json:"phase,omitempty" yaml:"phase"`
	Priority     *int              `json:"priority,omitempty" yaml:"priority"`
	Selector     string            `json:"selector,omitempty" yaml:"selector"`
	Match        map[string]any    `json:"match" yaml:"match"`
	EvaluatorIDs []string          `json:"evaluator_ids" yaml:"evaluator_ids"`
	ActionOnFail string            `json:"action_on_fail,omitempty" yaml:"action_on_fail"`
	ShortCircuit *bool             `json:"short_circuit,omitempty" yaml:"short_circuit"`
	ToolFilter   *HookToolFilter   `json:"tool_filter,omitempty" yaml:"tool_filter"`
	Redact       *HookRedactConfig `json:"redact,omitempty" yaml:"redact"`
}

// HookRule is observed configuration, not a registration or ownership receipt.
type HookRule struct {
	TenantID     string            `json:"tenant_id"`
	RuleID       string            `json:"rule_id"`
	Enabled      bool              `json:"enabled"`
	Phase        string            `json:"phase"`
	Priority     int               `json:"priority"`
	Selector     string            `json:"selector"`
	Match        map[string]any    `json:"match"`
	EvaluatorIDs []string          `json:"evaluator_ids"`
	ActionOnFail string            `json:"action_on_fail"`
	ShortCircuit bool              `json:"short_circuit"`
	ToolFilter   *HookToolFilter   `json:"tool_filter,omitempty"`
	Redact       *HookRedactConfig `json:"redact,omitempty"`
	CreatedBy    string            `json:"created_by,omitempty"`
	UpdatedBy    string            `json:"updated_by,omitempty"`
	DeletedAt    *time.Time        `json:"deleted_at,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}
