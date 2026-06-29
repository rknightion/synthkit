// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	sigilv1 "github.com/rknightion/synthkit/internal/sink/sigil/v1"
)

// modeEnum maps native Mode strings to the proto GenerationMode enum.
var modeEnum = map[string]sigilv1.GenerationMode{
	"SYNC":   sigilv1.GenerationMode_GENERATION_MODE_SYNC,
	"STREAM": sigilv1.GenerationMode_GENERATION_MODE_STREAM,
}

// roleEnum maps native Role strings to the proto MessageRole enum.
var roleEnum = map[string]sigilv1.MessageRole{
	"MESSAGE_ROLE_USER":      sigilv1.MessageRole_MESSAGE_ROLE_USER,
	"MESSAGE_ROLE_ASSISTANT": sigilv1.MessageRole_MESSAGE_ROLE_ASSISTANT,
	"MESSAGE_ROLE_TOOL":      sigilv1.MessageRole_MESSAGE_ROLE_TOOL,
	// short-form aliases used by the mechanic lib
	"user":      sigilv1.MessageRole_MESSAGE_ROLE_USER,
	"assistant": sigilv1.MessageRole_MESSAGE_ROLE_ASSISTANT,
	"tool":      sigilv1.MessageRole_MESSAGE_ROLE_TOOL,
}

// toProtoGenerations maps a slice of native Generation structs to proto Generation messages.
func toProtoGenerations(gens []nativesigil.Generation) []*sigilv1.Generation {
	if len(gens) == 0 {
		return nil
	}
	out := make([]*sigilv1.Generation, len(gens))
	for i, g := range gens {
		pg := &sigilv1.Generation{
			Id:             g.ID,
			ConversationId: g.ConversationID,
			OperationName:  g.OperationName,
			Mode:           toProtoMode(g.Mode),
			TraceId:        g.TraceID,
			SpanId:         g.SpanID,
			ResponseId:     g.ResponseID,
			ResponseModel:  g.ResponseModel,
			SystemPrompt:   g.SystemPrompt,
			StopReason:     g.StopReason,
			CallError:      g.CallError,
			AgentName:      g.AgentName,
			AgentVersion:   g.AgentVersion,
		}

		if g.Provider != "" || g.Model != "" {
			pg.Model = &sigilv1.ModelRef{
				Provider: g.Provider,
				Name:     g.Model,
			}
		}

		pg.Input = toProtoMessages(g.Input)
		pg.Output = toProtoMessages(g.Output)
		pg.Tools = toProtoToolDefs(g.Tools)

		pg.Usage = &sigilv1.TokenUsage{
			InputTokens:           g.Usage.Input,
			OutputTokens:          g.Usage.Output,
			TotalTokens:           g.Usage.Total,
			CacheReadInputTokens:  g.Usage.CacheRead,
			CacheWriteInputTokens: g.Usage.CacheWrite,
			ReasoningTokens:       g.Usage.Reasoning,
		}

		if !g.StartedAt.IsZero() {
			pg.StartedAt = timestamppb.New(g.StartedAt)
		}
		if !g.EndedAt.IsZero() {
			pg.CompletedAt = timestamppb.New(g.EndedAt)
		}

		pg.Tags = g.Tags

		if len(g.Metadata) > 0 {
			s, err := toStruct(g.Metadata)
			if err == nil {
				pg.Metadata = s
			}
		}

		// optional scalars — set only when non-nil
		if g.MaxTokens != nil {
			pg.MaxTokens = g.MaxTokens
		}
		if g.Temperature != nil {
			pg.Temperature = g.Temperature
		}
		if g.TopP != nil {
			pg.TopP = g.TopP
		}
		if g.ToolChoice != nil {
			pg.ToolChoice = g.ToolChoice
		}
		if g.ThinkingEnabled != nil {
			pg.ThinkingEnabled = g.ThinkingEnabled
		}

		if len(g.ParentGenerationIDs) > 0 {
			pg.ParentGenerationIds = g.ParentGenerationIDs
		}

		if g.EffectiveVersion != "" {
			pg.EffectiveVersion = &g.EffectiveVersion
		}

		out[i] = pg
	}
	return out
}

