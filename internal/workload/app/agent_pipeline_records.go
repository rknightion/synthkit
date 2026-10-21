// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/state"
)

const pipelineDurationMetric = "gen_ai.client.operation.duration"
const pipelineTokenMetric = "gen_ai.client.token.usage"

func pipelineDraw(seed, purpose string) float64 {
	s := sha256.Sum256([]byte(seed + "\x00" + purpose))
	return float64(binary.BigEndian.Uint64(s[:8])>>11) / float64(uint64(1)<<53)
}
func pipelinePick(seed, purpose string, n int) int {
	return int(pipelineDraw(seed, purpose) * float64(n))
}
func pipelineKind(p *AgentPipeline, r *ledger.Request) string {
	total := 0.0
	for _, k := range p.RequestKinds {
		total += k.Weight
	}
	draw := pipelineDraw(r.SpanID, "kind") * total
	for _, k := range p.RequestKinds {
		draw -= k.Weight
		if draw < 0 {
			return k.Name
		}
	}
	return p.RequestKinds[len(p.RequestKinds)-1].Name
}
func pipelineOutcome(p *AgentPipeline, r *ledger.Request) PipelineOutcome {
	total := 0.0
	for _, o := range p.Outcomes {
		total += o.Weight
	}
	draw := pipelineDraw(r.SpanID, "outcome") * total
	for _, o := range p.Outcomes {
		draw -= o.Weight
		if draw < 0 {
			return o
		}
	}
	return p.Outcomes[len(p.Outcomes)-1]
}

// Clone at the stage boundary: neither adapter mutation nor captured writer
// aliases may put an original tool result into later model-visible content.
func pipelineClone[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic("validated pipeline JSON became invalid")
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		panic("pipeline JSON clone failed")
	}
	return out
}
func pipelineMessage(r sigil.CorpusRecord, role string) sigil.Message {
	m := sigil.Message{Role: role}
	for _, p := range r.Parts {
		if p.Text != "" {
			m.Parts = append(m.Parts, sigil.Part{Text: p.Text, ProviderType: "text"})
		}
		if p.Thinking != "" {
			m.Parts = append(m.Parts, sigil.Part{Thinking: p.Thinking, ProviderType: "thinking"})
		}
	}
	return m
}

