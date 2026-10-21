// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/highcard"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/state"
)

const pipelineContentLimit = 1 << 20

// AgentBinding declares SDK-side identity and content selection, never a receipt
// or permission to provision backend rules. Runtime authority is root-owned.
type AgentBinding struct {
	Name           string            `yaml:"name"`
	Version        string            `yaml:"version"`
	SDK            string            `yaml:"sdk"`
	Provider       string            `yaml:"provider"`
	Models         []string          `yaml:"models"`
	Corpus         string            `yaml:"corpus"`
	ContentFacets  []string          `yaml:"content_facets"`
	Tools          []PipelineTool    `yaml:"tools"`
	ClientTags     map[string]string `yaml:"client_tags"`
	ContextTags    map[string]string `yaml:"context_tags"`
	GenerationTags map[string]string `yaml:"generation_tags"`
	Metadata       map[string]any    `yaml:"metadata"`
	CaptureMode    string            `yaml:"capture_mode"`
	Hooks          PipelineHooks     `yaml:"hooks"`
}
type PipelineTool struct {
	Name   string   `yaml:"name"`
	Kind   string   `yaml:"kind"`
	Facets []string `yaml:"facets"`
}
type PipelineHooks struct {
	Enabled   bool     `yaml:"enabled"`
	Phases    []string `yaml:"phases"`
	FailOpen  bool     `yaml:"fail_open"`
	TimeoutMS int64    `yaml:"timeout_ms"`
}
type AgentPipeline struct {
	Name             string            `yaml:"name"`
	EvaluationMode   string            `yaml:"evaluation_mode"`
	RequestKinds     []PipelineKind    `yaml:"request_kinds"`
	Stages           []PipelineStage   `yaml:"stages"`
	Review           PipelineReview    `yaml:"review"`
	Outcomes         []PipelineOutcome `yaml:"outcomes"`
	EscalatedOutcome string            `yaml:"escalated_outcome"`
	Ratings          PipelineRatings   `yaml:"ratings"`
}
type PipelineKind struct {
	Name   string  `yaml:"name"`
	Weight float64 `yaml:"weight"`
}
type PipelineStage struct {
	Name         string   `yaml:"name"`
	Service      string   `yaml:"service"`
	Operation    string   `yaml:"operation"`
	RequestKinds []string `yaml:"request_kinds"`
	CallerStage  string   `yaml:"caller_stage"`
}
type PipelineReview struct {
	Stage        string `yaml:"stage"`
	ReviseStage  string `yaml:"revise_stage"`
	MaxRevisions int    `yaml:"max_revisions"`
}
type PipelineOutcome struct {
	Name         string  `yaml:"name"`
	Weight       float64 `yaml:"weight"`
	Revisions    int     `yaml:"revisions"`
	ReviewPassed bool    `yaml:"review_passed"`
}
type PipelineRatings struct {
	SampleRate float64 `yaml:"sample_rate"`
}

type pipelineContent struct {
	system            string
	users, assistants []sigil.CorpusRecord
	tools             map[string][]*sigil.CorpusToolResult
}
type pipelineRuntime struct {
	contents map[string]pipelineContent
	states   map[string]*state.State
	started  time.Time
}