// toProtoWorkflowSteps maps a slice of native WorkflowStep structs to proto WorkflowStep messages.
func toProtoWorkflowSteps(steps []nativesigil.WorkflowStep) []*sigilv1.WorkflowStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]*sigilv1.WorkflowStep, len(steps))
	for i, s := range steps {
		ps := &sigilv1.WorkflowStep{
			Id:             s.ID,
			ConversationId: s.ConversationID,
			StepName:       s.StepName,
			Framework:      s.Framework,
			Error:          s.Error,
			AgentName:      s.AgentName,
			AgentVersion:   s.AgentVersion,
			TraceId:        s.TraceID,
			SpanId:         s.SpanID,
		}

		if !s.StartedAt.IsZero() {
			ps.StartedAt = timestamppb.New(s.StartedAt)
		}
		if !s.EndedAt.IsZero() {
			ps.CompletedAt = timestamppb.New(s.EndedAt)
		}

		if len(s.InputState) > 0 {
			st, err := toStruct(s.InputState)
			if err == nil {
				ps.InputState = st
			}
		}
		if len(s.OutputState) > 0 {
			st, err := toStruct(s.OutputState)
			if err == nil {
				ps.OutputState = st
			}
		}

		ps.Tags = s.Tags

		if len(s.LinkedGenerationIDs) > 0 {
			ps.LinkedGenerationIds = s.LinkedGenerationIDs
		}
		if len(s.ParentStepIDs) > 0 {
			ps.ParentStepIds = s.ParentStepIDs
		}

		if len(s.Metadata) > 0 {
			st, err := toStruct(s.Metadata)
			if err == nil {
				ps.Metadata = st
			}
		}

		out[i] = ps
	}
	return out
}

// toProtoScores maps a slice of native Score structs to proto ScoreItem messages.
func toProtoScores(scores []nativesigil.Score) []*sigilv1.ScoreItem {
	if len(scores) == 0 {
		return nil
	}
	out := make([]*sigilv1.ScoreItem, len(scores))
	for i, sc := range scores {
		ps := &sigilv1.ScoreItem{
			ScoreId:              sc.ScoreID,
			GenerationId:         sc.GenerationID,
			ConversationId:       sc.ConversationID,
			TraceId:              sc.TraceID,
			SpanId:               sc.SpanID,
			EvaluatorId:          sc.EvaluatorID,
			EvaluatorVersion:     sc.EvaluatorVersion,
			RuleId:               sc.RuleID,
			ExperimentId:         sc.ExperimentID,
			ScoreKey:             sc.ScoreKey,
			HasPassed:            sc.HasPassed,
			Passed:               sc.Passed,
			Explanation:          sc.Explanation,
			TrialId:              sc.TrialID,
			TestCaseId:           sc.TestCaseID,
			GraderConversationId: sc.GraderConversationID,
			GraderGenerationId:   sc.GraderGenerationID,
			GraderTraceId:        sc.GraderTraceID,
		}

		// ScoreValue oneof: Number takes precedence, then Bool, then String
		ps.Value = toProtoScoreValue(sc)

		if !sc.CreatedAt.IsZero() {
			ps.CreatedAt = timestamppb.New(sc.CreatedAt)
		}

		if sc.Source.Kind != "" || sc.Source.ID != "" {
			ps.Source = &sigilv1.ScoreSource{
				Kind: sc.Source.Kind,
				Id:   sc.Source.ID,
			}
		}

		if len(sc.Metadata) > 0 {
			st, err := toStruct(sc.Metadata)
			if err == nil {
				ps.Metadata = st
			}
		}

		out[i] = ps
	}
	return out
}