// Source: SDK 75a1c548 Go content_capture.go/contentcapture.Strip and
// Python client.py _strip_content. Native generation capture is distinct from
// tool-span capture: default/no_tool_content/full_with_metadata_spans all keep
// the full native export. There is no per-tool IncludeContent config here.
func pipelineCaptureGeneration(g sigil.Generation, mode string) (sigil.Generation, error) {
	out := pipelineClone(g)
	if mode == "" || mode == "default" {
		mode = sigil.CaptureNoToolContent
	}
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	out.Metadata["agento11y.sdk.content_capture_mode"] = mode
	if mode != sigil.CaptureMetadataOnly {
		return out, nil
	}
	out.SystemPrompt = ""
	if out.CallError != "" {
		category, _ := out.Metadata["error.category"].(string)
		if category == "" {
			category = "sdk_error"
		}
		out.CallError = category
	}
	delete(out.Metadata, "call_error")
	delete(out.Metadata, sigil.AttrConversationTitle)
	// The pinned Go policy also removes the legacy title mirror. The pinned
	// Python policy removes the current title only, not arbitrary authored
	// metadata (including an authored legacy key). Never promise blanket scrub.
	if out.Metadata[sigil.AttrSDKName] == sigil.SDKGo {
		delete(out.Metadata, "sigil.conversation.title")
	}
	for _, messages := range [][]sigil.Message{out.Input, out.Output} {
		for _, message := range messages {
			for i := range message.Parts {
				part := &message.Parts[i]
				if part.Thinking != "" || part.ProviderType == "thinking" {
					return sigil.Generation{}, pipelineError("metadata_only thinking parts unsupported by current native part encoder")
				}
				if part.Media != nil && out.Metadata[sigil.AttrSDKName] == sigil.SDKPython {
					return sigil.Generation{}, pipelineError("Python media parts not admitted by pipeline")
				}
				part.Text = ""
				if part.ToolCall != nil {
					part.ToolCall.InputJSON = nil
				}
				if part.ToolResult != nil {
					part.ToolResult.Content = ""
					part.ToolResult.ContentJSON = nil
				}
				if part.Media != nil {
					part.Media.URL = ""
				}
			}
		}
	}
	for i := range out.Tools {
		out.Tools[i].Description = ""
		out.Tools[i].InputSchemaJSON = nil
	}
	return out, nil
}
func (w *Workload) pipelineResource(n *node) map[string]any {
	attrs := w.identity(n).resourceAttrs()
	if n.decl.Controller == "statefulset" {
		delete(attrs, "k8s.deployment.name")
		attrs["k8s.statefulset.name"] = n.decl.Name
	}
	return attrs
}
func pipelineScope(a *AgentBinding) otlp.Scope { return otlp.Scope{Name: sigil.AgentScope("", a.SDK)} }
func pipelineGenerationAttrs(g sigil.Generation, a *AgentBinding) map[string]any {
	attrs := map[string]any{genai.AttrOperationName: g.OperationName, genai.AttrProviderName: g.Provider, genai.AttrRequestModel: g.Model, genai.AttrResponseModel: g.ResponseModel, "gen_ai.response.id": g.ResponseID, "gen_ai.response.finish_reasons": []string{g.StopReason}, genai.AttrAgentName: g.AgentName, genai.AttrConversationID: g.ConversationID, sigil.AttrGenerationID: g.ID, sigil.AttrSDKName: a.SDK, genai.AttrInputTokens: g.Usage.Input, genai.AttrOutputTokens: g.Usage.Output}
	if a.Version != "" {
		attrs["gen_ai.agent.version"] = a.Version
	}
	if a.SDK == sigil.SDKPython && len(g.ParentGenerationIDs) > 0 {
		attrs[sigil.AttrParentGenerationIDs] = slices.Clone(g.ParentGenerationIDs)
	}
	if g.Usage.InputSemantics == sigil.InputSemanticsInclusive {
		attrs[sigil.AttrTokenSemantics] = sigil.TokenSemanticsInclusive
	}
	for k, v := range pipelineTags(a) {
		attrs[sigil.TagPrefix+k] = v
	}
	return attrs
}
func (w *Workload) observePipelineGeneration(n *node, g sigil.Generation) {
	rt := w.pipeline
	st := rt.states[n.decl.Name]
	if st == nil {
		st = state.NewState()
		rt.states[n.decl.Name] = st
	}
	if rt.started.IsZero() {
		rt.started = g.StartedAt
	}
	labels := map[string]string{genai.AttrOperationName: g.OperationName, genai.AttrProviderName: g.Provider, genai.AttrRequestModel: g.Model, genai.AttrAgentName: g.AgentName}
	if g.AgentVersion != "" {
		labels["gen_ai.agent.version"] = g.AgentVersion
	}
	for k, v := range pipelineTags(n.decl.AgentObservability) {
		labels[sigil.TagPrefix+k] = v
	}
	st.Observe(pipelineDurationMetric, labels, genai.OpDurationBuckets, state.LEDotZero, g.EndedAt.Sub(g.StartedAt).Seconds())
	for _, v := range []struct {
		kind   string
		tokens int64
	}{{sigil.TokenInput, g.Usage.Input}, {sigil.TokenOutput, g.Usage.Output}, {sigil.TokenCacheRead, g.Usage.CacheRead}, {sigil.TokenCacheWrite, g.Usage.CacheWrite}, {sigil.TokenReasoning, g.Usage.Reasoning}} {
		if v.tokens <= 0 {
			continue
		}
		l := map[string]string{}
		for k, x := range labels {
			l[k] = x
		}
		l["gen_ai.token.type"] = v.kind
		if g.Usage.InputSemantics == sigil.InputSemanticsInclusive {
			l[sigil.AttrTokenSemantics] = sigil.TokenSemanticsInclusive
		}
		st.Observe(pipelineTokenMetric, l, genai.TokenUsageBuckets, state.LEDotZero, float64(v.tokens))
	}
}
func (w *Workload) pipelineMetrics(now time.Time) []otlp.MetricResource {
	var out []otlp.MetricResource
	for _, n := range w.graph.nodes {
		st := w.pipeline.states[n.decl.Name]
		if st == nil {
			continue
		}
		var metrics []otlp.Metric
		for _, spec := range []struct{ name, unit string }{{pipelineDurationMetric, "s"}, {pipelineTokenMetric, "{token}"}} {
			points := nativeHistogramPoints(st.CollectHistos(), spec.name, now, w.pipeline.started)
			if len(points) > 0 {
				metrics = append(metrics, otlp.Metric{Name: spec.name, Unit: spec.unit, Kind: otlp.MetricHistogram, Temporality: otlp.TemporalityCumulative, Histograms: points})
			}
		}
		out = append(out, otlp.MetricResource{Attrs: w.pipelineResource(n), Scope: pipelineScope(n.decl.AgentObservability), Metrics: metrics})
	}
	return out
}
func (w *Workload) projectPipeline(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	if world == nil || world.EmitSpanMetrics {
		return pipelineError("local synthkit spanmetrics unsupported for agent_pipeline")
	}
	// This component cannot certify root-owned custody. Missing signal writers or
	// required collaborators must not make an apparently successful silent run.
	if world.Sigil == nil || world.Traces == nil {
		return pipelineError("pipeline requires generation and trace writers")
	}
	for _, r := range batch {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.executePipeline(ctx, world, r); err != nil {
			return err
		}
	}
	return nil
}
func (w *Workload) executePipeline(ctx context.Context, world *core.World, r *ledger.Request) error {
	p := w.cfg.AgentPipeline
	k := pipelineKind(p, r)
	outcome := pipelineOutcome(p, r)
	plan := pipelinePlan(p, k, outcome)
	base := r.RenderStart()
	duration := r.Duration
	if duration <= 0 {
		return pipelineError("positive request duration required")
	}
	end := base.Add(duration)
	stepDur := duration / time.Duration(len(plan)+1)
	if stepDur < time.Millisecond {
		return pipelineError("request duration cannot hold bounded stages")
	}
	root := w.graph.entry
	if err := world.Traces.Write(ctx, []otlp.Resource{{Attrs: w.pipelineResource(root), Spans: []otlp.Span{{Name: r.Route, TraceID: r.TraceID, SpanID: r.SpanID, Kind: otlp.KindServer, Start: base, End: end, Attrs: universalAttrs(r)}}}}); err != nil {
		return err
	}
	var carry []sigil.Message
	completed := map[string]sigil.Generation{}
	lastGen, lastStep := "", ""
	contentBytes := 0
	visits := map[int]int{}
	charge := func(v any) error {
		raw, err := json.Marshal(v)
		if err != nil || len(raw) > pipelineContentLimit-contentBytes {
			return pipelineError("modeled content exceeds 1MiB")
		}
		contentBytes += len(raw)
		return nil
	}
	terminal := outcome.Name
	var stoppedErr error
	for attempt, idx := range plan {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := p.Stages[idx]
		visits[idx]++
		n := w.graph.byName[s.Service]
		a := n.decl.AgentObservability
		c := w.pipeline.contents[s.Name]
		start := base.Add(time.Duration(attempt) * stepDur)
		stop := start.Add(stepDur)
		unit := fmt.Sprintf("pipeline/%s/%d", s.Name, attempt)
		sid := ledger.SpanIDFromSeed(r.SessionID, unit)
		parent := r.SpanID
		var resources []otlp.Resource
		caller := root
		if s.CallerStage != "" {
			for _, cs := range p.Stages {
				if cs.Name == s.CallerStage {
					caller = w.graph.byName[cs.Service]
				}
			}
		}
		if n != caller {
			clientID := ledger.SpanIDFromSeed(r.SessionID, unit+"/client")
			resources = append(resources, otlp.Resource{Attrs: w.pipelineResource(caller), Spans: []otlp.Span{{Name: "call " + s.Service, TraceID: r.TraceID, SpanID: clientID, ParentID: r.SpanID, Kind: otlp.KindClient, Start: start, End: stop, Attrs: universalAttrs(r)}}})
			parent = clientID
		}
		resources = append(resources, otlp.Resource{Attrs: w.pipelineResource(n), Spans: []otlp.Span{{Name: s.Name, TraceID: r.TraceID, SpanID: sid, ParentID: parent, Kind: otlp.KindServer, Start: start, End: stop, Attrs: universalAttrs(r)}}})
		step := sigil.WorkflowStep{ID: ledger.SpanIDFromSeed(r.SessionID, unit+"/step"), ConversationID: r.SessionID, StepName: s.Name, Framework: p.Name, AgentName: a.Name, AgentVersion: a.Version, TraceID: r.TraceID, SpanID: sid, StartedAt: start, EndedAt: stop, OutputState: map[string]any{}}
		if lastStep != "" {
			step.ParentStepIDs = []string{lastStep}
		}
		if s.Name == p.Review.Stage {
			step.OutputState["review_passed"] = outcome.ReviewPassed && visits[idx] > outcome.Revisions
		}
		if s.Operation == "generation" {
			genID := ledger.SpanIDFromSeed(r.SessionID, unit+"/gen")
			genSpan := ledger.SpanIDFromSeed(r.SessionID, unit+"/generation-span")
			gen := sigil.Generation{ID: genID, ConversationID: r.SessionID, TraceID: r.TraceID, SpanID: genSpan, OperationName: sigil.OpGenerateText, Mode: sigil.ModeSync, AgentName: a.Name, AgentVersion: a.Version, Provider: a.Provider, Model: a.Models[pipelinePick(r.SpanID, unit+"/model", len(a.Models))], SystemPrompt: c.system, StartedAt: start, EndedAt: stop, StopReason: "end_turn", Metadata: pipelineClone(a.Metadata), Tags: pipelineTags(a)}
			if gen.Metadata == nil {
				gen.Metadata = map[string]any{}
			}
			gen.Metadata[sigil.AttrSDKName] = a.SDK
			for key, v := range a.GenerationTags {
				gen.Tags[key] = v
			}
			gen.ResponseModel = gen.Model
			gen.ResponseID = ledger.SpanIDFromSeed(genID, "response")
			gen.EffectiveVersion = sigil.EffectiveVersion(gen.SystemPrompt)
			if lastGen != "" {
				gen.ParentGenerationIDs = []string{lastGen}
			}
			gen.Input = append(pipelineClone(carry), pipelineMessage(c.users[pipelinePick(r.SpanID, unit+"/user", len(c.users))], sigil.RoleUser))
			gen.Output = []sigil.Message{pipelineMessage(c.assistants[pipelinePick(r.SpanID, unit+"/assistant", len(c.assistants))], sigil.RoleAssistant)}
			for _, tool := range a.Tools {
				gen.Tools = append(gen.Tools, sigil.ToolDef{Name: tool.Name, Type: "function"})
			}
			// Only actual admitted retrieval stages create tool calls. Declaring a second
			// available tool does not select it or invoke the hook path.
			for _, next := range p.Stages {
				if next.CallerStage != s.Name || !stageEnabled(next, k) {
					continue
				}
				for _, tool := range w.graph.byName[next.Service].decl.AgentObservability.Tools {
					if !pipelineToolSelected(a, tool.Name) {
						continue
					}
					id := ledger.SpanIDFromSeed(genID, "tool/"+next.Name+"/"+tool.Name)
					gen.Output[0].Parts = append(gen.Output[0].Parts, sigil.Part{ProviderType: "tool_call", ToolCall: &sigil.ToolCall{ID: id, Name: tool.Name, InputJSON: []byte("{}")}})
					gen.StopReason = "tool_use"
				}
			}
			// Content/token sizes are synthetic modeling, not vendor tokenizer counts:
			// one token per four serialized bytes is an explicit modest approximation.
			in, _ := json.Marshal(gen.Input)
			output, _ := json.Marshal(gen.Output)
			gen.Usage = sigil.Usage{Input: int64((len(in)+len(c.system))/4 + 1), Output: int64(len(output)/4 + 1)}
			gen.Usage.Total = gen.Usage.Input + gen.Usage.Output
			if a.SDK == sigil.SDKPython {
				gen.Usage.InputSemantics = sigil.InputSemanticsInclusive
			}
			if err := charge(gen); err != nil {
				return err
			}
			// Count completed execution, not successful publication. A later
			// callback/sink failure cannot erase work that actually completed.
			w.observePipelineGeneration(n, gen)
			if err := w.b.GenerationStages.AfterGeneration(ctx, pipelineClone(gen)); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			// No caller can reuse a reference handed to the adapter for carry/export.
			completed[s.Name] = pipelineClone(gen)
			lastGen = gen.ID
			captured, err := pipelineCaptureGeneration(gen, a.CaptureMode)
			if err != nil {
				return err
			}
			if err := world.Sigil.Write(ctx, []sigil.Export{{ConvKey: r.SessionID, Generations: []sigil.Generation{captured}}}); err != nil {
				return err
			}
			resources = append(resources, otlp.Resource{Attrs: w.pipelineResource(n), Scope: pipelineScope(a), Spans: []otlp.Span{{Name: sigil.OpGenerateText + " " + gen.Model, TraceID: r.TraceID, SpanID: genSpan, ParentID: sid, Kind: otlp.KindClient, Start: start, End: stop, Attrs: pipelineGenerationAttrs(gen, a)}}})
			step.LinkedGenerationIDs = []string{gen.ID}
			carry = append(carry, pipelineClone(gen.Output)...)
		} else {
			producer, ok := completed[s.CallerStage]
			if !ok {
				return pipelineError("retrieval producer not completed")
			}
			policy := caller.decl.AgentObservability.Hooks
			for _, tool := range a.Tools {
				if !pipelineToolSelected(caller.decl.AgentObservability, tool.Name) {
					continue
				}
				candidate := c.tools[tool.Name][pipelinePick(r.SpanID, unit+"/tool/"+tool.Name, len(c.tools[tool.Name]))]
				if len(candidate.Content) > pipelineContentLimit-contentBytes {
					return pipelineError("tool result exceeds request content budget")
				}
				original := sigil.ToolResult{Name: candidate.Name, Content: candidate.Content, IsError: candidate.IsError}
				// Both forms of a JSON-valued tool are produced here and cross
				// the identical preflight barrier; no decoded result is prebuilt.
				if json.Valid([]byte(candidate.Content)) {
					original.ContentJSON = []byte(candidate.Content)
				}
				original.ToolCallID = ledger.SpanIDFromSeed(producer.ID, "tool/"+s.Name+"/"+tool.Name)
				if err := charge(original); err != nil {
					return err
				}
				accepted := pipelineClone(original)
				allowed := true
				if policy.Enabled {
					hookCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.TimeoutMS)*time.Millisecond)
					decision, err := w.b.GenerationStages.BeforeToolResult(hookCtx, sigil.GenerationStage{GenerationID: producer.ID, ConversationID: r.SessionID, TraceID: r.TraceID, SpanID: producer.SpanID, AgentName: producer.AgentName, AgentVersion: producer.AgentVersion, Provider: producer.Provider, Model: producer.Model}, pipelineClone(original))
					if err == nil && decision.Allowed && hookCtx.Err() != nil {
						err = hookCtx.Err()
					}
					cancel()
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if err != nil {
						allowed = policy.FailOpen
						if !allowed {
							stoppedErr = err
						}
					} else {
						allowed = decision.Allowed
						if allowed && decision.Transformed != nil {
							if decision.Transformed.Name != original.Name || decision.Transformed.ToolCallID != original.ToolCallID {
								return pipelineError("transformation changed tool identity")
							}
							tr := decision.Transformed
							if len(tr.Content) > pipelineContentLimit-contentBytes || len(tr.ContentJSON) > pipelineContentLimit-contentBytes-len(tr.Content) {
								return pipelineError("transformed content exceeds request budget")
							}
							if len(tr.ContentJSON) > 0 && !json.Valid(tr.ContentJSON) {
								return pipelineError("invalid transformed content_json")
							}
							accepted = pipelineClone(*tr)
						}
					}
				}
				if !allowed {
					terminal = p.EscalatedOutcome
					step.Error = "retrieval denied"
					break
				}
				if err := charge(accepted); err != nil {
					return err
				}
				carry = append(carry, sigil.Message{Role: sigil.RoleTool, Name: accepted.Name, Parts: []sigil.Part{{ProviderType: "tool_result", ToolResult: &accepted}}})
				attrs := map[string]any{genai.AttrOperationName: sigil.OpExecuteTool, genai.AttrToolName: tool.Name, "gen_ai.tool.call.id": accepted.ToolCallID, genai.AttrAgentName: a.Name, genai.AttrConversationID: r.SessionID, sigil.AttrSDKName: a.SDK}
				if a.CaptureMode == sigil.CaptureFull {
					// Full includes the actual completed planner call's arguments,
					// never a separately invented tool payload. Other modes have
					// no IncludeContent opt-in on this component surface.
					for _, message := range producer.Output {
						for _, part := range message.Parts {
							if call := part.ToolCall; call != nil && call.ID == accepted.ToolCallID && call.Name == accepted.Name && len(call.InputJSON) > 0 {
								attrs["gen_ai.tool.call.arguments"] = string(call.InputJSON)
							}
						}
					}
					if accepted.Content != "" {
						attrs["gen_ai.tool.call.result"] = accepted.Content
					} else if len(accepted.ContentJSON) > 0 {
						attrs["gen_ai.tool.call.result"] = string(accepted.ContentJSON)
					}
				}
				resources = append(resources, otlp.Resource{Attrs: w.pipelineResource(n), Scope: pipelineScope(a), Spans: []otlp.Span{{Name: sigil.OpExecuteTool + " " + tool.Name, TraceID: r.TraceID, SpanID: ledger.SpanIDFromSeed(r.SessionID, unit+"/tool-span/"+tool.Name), ParentID: sid, Kind: otlp.KindInternal, Start: start, End: stop, Attrs: attrs}}})
			}
		}
		if err := world.Traces.Write(ctx, resources); err != nil {
			return err
		}
		if err := world.Sigil.Write(ctx, []sigil.Export{{ConvKey: r.SessionID, WorkflowSteps: []sigil.WorkflowStep{step}}}); err != nil {
			return err
		}
		lastStep = step.ID
		if step.Error != "" {
			break
		}
	}
	terminalStep := sigil.WorkflowStep{ID: ledger.SpanIDFromSeed(r.SessionID, "pipeline/terminal"), ConversationID: r.SessionID, StepName: p.Name, Framework: p.Name, AgentName: root.decl.AgentObservability.Name, AgentVersion: root.decl.AgentObservability.Version, TraceID: r.TraceID, SpanID: r.SpanID, StartedAt: base, EndedAt: end, OutputState: map[string]any{"outcome": terminal, "request_kind": k}}
	if lastStep != "" {
		terminalStep.ParentStepIDs = []string{lastStep}
	}
	if err := world.Sigil.Write(ctx, []sigil.Export{{ConvKey: r.SessionID, WorkflowSteps: []sigil.WorkflowStep{terminalStep}}}); err != nil {
		return err
	}
	if pipelineDraw(r.SpanID, "rating") < p.Ratings.SampleRate {
		value := sigil.ConversationRatingValueGood
		if terminal == p.EscalatedOutcome || !outcome.ReviewPassed {
			value = sigil.ConversationRatingValueBad
		}
		rating := sigil.ConversationRatingInput{RatingID: ledger.SpanIDFromSeed(r.SessionID, "pipeline/rating"), Rating: value, GenerationID: lastGen}
		if err := w.b.Ratings.WriteConversationRating(ctx, r.SessionID, rating); err != nil {
			return err
		}
	}
	return stoppedErr
}