func pipelineError(s string) error { return fmt.Errorf("app agent_pipeline: %s", s) }
func pipelineName(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "*?[]\\")
}
func facetKey(f []string) (string, error) {
	a := slices.Clone(f)
	slices.Sort(a)
	for i, s := range a {
		if strings.TrimSpace(s) == "" || strings.TrimSpace(s) != s || i > 0 && a[i-1] == s {
			return "", pipelineError("facets require unique nonempty names")
		}
	}
	// JSON avoids delimiter collisions for arbitrary opaque facet names.
	b, _ := json.Marshal(a)
	if len(a) == 0 {
		return "[]", nil
	}
	return string(b), nil
}
func normalizedTag(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
}
func pipelineTags(a *AgentBinding) map[string]string {
	out := map[string]string{}
	for k, v := range a.ClientTags {
		out[k] = v
	}
	for k, v := range a.ContextTags {
		out[k] = v
	}
	return out
}
func validateBinding(a *AgentBinding, runtime string) error {
	if !pipelineName(a.Name) {
		return pipelineError("agent name must be a literal without surrounding whitespace")
	}
	if a.SDK != sigil.SDKGo && a.SDK != sigil.SDKPython {
		return pipelineError("only sdk-go and sdk-python are admitted")
	}
	if a.SDK == sigil.SDKGo && runtime != "go" || a.SDK == sigil.SDKPython && runtime != "python" {
		return pipelineError("SDK and service runtime disagree")
	}
	if a.SDK != sigil.SDKGo && len(a.ContextTags) > 0 {
		return pipelineError("context_tags require sdk-go")
	}
	if a.Corpus == "" {
		return pipelineError("explicit corpus key required")
	}
	if _, err := facetKey(a.ContentFacets); err != nil {
		return err
	}
	if a.CaptureMode != "" && a.CaptureMode != "default" && a.CaptureMode != sigil.CaptureFull && a.CaptureMode != sigil.CaptureMetadataOnly && a.CaptureMode != sigil.CaptureNoToolContent && a.CaptureMode != sigil.CaptureFullWithMetadataSpans {
		return pipelineError("unsupported capture mode")
	}
	h := a.Hooks
	if h.TimeoutMS < 0 || h.TimeoutMS > 15000 || h.Enabled && h.TimeoutMS == 0 {
		return pipelineError("hook timeout_ms must be positive when enabled and at most 15000")
	}
	if len(h.Phases) > 1 || len(h.Phases) == 1 && h.Phases[0] != "preflight" || h.Enabled && len(h.Phases) != 1 {
		return pipelineError("only one explicit preflight phase is admitted")
	}
	keys := map[string]string{}
	for _, tags := range []map[string]string{a.ClientTags, a.ContextTags} {
		for k, v := range tags {
			if strings.TrimSpace(k) != k || k == "" || strings.TrimSpace(v) != v || v == "" || len(k) > 128 || len(v) > 128 || highcard.Contains(k) {
				return pipelineError("client/context tags need bounded nonempty low-cardinality keys and values")
			}
			n := normalizedTag(sigil.TagPrefix + k)
			if prev, ok := keys[n]; ok && prev != k {
				return pipelineError("metric tag keys collide after normalization")
			}
			keys[n] = k
		}
	}
	if len(keys) > 16 {
		return pipelineError("at most 16 metric tag keys admitted")
	}
	seen := map[string]bool{}
	for _, t := range a.Tools {
		if !pipelineName(t.Name) || seen[t.Name] || t.Kind != "tool_result" {
			return pipelineError("tools require unique literal names and explicit tool_result kind")
		}
		seen[t.Name] = true
		if _, err := facetKey(t.Facets); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(a.Metadata)
	if err != nil || len(raw) > 16384 {
		return pipelineError("metadata must be JSON of at most 16KiB")
	}
	return nil
}

// agentDeclarations is a pure structural callback. No corpus loading, Build,
// sampling, collaborator use or identity minting occurs here.
func agentDeclarations(cfg any, b core.Binding) ([]core.AgentDeclaration, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, pipelineError("invalid config")
	}
	if err := validatePipeline(c); err != nil {
		return nil, err
	}
	var out []core.AgentDeclaration
	for _, s := range c.Services {
		a := s.AgentObservability
		if a == nil {
			continue
		}
		ns := s.Namespace
		if ns == "" {
			ns = s.Name
		}
		d := core.AgentDeclaration{Name: a.Name, Service: core.AgentServiceIdentity{Name: s.Name, Namespace: ns}, ProducerMode: core.AgentProducerReal, HookPolicy: core.AgentHookPolicy{Enabled: a.Hooks.Enabled, Phases: slices.Clone(a.Hooks.Phases), FailOpen: a.Hooks.FailOpen, Timeout: time.Duration(a.Hooks.TimeoutMS) * time.Millisecond}}
		for _, m := range a.Models {
			d.Models = append(d.Models, core.AgentModelPair{Provider: a.Provider, Model: m})
		}
		out = append(out, d)
	}
	return out, nil
}
func pipelineToolSelected(a *AgentBinding, name string) bool {
	return slices.ContainsFunc(a.Tools, func(t PipelineTool) bool { return t.Name == name })
}