// toProtoMode converts a native mode string to the proto GenerationMode enum.
func toProtoMode(mode string) sigilv1.GenerationMode {
	if v, ok := modeEnum[mode]; ok {
		return v
	}
	return sigilv1.GenerationMode_GENERATION_MODE_UNSPECIFIED
}

// toProtoRole converts a native role string to the proto MessageRole enum.
func toProtoRole(role string) sigilv1.MessageRole {
	if v, ok := roleEnum[role]; ok {
		return v
	}
	return sigilv1.MessageRole_MESSAGE_ROLE_UNSPECIFIED
}

// toProtoMessages maps a slice of native Messages to proto Messages.
func toProtoMessages(msgs []nativesigil.Message) []*sigilv1.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]*sigilv1.Message, len(msgs))
	for i, m := range msgs {
		pm := &sigilv1.Message{
			Role:  toProtoRole(m.Role),
			Name:  m.Name,
			Parts: toProtoParts(m.Parts),
		}
		out[i] = pm
	}
	return out
}

// toProtoParts maps a slice of native Parts to proto Parts.
func toProtoParts(parts []nativesigil.Part) []*sigilv1.Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]*sigilv1.Part, len(parts))
	for i, p := range parts {
		pp := &sigilv1.Part{}
		if p.ProviderType != "" {
			pp.Metadata = &sigilv1.PartMetadata{ProviderType: p.ProviderType}
		}

		switch {
		case p.ToolCall != nil:
			pp.Payload = &sigilv1.Part_ToolCall{
				ToolCall: &sigilv1.ToolCall{
					Id:        p.ToolCall.ID,
					Name:      p.ToolCall.Name,
					InputJson: p.ToolCall.InputJSON,
				},
			}
		case p.ToolResult != nil:
			pp.Payload = &sigilv1.Part_ToolResult{
				ToolResult: &sigilv1.ToolResult{
					ToolCallId:  p.ToolResult.ToolCallID,
					Name:        p.ToolResult.Name,
					Content:     p.ToolResult.Content,
					ContentJson: p.ToolResult.ContentJSON,
					IsError:     p.ToolResult.IsError,
				},
			}
		case p.Thinking != "":
			pp.Payload = &sigilv1.Part_Thinking{Thinking: p.Thinking}
		default:
			pp.Payload = &sigilv1.Part_Text{Text: p.Text}
		}

		out[i] = pp
	}
	return out
}

// toProtoToolDefs maps a slice of native ToolDefs to proto ToolDefinitions.
func toProtoToolDefs(tools []nativesigil.ToolDef) []*sigilv1.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	out := make([]*sigilv1.ToolDefinition, len(tools))
	for i, t := range tools {
		out[i] = &sigilv1.ToolDefinition{
			Name:            t.Name,
			Description:     t.Description,
			Type:            t.Type,
			InputSchemaJson: t.InputSchemaJSON,
		}
	}
	return out
}

// toProtoScoreValue builds the ScoreValue oneof from a native Score.
// Priority: Number (*float64) > Bool (*bool) > String (non-empty).
func toProtoScoreValue(sc nativesigil.Score) *sigilv1.ScoreValue {
	if sc.Number != nil {
		return &sigilv1.ScoreValue{Value: &sigilv1.ScoreValue_Number{Number: *sc.Number}}
	}
	if sc.Bool != nil {
		return &sigilv1.ScoreValue{Value: &sigilv1.ScoreValue_Bool{Bool: *sc.Bool}}
	}
	if sc.String != "" {
		return &sigilv1.ScoreValue{Value: &sigilv1.ScoreValue_String_{String_: sc.String}}
	}
	return nil
}

// toStruct converts a map[string]any to a structpb.Struct, ignoring conversion errors for
// individual values that are not structpb-compatible.
func toStruct(m map[string]any) (*structpb.Struct, error) {
	return structpb.NewStruct(m)
}
