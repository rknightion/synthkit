// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import "time"

// RuleSpec creates an online rule. Pointers preserve omitted defaults and
// explicit false/zero/empty values. No semantic update is exposed.
// Wire schemas follow grafana/sigil bc963fb09, eval/control/http.go and eval/types.go.
type RuleSpec struct {
	RuleID            string          `json:"rule_id" yaml:"rule_id"`
	Enabled           *bool           `json:"enabled,omitempty" yaml:"enabled"`
	Selector          string          `json:"selector,omitempty" yaml:"selector"`
	Match             *map[string]any `json:"match,omitempty" yaml:"match"`
	SampleRate        *float64        `json:"sample_rate,omitempty" yaml:"sample_rate"`
	EvaluatorIDs      []string        `json:"evaluator_ids" yaml:"evaluator_ids"`
	ExecutionMode     string          `json:"execution_mode,omitempty" yaml:"execution_mode"`
	AlertRuleUIDs     *[]string       `json:"alert_rule_uids,omitempty" yaml:"alert_rule_uids"`
	FilterableTagKeys *[]string       `json:"filterable_tag_keys,omitempty" yaml:"filterable_tag_keys"`
	MinIdleSeconds    *int            `json:"min_idle_seconds,omitempty" yaml:"min_idle_seconds"`
}

// RuleSnapshot is the backend's configuration history shape.
type RuleSnapshot struct {
	Enabled           bool           `json:"enabled"`
	Selector          string         `json:"selector"`
	Match             map[string]any `json:"match"`
	SampleRate        float64        `json:"sample_rate"`
	EvaluatorIDs      []string       `json:"evaluator_ids"`
	ExecutionMode     string         `json:"execution_mode,omitempty"`
	AlertRuleUIDs     []string       `json:"alert_rule_uids,omitempty"`
	FilterableTagKeys []string       `json:"filterable_tag_keys,omitempty"`
	MinIdleSeconds    *int           `json:"min_idle_seconds,omitempty"`
}

type RuleEdit struct {
	UpdatedBy string       `json:"updated_by"`
	UpdatedAt time.Time    `json:"updated_at"`
	Before    RuleSnapshot `json:"before"`
	After     RuleSnapshot `json:"after"`
}