func stageEnabled(s PipelineStage, k string) bool {
	return len(s.RequestKinds) == 0 || slices.Contains(s.RequestKinds, k)
}
func pipelineIndices(p *AgentPipeline) (int, int) {
	a, b := -1, -1
	for i, s := range p.Stages {
		if s.Name == p.Review.ReviseStage {
			a = i
		}
		if s.Name == p.Review.Stage {
			b = i
		}
	}
	return a, b
}
func pipelinePlan(p *AgentPipeline, k string, o PipelineOutcome) []int {
	a, b := pipelineIndices(p)
	var out []int
	for i, s := range p.Stages {
		if !stageEnabled(s, k) {
			continue
		}
		out = append(out, i)
		if i == b {
			for range o.Revisions {
				for j := a; j <= b; j++ {
					if stageEnabled(p.Stages[j], k) {
						out = append(out, j)
					}
				}
			}
			if !o.ReviewPassed {
				break
			}
		}
	}
	return out
}
func validatePipeline(c *Config) error {
	has := false
	byName := map[string]ServiceNode{}
	agents := map[string]bool{}
	for _, s := range c.Services {
		byName[s.Name] = s
		if s.AgentObservability != nil {
			has = true
			a := s.AgentObservability
			if err := validateBinding(a, s.Runtime); err != nil {
				return err
			}
			key := strings.ToLower(a.Name)
			if agents[key] {
				return pipelineError("duplicate agent identity")
			}
			agents[key] = true
		}
	}
	p := c.AgentPipeline
	if p == nil {
		if has {
			return pipelineError("agent bindings require agent_pipeline")
		}
		return nil
	}
	// This executor projects only its bound stage plan, not ordinary graph
	// logs/RUM/unbound hops. Reject declarations rather than silently drop them.
	for _, s := range c.Services {
		if s.AgentObservability == nil {
			return pipelineError("every service must be bound to agent_pipeline")
		}
		if len(s.Logs) > 0 {
			return pipelineError("log output unsupported for agent_pipeline")
		}
		if len(s.Pages) > 0 || slices.Contains(s.Profiles, "rum_faro") {
			return pipelineError("RUM output unsupported for agent_pipeline")
		}
	}
	if c.Automation != nil || !pipelineName(p.Name) || p.EvaluationMode != "real" || len(p.Stages) < 1 || len(p.Stages) > 16 {
		return pipelineError("require named real-only pipeline with 1-16 stages and no automation")
	}
	if !c.otelMetricsEnabled() {
		return pipelineError("initial pipeline requires otel.metrics true")
	}
	if math.IsNaN(p.Ratings.SampleRate) || math.IsInf(p.Ratings.SampleRate, 0) || p.Ratings.SampleRate < 0 || p.Ratings.SampleRate > 1 {
		return pipelineError("invalid rating sample_rate")
	}
	kinds := map[string]bool{}
	total := 0.0
	for _, k := range p.RequestKinds {
		if !pipelineName(k.Name) || kinds[k.Name] || !validWeight(k.Weight) {
			return pipelineError("invalid request kind/weight")
		}
		kinds[k.Name] = true
		total += k.Weight
	}
	if total <= 0 || math.IsInf(total, 0) {
		return pipelineError("positive finite request weights required")
	}
	stages := map[string]int{}
	used := map[string]bool{}
	for i, s := range p.Stages {
		n, ok := byName[s.Service]
		if !ok || n.AgentObservability == nil || !pipelineName(s.Name) {
			return pipelineError("stage needs a named bound service")
		}
		if _, dup := stages[s.Name]; dup {
			return pipelineError("duplicate stage name")
		}
		stages[s.Name] = i
		used[s.Service] = true
		if n.External || !n.tracesEnabled() || !n.metricsEnabled() || !resolveNodeKind(n.Type).serverSpan || n.AgenticFlow != nil || len(n.Spans) > 0 {
			return pipelineError("bound services require placed traced SDK telemetry without agentic_flow/inline spans")
		}
		a := n.AgentObservability
		for _, profile := range n.Profiles {
			if strings.Contains(profile, "gen_ai") {
				return pipelineError("bound services cannot also emit gen_ai profiles")
			}
		}
		if len(n.Metrics) > 0 || len(n.CustomMetrics) > 0 { // normalized identity is checked again after graph compilation
			for _, m := range append(slices.Clone(n.Metrics), n.CustomMetrics...) {
				if strings.HasPrefix(normalizedTag(m.Name), "gen_ai_client_") {
					return pipelineError("duplicate inline SDK instrument")
				}
			}
		}
		if s.Operation != "generation" && s.Operation != "retrieval" {
			return pipelineError("unknown stage operation")
		}
		if s.Operation == "generation" {
			if a.Provider == "" || len(a.Models) == 0 || s.CallerStage != "" {
				return pipelineError("generation needs provider/models and no caller_stage")
			}
			seen := map[string]bool{}
			for _, m := range a.Models {
				if strings.TrimSpace(m) == "" || seen[m] {
					return pipelineError("invalid model set")
				}
				seen[m] = true
			}
		} else {
			caller, found := stages[s.CallerStage]
			if !found || caller >= i || p.Stages[caller].Operation != "generation" || a.Hooks.Enabled || a.Provider != "" || len(a.Models) > 0 || len(a.Tools) == 0 {
				return pipelineError("retrieval needs prior generation caller, explicit tools, no model or source hooks")
			}
			ca := byName[p.Stages[caller].Service].AgentObservability
			if !slices.ContainsFunc(a.Tools, func(t PipelineTool) bool { return pipelineToolSelected(ca, t.Name) }) {
				return pipelineError("retrieval has no tool selected by producing caller")
			}
		}
		seenKinds := map[string]bool{}
		for _, k := range s.RequestKinds {
			if !kinds[k] || seenKinds[k] {
				return pipelineError("invalid stage request kind filter")
			}
			seenKinds[k] = true
		}
	}
	for _, s := range c.Services {
		if s.AgentObservability != nil && !used[s.Name] {
			return pipelineError("unused agent binding")
		}
	}
	if p.Stages[0].Operation != "generation" || !byName[p.Stages[0].Service].Entry {
		return pipelineError("first generation must run on entry service")
	}
	a, b := pipelineIndices(p)
	if a < 0 || b <= a || p.Stages[a].Operation != "generation" || p.Stages[b].Operation != "generation" || p.Review.MaxRevisions < 0 || p.Review.MaxRevisions > 3 {
		return pipelineError("invalid bounded review/revise pair")
	}
	outcomes := map[string]bool{}
	total = 0
	for _, o := range p.Outcomes {
		if !pipelineName(o.Name) || outcomes[o.Name] || !validWeight(o.Weight) || o.Revisions < 0 || o.Revisions > p.Review.MaxRevisions || !o.ReviewPassed && o.Revisions != p.Review.MaxRevisions {
			return pipelineError("invalid outcome/revision budget")
		}
		outcomes[o.Name] = true
		total += o.Weight
		for _, k := range p.RequestKinds {
			if k.Weight == 0 {
				continue
			}
			if !stageEnabled(p.Stages[a], k.Name) || !stageEnabled(p.Stages[b], k.Name) {
				return pipelineError("outcome requires skipped review/revise")
			}
			plan := pipelinePlan(p, k.Name, o)
			if len(plan) > 32 {
				return pipelineError("expanded attempts exceed 32")
			}
			executed := map[string]int{}
			retrieved := map[string]bool{}
			for _, idx := range plan {
				s := p.Stages[idx]
				caller := p.Stages[0].Service
				if s.CallerStage != "" {
					ci := stages[s.CallerStage]
					if executed[s.CallerStage] == 0 {
						return pipelineError("retrieval caller skipped")
					}
					key := fmt.Sprintf("%s/%d", s.Name, executed[s.CallerStage])
					if retrieved[key] {
						return pipelineError("revision repeats retrieval without a new producing tool call")
					}
					retrieved[key] = true
					caller = p.Stages[ci].Service
				}
				if s.Service != caller && !slices.Contains(byName[caller].Calls, s.Service) {
					return pipelineError("stage transition lacks declared caller edge")
				}
				executed[s.Name]++
			}
		}
	}
	if total <= 0 || math.IsInf(total, 0) || !outcomes[p.EscalatedOutcome] {
		return pipelineError("positive outcome weights and declared escalated outcome required")
	}
	if _, err := json.Marshal(c); err != nil {
		return pipelineError("config must be finite JSON-compatible data")
	}
	return nil
}
func validWeight(w float64) bool { return !math.IsNaN(w) && !math.IsInf(w, 0) && w >= 0 }

