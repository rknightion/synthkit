// SPDX-License-Identifier: AGPL-3.0-only

// Package aiagent is the AI-agent workload: it models agent CONVERSATIONS (coding agents like
// Claude Code / Codex and general agents like multi-agent orchestrators, autonomous tool-loops,
// multi-turn assistants, single-shot utilities) and emits three concurrent lanes keyed by
// conversation_id — native sigil generation/workflow-step/score ingest (content), OTLP gen_ai
// spans, and gen_ai_client_*/sigil_eval_* metrics. Vocabulary + the content corpus live in the
// internal/sigil mechanic lib; this workload owns the per-conversation turn/token/timing model.
// The Kind() string is "ai_agent" (matches web_service); the package dir is aiagent (Go idiom).
package aiagent

// Config is the blueprint-facing config for a `type: ai_agent` workload entry (frozen seam).
type Config struct {
	Resource   ResourceID  `yaml:"resource"`
	Agents     []AgentDecl `yaml:"agents"`
	Evaluators []EvalDecl  `yaml:"evaluators,omitempty"`
	Rules      []RuleDecl  `yaml:"rules,omitempty"`
}

// ResourceID is the OTLP resource identity for this workload's spans + metrics. Coding fleets use
// the sigil/job form (ServiceName="sigil", Job="sigil"); general fleets use the service+k8s form.
type ResourceID struct {
	ServiceName           string `yaml:"service_name,omitempty"`
	ServiceNamespace      string `yaml:"service_namespace,omitempty"`
	ServiceVersion        string `yaml:"service_version,omitempty"`
	DeploymentEnvironment string `yaml:"deployment_environment,omitempty"`
	K8sCluster            string `yaml:"k8s_cluster,omitempty"`
	K8sNamespace          string `yaml:"k8s_namespace,omitempty"`
	K8sDeployment         string `yaml:"k8s_deployment,omitempty"`
	CloudRegion           string `yaml:"cloud_region,omitempty"`
	Job                   string `yaml:"job,omitempty"`
}

// AgentDecl declares one agent in the fleet. Archetype selects the turn grammar + corpus
// (sigil.Archetype* values); SDK/Provider/Models/Tools/CaptureMode shape the emitted identity.
type AgentDecl struct {
	Name        string            `yaml:"name"`
	Archetype   string            `yaml:"archetype"`
	SDK         string            `yaml:"sdk"`      // sdk-go | sdk-python
	Provider    string            `yaml:"provider"` // anthropic | openai | bedrock | gemini
	Models      []string          `yaml:"models"`
	Tools       []string          `yaml:"tools,omitempty"`
	CaptureMode string            `yaml:"capture_mode,omitempty"` // full | no_tool_content | metadata_only | full_with_metadata_spans
	Version     string            `yaml:"version,omitempty"`      // declared agent version (empty ⇒ omitted, e.g. codex)
	Streaming   bool              `yaml:"streaming,omitempty"`    // streamText + time_to_first_token
	Subagents   []string          `yaml:"subagents,omitempty"`    // coding: spawns claude-code/<type> child conversations
	Tags        map[string]string `yaml:"tags,omitempty"`         // sigil.tag.<k> + generation tags (cwd/git.branch/region/team…)
	Activity    Activity          `yaml:"activity"`
}

// Activity shapes how often an agent produces conversations and how long they run.
type Activity struct {
	SessionsPerMin float64 `yaml:"sessions_per_min"`
	TurnsP50       int     `yaml:"turns_p50"`
	TurnsP95       int     `yaml:"turns_p95"`
}

// EvalDecl declares an online evaluator that scores matching generations.
type EvalDecl struct {
	Name          string  `yaml:"name"`
	Kind          string  `yaml:"kind"` // llm_judge | heuristic
	ScoreKey      string  `yaml:"score_key"`
	ValueType     string  `yaml:"value_type"` // number | bool | string
	Threshold     float64 `yaml:"threshold,omitempty"`
	JudgeModel    string  `yaml:"judge_model,omitempty"`
	JudgeProvider string  `yaml:"judge_provider,omitempty"`
}

// RuleDecl binds evaluators to agents at a sampling rate.
type RuleDecl struct {
	Name       string   `yaml:"name"`
	Selector   string   `yaml:"selector"` // e.g. all_assistant_generations | user_visible_turn
	SampleRate float64  `yaml:"sample_rate"`
	MatchAgent []string `yaml:"match_agent"`
	Evaluators []string `yaml:"evaluators"`
}