// Rule is an observation, never a custody or backend-drain receipt.
type Rule struct {
	RuleSnapshot
	TenantID  string     `json:"tenant_id"`
	RuleID    string     `json:"rule_id"`
	CreatedBy string     `json:"created_by"`
	UpdatedBy string     `json:"updated_by"`
	Edits     []RuleEdit `json:"edits,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// CollectionSpec has no caller-assigned ID or actor (collection_service.go).
type CollectionSpec struct {
	Name        string  `json:"name" yaml:"name"`
	Description *string `json:"description,omitempty" yaml:"description"`
}

type Collection struct {
	TenantID     string    `json:"tenant_id"`
	CollectionID string    `json:"collection_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	CreatedBy    string    `json:"created_by"`
	UpdatedBy    string    `json:"updated_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MemberCount  int       `json:"member_count"`
}

// RuleActionCondition deliberately excludes score_value expansion.
type RuleActionCondition struct {
	Kind string `json:"kind" yaml:"kind"`
}
type RuleActionConfig struct {
	Kind          string   `json:"kind" yaml:"kind"`
	CollectionIDs []string `json:"collection_ids" yaml:"collection_ids"`
}
type RuleActionSpec struct {
	Condition    RuleActionCondition `json:"condition" yaml:"condition"`
	ActionConfig RuleActionConfig    `json:"action_config" yaml:"action_config"`
	Enabled      *bool               `json:"enabled,omitempty" yaml:"enabled"`
}
type RuleAction struct {
	TenantID     string              `json:"tenant_id"`
	ActionID     string              `json:"action_id"`
	RuleID       string              `json:"rule_id"`
	Condition    RuleActionCondition `json:"condition"`
	ActionConfig RuleActionConfig    `json:"action_config"`
	Enabled      bool                `json:"enabled"`
	CreatedBy    string              `json:"created_by"`
	UpdatedBy    string              `json:"updated_by"`
	DeletedAt    *time.Time          `json:"deleted_at,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

// Suite DTOs follow eval/suite.go and control/suite_service.go. These are
// transport representations, not authority to publish or execute evaluations.
type SuiteSpec struct {
	SuiteID     string    `json:"suite_id" yaml:"suite_id"`
	Name        string    `json:"name" yaml:"name"`
	Description *string   `json:"description,omitempty" yaml:"description"`
	Tags        *[]string `json:"tags,omitempty" yaml:"tags"`
}
type Suite struct {
	TenantID      string         `json:"tenant_id"`
	SuiteID       string         `json:"suite_id"`
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	LatestVersion string         `json:"latest_version,omitempty"`
	Versions      []SuiteVersion `json:"versions,omitempty"`
	CreatedBy     string         `json:"created_by,omitempty"`
	UpdatedBy     string         `json:"updated_by,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// DraftSpec cannot request cloning of a previous checkpoint.
type DraftSpec struct {
	Changelog *string `json:"changelog,omitempty" yaml:"changelog"`
}
type SuiteVersion struct {
	TenantID      string         `json:"tenant_id,omitempty"`
	SuiteID       string         `json:"suite_id"`
	Version       string         `json:"version"`
	TestCaseCount int            `json:"test_case_count"`
	Changelog     string         `json:"changelog,omitempty"`
	Published     bool           `json:"published"`
	SourceVersion string         `json:"source_version,omitempty"`
	CreatedBy     string         `json:"created_by,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	PublishedBy   string         `json:"published_by,omitempty"`
	PublishedAt   *time.Time     `json:"published_at,omitempty"`
	Bindings      []SuiteBinding `json:"bindings,omitempty"`
}
type SuiteBinding struct {
	BindingID        string            `json:"binding_id" yaml:"binding_id"`
	EvaluatorID      string            `json:"evaluator_id" yaml:"evaluator_id"`
	EvaluatorVersion string            `json:"evaluator_version" yaml:"evaluator_version"`
	InputMappings    map[string]string `json:"input_mappings" yaml:"input_mappings"`
	ReportRole       string            `json:"report_role,omitempty" yaml:"report_role"`
}

// ArtifactRef is observed metadata only; this client has no upload surface.
type ArtifactRef struct {
	ArtifactID string `json:"artifact_id" yaml:"artifact_id"`
	Name       string `json:"name" yaml:"name"`
	Kind       string `json:"kind" yaml:"kind"`
}

// CaseSpec cannot upsert an existing case: CreateCase always sends create_only.
// Nonempty artifact references are outside this initial transport slice.
type CaseSpec struct {
	TestCaseID                string          `json:"test_case_id" yaml:"test_case_id"`
	Name                      *string         `json:"name,omitempty" yaml:"name"`
	Description               *string         `json:"description,omitempty" yaml:"description"`
	Tags                      *[]string       `json:"tags,omitempty" yaml:"tags"`
	Category                  *string         `json:"category,omitempty" yaml:"category"`
	Input                     map[string]any  `json:"input" yaml:"input"`
	Expected                  *map[string]any `json:"expected,omitempty" yaml:"expected"`
	Metadata                  *map[string]any `json:"metadata,omitempty" yaml:"metadata"`
	ArtifactRefs              *[]ArtifactRef  `json:"artifact_refs,omitempty" yaml:"artifact_refs"`
	SanitizationReviewed      *bool           `json:"sanitization_reviewed,omitempty" yaml:"sanitization_reviewed"`
	SanitizationContentDigest *string         `json:"sanitization_content_digest,omitempty" yaml:"sanitization_content_digest"`
}

// CaseSnapshot contains only reviewed content, not storage timestamps/tenant.
type CaseSnapshot struct {
	TestCaseID   string         `json:"test_case_id" yaml:"test_case_id"`
	SuiteID      string         `json:"suite_id,omitempty" yaml:"suite_id"`
	SuiteVersion string         `json:"suite_version,omitempty" yaml:"suite_version"`
	Name         string         `json:"name,omitempty" yaml:"name"`
	Description  string         `json:"description,omitempty" yaml:"description"`
	Tags         []string       `json:"tags" yaml:"tags"`
	Category     string         `json:"category,omitempty" yaml:"category"`
	Input        map[string]any `json:"input" yaml:"input"`
	Expected     map[string]any `json:"expected" yaml:"expected"`
	Metadata     map[string]any `json:"metadata" yaml:"metadata"`
	ArtifactRefs []ArtifactRef  `json:"artifact_refs" yaml:"artifact_refs"`
}
type SuiteCase struct {
	CaseSnapshot
	TenantID  string    `json:"tenant_id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// PublicationReview must be a complete reviewed checkpoint. Empty slices are
// explicit; nil cannot fall back to the backend's legacy unreviewed publication.
type PublicationReview struct {
	Cases    []CaseSnapshot `json:"cases" yaml:"cases"`
	Bindings []SuiteBinding `json:"bindings" yaml:"bindings"`
}