func selectPipelineContent(a *AgentBinding, generation bool) (pipelineContent, error) {
	out := pipelineContent{tools: map[string][]*sigil.CorpusToolResult{}}
	records, err := sigil.LoadCorpus(a.Corpus)
	if err != nil {
		return out, err
	}
	key, _ := facetKey(a.ContentFacets)
	for _, r := range records {
		if r.Kind == "system_prompt" && out.system == "" {
			out.system = r.Text
		}
		rk, e := facetKey(r.Facets)
		if e != nil {
			return out, e
		}
		if generation && r.Kind == "turn" && rk == key {
			text := false
			for _, p := range r.Parts {
				// The frozen native Part has no explicit empty-thinking kind;
				// its current encoder would turn stripped thinking into text.
				// Refuse that concrete shape instead of silently losing it.
				if a.CaptureMode == sigil.CaptureMetadataOnly && p.Thinking != "" {
					return out, pipelineError("metadata_only thinking parts unsupported by current native part encoder")
				}
				if p.ToolCall != nil || p.ToolResult != nil {
					return out, pipelineError("selected model turns contain implicit tools")
				}
				text = text || p.Text != ""
			}
			if text {
				if r.Role == "user" {
					out.users = append(out.users, r)
				}
				if r.Role == "assistant" {
					out.assistants = append(out.assistants, r)
				}
			}
		}
		for _, t := range a.Tools {
			if generation {
				continue
			} // caller defines tools; source selects their result content
			tk, _ := facetKey(t.Facets)
			if r.Kind != t.Kind || rk != tk {
				continue
			}
			for _, p := range r.Parts {
				if p.ToolResult != nil && p.ToolResult.Name == t.Name {
					tr := p.ToolResult
					// Retain an immutable corpus candidate reference only. Actual
					// results (including JSON bytes and call identity) materialize
					// at the executing stage, never during conversation planning.
					out.tools[t.Name] = append(out.tools[t.Name], tr)
				}
			}
		}
	}
	if generation && (len(out.users) == 0 || len(out.assistants) == 0) {
		return out, pipelineError("content_facets must select both textual user and assistant turns; no fallback")
	}
	for _, t := range a.Tools {
		if !generation && len(out.tools[t.Name]) == 0 {
			return out, pipelineError("tool kind/facets select no matching result")
		}
	}
	return out, nil
}
func (w *Workload) buildPipeline() error {
	if w.cfg.AgentPipeline == nil {
		return nil
	}
	p := w.cfg.AgentPipeline
	// Profiles expand into effective log specs during graph compilation. Check
	// the normalized graph too, so a profile cannot bypass the inline-log fence.
	for _, n := range w.graph.nodes {
		if len(n.logs) > 0 {
			return pipelineError("log output unsupported for agent_pipeline (including profiles)")
		}
	}
	// Always require the canonical construction dependency, even when optional
	// hooks and ratings are off. Ordinary unwired boot/live/restore must stay
	// closed. Nonnil is NOT custody/admission proof: root installs this only
	// after its real admission checks; structural discovery remains pure.
	if w.b.GenerationStages == nil {
		return pipelineError("GenerationStages construction dependency required for every pipeline; not admission proof")
	}
	if p.Ratings.SampleRate > 0 && w.b.Ratings == nil {
		return pipelineError("positive ratings require Ratings collaborator")
	}
	rt := &pipelineRuntime{contents: map[string]pipelineContent{}, states: map[string]*state.State{}}
	// Compile candidate references, not complete conversations or carry-forward.
	for _, s := range p.Stages {
		n := w.graph.byName[s.Service]
		a := n.decl.AgentObservability
		if w.b.Cluster == nil {
			return pipelineError("verified cluster placement required")
		}
		found := false
		for _, pl := range w.b.Cluster.Workloads {
			if pl.Name == s.Service && pl.Namespace == w.identity(n).namespace && len(pl.PodNames) > 0 {
				controller := pl.Controller
				if controller == "" {
					controller = "deployment"
				}
				expected := n.decl.Controller
				if expected == "" {
					expected = "deployment"
				}
				if (controller != "deployment" && controller != "statefulset") || controller != expected || found {
					return pipelineError("unsupported, ambiguous or contradictory bound placement")
				}
				found = true
			}
		}
		if !found {
			return pipelineError("bound service missing actual pod placement")
		}
		for _, m := range n.metrics {
			if strings.HasPrefix(normalizedTag(m.Name), "gen_ai_client_") {
				return pipelineError("normalized duplicate SDK metric")
			}
		}
		content, err := selectPipelineContent(a, s.Operation == "generation")
		if err != nil {
			return err
		}
		rt.contents[s.Name] = content
	}
	// Conservative serialized-content upper bound including repeated carry. It is
	// computed for every admitted path before any request/content publication.
	for _, k := range p.RequestKinds {
		for _, o := range p.Outcomes {
			size := 0
			carry := 0
			for _, idx := range pipelinePlan(p, k.Name, o) {
				s := p.Stages[idx]
				c := rt.contents[s.Name]
				own := len(c.system) + 1024
				for _, set := range [][]sigil.CorpusRecord{c.users, c.assistants} {
					max := 0
					for _, r := range set {
						raw, _ := json.Marshal(r)
						if len(raw) > max {
							max = len(raw)
						}
					}
					own += max
				}
				for _, results := range c.tools {
					max := 0
					for _, r := range results {
						raw, _ := json.Marshal(r)
						if len(raw) > max {
							max = len(raw)
						}
					}
					// Allow textual plus base64-encoded JSON representations and
					// their accepted carry copy without materializing either here.
					own += 4 * max
				}
				raw, _ := json.Marshal(w.graph.byName[s.Service].decl.AgentObservability)
				own += len(raw)
				if own > pipelineContentLimit || carry > pipelineContentLimit-own || size > pipelineContentLimit-carry-own {
					return pipelineError("modeled request content exceeds 1MiB")
				}
				size += carry + own
				carry += own
			}
		}
	}
	w.pipeline = rt
	return nil
}
