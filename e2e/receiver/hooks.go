// SPDX-License-Identifier: AGPL-3.0-only

package receiver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file is the deterministic e2e stand-in for two Agent Observability request/response
// endpoints: POST /api/v1/hooks:evaluate and the conversation rating routes. It decides only
// from the request content, with no clock or randomness in a decision, so an e2e run is
// reproducible.
//
// It is a test double, not the backend. It emits NO telemetry: no backend hook metric series, no
// guard workflow step and no score, because the real backend owns every one of them. Its
// wire shapes follow the SDK conformance fixtures (SDK 75a1c548, conformance/hooks) and the
// server decoder/encoder at backend bc963fb09: requests carry embedded JSON for tool
// payloads, responses carry the same payloads as base64.

// Receipt names for the two lanes. Counts only; no content.
const (
	receiptSigilHookEvaluations = "sigil_hook_evaluations"
	receiptSigilRatings         = "sigil_conversation_ratings"
)

// Rule and evaluator identities in a deterministic response. They are the double's own names.
const (
	HookRuleDeny        = "e2e-hook-deny"
	HookRuleRedact      = "e2e-hook-redact"
	hookEvaluatorDeny   = "e2e-evaluator-deny"
	hookEvaluatorRedact = "e2e-evaluator-redact"
	hookRedactedText    = "[REDACTED]"
)

// HookPolicy is the whole deterministic rule set. Deny wins over redact, as a deny does in the
// backend. A nil pattern disables that rule.
type HookPolicy struct {
	// DenyPattern denies the request when it matches any text-bearing field of the input.
	DenyPattern *regexp.Regexp
	// RedactPattern replaces every match with [REDACTED] in all of those fields and returns
	// the whole redacted input as transformed_input, the way the backend echoes its working copy.
	RedactPattern *regexp.Regexp
}

// DefaultHookPolicy denies the literal marker [e2e-deny] and redacts subject identifiers of
// the shape SUBJ-NNNNNN.
func DefaultHookPolicy() HookPolicy {
	return HookPolicy{
		DenyPattern:   regexp.MustCompile(`\[e2e-deny\]`),
		RedactPattern: regexp.MustCompile(`SUBJ-[0-9]{6}`),
	}
}

// HookCall is the receiver's record of one evaluation. It deliberately holds identities and
// the decision only, never request content, so GET /__sigil_hooks cannot leak what a rule
// denied or redacted.
type HookCall struct {
	Tenant         string        `json:"tenant"`
	Phase          string        `json:"phase"`
	AgentName      string        `json:"agent_name,omitempty"`
	AgentVersion   string        `json:"agent_version,omitempty"`
	ModelProvider  string        `json:"model_provider,omitempty"`
	ModelName      string        `json:"model_name,omitempty"`
	ConversationID string        `json:"conversation_id,omitempty"`
	TraceID        string        `json:"trace_id,omitempty"`
	SpanID         string        `json:"span_id,omitempty"`
	TimeoutMS      string        `json:"timeout_ms,omitempty"`
	ToolResults    []HookToolRef `json:"tool_results,omitempty"`
	Decision       string        `json:"decision"` // allow | deny | redact
	RuleID         string        `json:"rule_id,omitempty"`
}

// HookToolRef names one tool result in a request.
type HookToolRef struct {
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// guardState holds the double's own state behind its own lock, so the existing Receiver
// state and its lock are untouched.
type guardState struct {
	mu      sync.Mutex
	policy  *HookPolicy
	calls   []HookCall
	ratings map[string]*storedRating   // tenant::rating_id
	byConv  map[string][]*storedRating // tenant::conversation_id, newest first
	summary map[string]*ratingSummary  // tenant::conversation_id
	now     func() time.Time
}

type storedRating struct {
	RatingID       string         `json:"rating_id"`
	ConversationID string         `json:"conversation_id"`
	GenerationID   string         `json:"generation_id,omitempty"`
	Rating         string         `json:"rating"`
	Comment        string         `json:"comment,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RaterID        string         `json:"rater_id,omitempty"`
	Source         string         `json:"source,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type ratingSummary struct {
	TotalCount    int       `json:"total_count"`
	GoodCount     int       `json:"good_count"`
	BadCount      int       `json:"bad_count"`
	LatestRating  string    `json:"latest_rating,omitempty"`
	LatestRatedAt time.Time `json:"latest_rated_at"`
	LatestBadAt   time.Time `json:"latest_bad_at,omitzero"`
	HasBadRating  bool      `json:"has_bad_rating"`
}

// SetHookPolicy replaces the deterministic rule set. The zero HookPolicy allows everything.
func (r *Receiver) SetHookPolicy(p HookPolicy) {
	r.guard.mu.Lock()
	defer r.guard.mu.Unlock()
	r.guard.policy = &p
}

// HookCalls returns a copy of the evaluations received so far, in arrival order.
func (r *Receiver) HookCalls() []HookCall {
	r.guard.mu.Lock()
	defer r.guard.mu.Unlock()
	out := make([]HookCall, len(r.guard.calls))
	for i, call := range r.guard.calls {
		call.ToolResults = append([]HookToolRef(nil), call.ToolResults...)
		out[i] = call
	}
	return out
}

func (g *guardState) activePolicy() HookPolicy {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.policy != nil {
		return *g.policy
	}
	return DefaultHookPolicy()
}

func (g *guardState) clock() time.Time {
	if g.now != nil {
		return g.now().UTC()
	}
	return time.Now().UTC()
}

func (r *Receiver) registerGuardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/hooks:evaluate", r.handleHooksEvaluate)
	mux.HandleFunc("POST /api/v1/conversations/{id}/ratings", r.handleRatingCreate)
	mux.HandleFunc("GET /api/v1/conversations/{id}/ratings", r.handleRatingList)
	mux.HandleFunc("GET /__sigil_hooks", r.handleHookCalls)
}

// requestTenant resolves the tenant the way the backend does: it must be authenticated.
func requestTenant(req *http.Request) (string, bool) {
	if tenant := strings.TrimSpace(req.Header.Get("X-Scope-OrgID")); tenant != "" {
		return tenant, true
	}
	if user, _, ok := req.BasicAuth(); ok && strings.TrimSpace(user) != "" {
		return strings.TrimSpace(user), true
	}
	return "", false
}

// Wire types for the hook body. The request reads tool payloads as embedded JSON; the response
// writes them as base64 strings.

type guardRequest struct {
	Phase   string `json:"phase"`
	Context struct {
		AgentName    string `json:"agent_name"`
		AgentVersion string `json:"agent_version"`
		Model        *struct {
			Provider string `json:"provider"`
			Name     string `json:"name"`
		} `json:"model"`
		ConversationID string `json:"conversation_id"`
		TraceID        string `json:"trace_id"`
		SpanID         string `json:"span_id"`
	} `json:"context"`
	Input guardInput `json:"input"`
}

type guardInput struct {
	Messages            []guardMessage    `json:"messages,omitempty"`
	Tools               []json.RawMessage `json:"tools,omitempty"`
	SystemPrompt        string            `json:"system_prompt,omitempty"`
	Output              []guardMessage    `json:"output,omitempty"`
	ConversationPreview string            `json:"conversation_preview,omitempty"`
}

type guardMessage struct {
	Role  string      `json:"role"`
	Name  string      `json:"name,omitempty"`
	Parts []guardPart `json:"parts,omitempty"`
}

type guardPart struct {
	Kind       string           `json:"kind,omitempty"`
	Text       string           `json:"text,omitempty"`
	Thinking   string           `json:"thinking,omitempty"`
	ToolCall   *guardToolCall   `json:"tool_call,omitempty"`
	ToolResult *guardToolResult `json:"tool_result,omitempty"`
}

type guardToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	InputJSON json.RawMessage `json:"input_json,omitempty"`
}

type guardToolResult struct {
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	IsError     bool            `json:"is_error,omitempty"`
	Content     string          `json:"content,omitempty"`
	ContentJSON json.RawMessage `json:"content_json,omitempty"`
}

type guardEvaluation struct {
	RuleID        string `json:"rule_id"`
	EvaluatorID   string `json:"evaluator_id"`
	EvaluatorKind string `json:"evaluator_kind"`
	Passed        bool   `json:"passed"`
	LatencyMs     int64  `json:"latency_ms"`
	Explanation   string `json:"explanation,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type guardResponse struct {
	Action           string            `json:"action"`
	RuleID           string            `json:"rule_id,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	TransformedInput *guardInput       `json:"transformed_input,omitempty"`
	Evaluations      []guardEvaluation `json:"evaluations"`
}

const maxGuardBody = 4 << 20

func (r *Receiver) handleHooksEvaluate(w http.ResponseWriter, req *http.Request) {
	tenant, ok := requestTenant(req)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, maxGuardBody+1))
	if err != nil || len(data) > maxGuardBody {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	var in guardRequest
	if err := json.Unmarshal(data, &in); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	policy := r.guard.activePolicy()

	deny := scanInput(in.Input, func(s string) bool { return policy.DenyPattern != nil && policy.DenyPattern.MatchString(s) })
	call := HookCall{
		Tenant: tenant, Phase: in.Phase,
		AgentName: in.Context.AgentName, AgentVersion: in.Context.AgentVersion,
		ConversationID: in.Context.ConversationID, TraceID: in.Context.TraceID, SpanID: in.Context.SpanID,
		TimeoutMS: req.Header.Get("X-Agento11y-Hook-Timeout-Ms"),
		Decision:  "allow",
	}
	if in.Context.Model != nil {
		call.ModelProvider, call.ModelName = in.Context.Model.Provider, in.Context.Model.Name
	}
	for _, m := range append(append([]guardMessage(nil), in.Input.Messages...), in.Input.Output...) {
		for _, p := range m.Parts {
			if p.ToolResult != nil {
				call.ToolResults = append(call.ToolResults, HookToolRef{ToolCallID: p.ToolResult.ToolCallID, Name: p.ToolResult.Name})
			}
		}
	}

	passDeny := guardEvaluation{RuleID: HookRuleDeny, EvaluatorID: hookEvaluatorDeny, EvaluatorKind: "regex", Passed: true, Explanation: "no deny match"}
	var resp guardResponse
	switch {
	case deny:
		call.Decision, call.RuleID = "deny", HookRuleDeny
		resp = guardResponse{
			Action: "deny", RuleID: HookRuleDeny, Reason: "deterministic e2e deny rule matched",
			Evaluations: []guardEvaluation{{RuleID: HookRuleDeny, EvaluatorID: hookEvaluatorDeny, EvaluatorKind: "regex", Reason: "deny pattern matched"}},
		}
	default:
		resp = guardResponse{Action: "allow", Evaluations: []guardEvaluation{passDeny}}
		if policy.RedactPattern != nil {
			redacted, changed := redactInput(in.Input, policy.RedactPattern)
			if changed {
				call.Decision, call.RuleID = "redact", HookRuleRedact
				resp.RuleID = HookRuleRedact
				resp.TransformedInput = encodeResponseInput(redacted)
				resp.Evaluations = append(resp.Evaluations, guardEvaluation{RuleID: HookRuleRedact, EvaluatorID: hookEvaluatorRedact, EvaluatorKind: "regex", Passed: true, Explanation: "redacted matching text"})
			}
		}
	}

	r.guard.mu.Lock()
	r.guard.calls = append(r.guard.calls, call)
	r.guard.mu.Unlock()
	r.mu.Lock()
	r.inv.AddReceipt(receiptSigilHookEvaluations, 1)
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// scanInput reports whether match holds for any text-bearing field of in.
func scanInput(in guardInput, match func(string) bool) bool {
	hit := match(in.SystemPrompt) || match(in.ConversationPreview)
	for _, m := range append(append([]guardMessage(nil), in.Messages...), in.Output...) {
		for _, p := range m.Parts {
			hit = hit || match(p.Text) || match(p.Thinking)
			if p.ToolCall != nil {
				hit = hit || match(compactJSON(p.ToolCall.InputJSON))
			}
			if p.ToolResult != nil {
				hit = hit || match(p.ToolResult.Content) || match(compactJSON(p.ToolResult.ContentJSON))
			}
		}
	}
	return hit
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// redactInput returns a copy of in with every pattern match replaced, and whether anything
// changed. Embedded JSON payloads are redacted as compact text; if that leaves them
// unparsable they collapse to {"redacted":"[REDACTED]"}, as the backend does.
func redactInput(in guardInput, pattern *regexp.Regexp) (guardInput, bool) {
	changed := false
	text := func(s string) string {
		out := pattern.ReplaceAllLiteralString(s, hookRedactedText)
		if out != s {
			changed = true
		}
		return out
	}
	payload := func(raw json.RawMessage) json.RawMessage {
		if len(raw) == 0 {
			return nil
		}
		before := compactJSON(raw)
		after := text(before)
		if after == before {
			return json.RawMessage(before)
		}
		if !json.Valid([]byte(after)) {
			return json.RawMessage(`{"redacted":"` + hookRedactedText + `"}`)
		}
		return json.RawMessage(after)
	}
	messages := func(src []guardMessage) []guardMessage {
		out := make([]guardMessage, len(src))
		for i, m := range src {
			out[i] = guardMessage{Role: m.Role, Name: m.Name, Parts: make([]guardPart, len(m.Parts))}
			for j, p := range m.Parts {
				np := guardPart{Kind: p.Kind, Text: text(p.Text), Thinking: text(p.Thinking)}
				if p.ToolCall != nil {
					tc := *p.ToolCall
					tc.InputJSON = payload(tc.InputJSON)
					np.ToolCall = &tc
				}
				if p.ToolResult != nil {
					tr := *p.ToolResult
					tr.Content = text(tr.Content)
					tr.ContentJSON = payload(tr.ContentJSON)
					np.ToolResult = &tr
				}
				out[i].Parts[j] = np
			}
		}
		return out
	}
	out := guardInput{
		Messages:            messages(in.Messages),
		Tools:               in.Tools,
		SystemPrompt:        text(in.SystemPrompt),
		Output:              messages(in.Output),
		ConversationPreview: text(in.ConversationPreview),
	}
	if len(in.Messages) == 0 {
		out.Messages = nil
	}
	if len(in.Output) == 0 {
		out.Output = nil
	}
	return out, changed
}

// encodeResponseInput converts embedded JSON payloads to the base64 strings the server writes.
func encodeResponseInput(in guardInput) *guardInput {
	enc := func(raw json.RawMessage) json.RawMessage {
		if len(raw) == 0 {
			return nil
		}
		quoted, _ := json.Marshal(base64.StdEncoding.EncodeToString(raw))
		return quoted
	}
	convert := func(src []guardMessage) []guardMessage {
		for i := range src {
			for j := range src[i].Parts {
				if tc := src[i].Parts[j].ToolCall; tc != nil {
					tc.InputJSON = enc(tc.InputJSON)
				}
				if tr := src[i].Parts[j].ToolResult; tr != nil {
					tr.ContentJSON = enc(tr.ContentJSON)
				}
			}
		}
		return src
	}
	in.Messages = convert(in.Messages)
	in.Output = convert(in.Output)
	return &in
}

func (r *Receiver) handleHookCalls(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(r.HookCalls()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Rating limits and validation mirror sigil/internal/feedback at backend bc963fb09.
const (
	ratingGood              = "CONVERSATION_RATING_VALUE_GOOD"
	ratingBad               = "CONVERSATION_RATING_VALUE_BAD"
	ratingDefaultPageLimit  = 50
	ratingMaxPageLimit      = 200
	ratingMaxBodyBytes      = 1 << 20
	ratingMaxConversationID = 255
	ratingMaxRatingID       = 128
	ratingMaxGenerationID   = 255
	ratingMaxActorID        = 255
	ratingMaxSource         = 64
	ratingMaxComment        = 4096
	ratingMaxMetadata       = 16 * 1024
)

type ratingInput struct {
	RatingID     string         `json:"rating_id"`
	Rating       string         `json:"rating"`
	Comment      string         `json:"comment"`
	Metadata     map[string]any `json:"metadata"`
	GenerationID string         `json:"generation_id"`
	RaterID      string         `json:"rater_id"`
	Source       string         `json:"source"`
}

var errRatingConflict = errors.New("idempotency conflict")

func (r *Receiver) handleRatingCreate(w http.ResponseWriter, req *http.Request) {
	tenant, ok := requestTenant(req)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var in ratingInput
	dec := json.NewDecoder(io.LimitReader(req.Body, ratingMaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	conversationID := strings.TrimSpace(req.PathValue("id"))
	in, msg := normalizeRating(conversationID, in)
	if msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	g := &r.guard
	g.mu.Lock()
	rating, summary, err := g.createRating(tenant, conversationID, in)
	g.mu.Unlock()
	if errors.Is(err, errRatingConflict) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	r.mu.Lock()
	r.inv.AddReceipt(receiptSigilRatings, 1)
	r.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"rating": rating, "summary": summary})
}

func normalizeRating(conversationID string, in ratingInput) (ratingInput, string) {
	in.RatingID, in.Rating, in.Comment = strings.TrimSpace(in.RatingID), strings.TrimSpace(in.Rating), strings.TrimSpace(in.Comment)
	in.GenerationID, in.RaterID, in.Source = strings.TrimSpace(in.GenerationID), strings.TrimSpace(in.RaterID), strings.TrimSpace(in.Source)
	switch {
	case conversationID == "":
		return in, "conversation id is required"
	case len(conversationID) > ratingMaxConversationID:
		return in, "conversation id is too long"
	case in.RatingID == "":
		return in, "rating id is required"
	case len(in.RatingID) > ratingMaxRatingID:
		return in, "rating id is too long"
	case in.Rating != ratingGood && in.Rating != ratingBad:
		return in, "rating must be CONVERSATION_RATING_VALUE_GOOD or CONVERSATION_RATING_VALUE_BAD"
	case len(in.Comment) > ratingMaxComment:
		return in, "comment is too long"
	case len(in.GenerationID) > ratingMaxGenerationID:
		return in, "generation id is too long"
	case len(in.RaterID) > ratingMaxActorID:
		return in, "rater id is too long"
	case len(in.Source) > ratingMaxSource:
		return in, "source is too long"
	}
	if in.Metadata != nil {
		encoded, err := json.Marshal(in.Metadata)
		if err != nil || len(encoded) > ratingMaxMetadata {
			return in, "metadata is too large"
		}
	}
	return in, ""
}

// createRating stores a rating idempotently on (tenant, rating_id): a replay of the same
// payload returns the stored rating, a different payload is a conflict. Callers hold g.mu.
func (g *guardState) createRating(tenant, conversationID string, in ratingInput) (*storedRating, *ratingSummary, error) {
	if g.ratings == nil {
		g.ratings = map[string]*storedRating{}
		g.byConv = map[string][]*storedRating{}
		g.summary = map[string]*ratingSummary{}
	}
	convKey := tenant + "::" + conversationID
	if existing, ok := g.ratings[tenant+"::"+in.RatingID]; ok {
		if !sameRating(existing, conversationID, in) {
			return nil, nil, errRatingConflict
		}
		summary := *g.summary[convKey]
		return existing, &summary, nil
	}
	rating := &storedRating{
		RatingID: in.RatingID, ConversationID: conversationID, GenerationID: in.GenerationID,
		Rating: in.Rating, Comment: in.Comment, Metadata: in.Metadata, RaterID: in.RaterID,
		Source: in.Source, CreatedAt: g.clock(),
	}
	g.ratings[tenant+"::"+in.RatingID] = rating
	g.byConv[convKey] = append([]*storedRating{rating}, g.byConv[convKey]...)
	summary := g.summary[convKey]
	if summary == nil {
		summary = &ratingSummary{}
		g.summary[convKey] = summary
	}
	summary.TotalCount++
	if in.Rating == ratingGood {
		summary.GoodCount++
	} else {
		summary.BadCount++
		summary.HasBadRating = true
		summary.LatestBadAt = rating.CreatedAt
	}
	summary.LatestRating, summary.LatestRatedAt = in.Rating, rating.CreatedAt
	out := *summary
	return rating, &out, nil
}

func sameRating(existing *storedRating, conversationID string, in ratingInput) bool {
	if existing.ConversationID != conversationID || existing.GenerationID != in.GenerationID || existing.Rating != in.Rating ||
		existing.Comment != in.Comment || existing.RaterID != in.RaterID || existing.Source != in.Source {
		return false
	}
	a, _ := json.Marshal(existing.Metadata)
	b, _ := json.Marshal(in.Metadata)
	return bytes.Equal(a, b)
}

func (r *Receiver) handleRatingList(w http.ResponseWriter, req *http.Request) {
	tenant, ok := requestTenant(req)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	conversationID := strings.TrimSpace(req.PathValue("id"))
	if conversationID == "" || len(conversationID) > ratingMaxConversationID {
		http.Error(w, "invalid conversation id", http.StatusBadRequest)
		return
	}
	limit := ratingDefaultPageLimit
	if raw := strings.TrimSpace(req.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = min(parsed, ratingMaxPageLimit)
	}
	var cursor uint64
	if raw := strings.TrimSpace(req.URL.Query().Get("cursor")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		cursor = parsed
	}

	r.guard.mu.Lock()
	rows := r.guard.byConv[tenant+"::"+conversationID]
	items := []*storedRating{}
	next := ""
	if cursor < uint64(len(rows)) {
		end := min(int(cursor)+limit, len(rows))
		items = append(items, rows[cursor:end]...)
		if end < len(rows) {
			next = strconv.Itoa(end)
		}
	}
	body, err := json.Marshal(map[string]any{"items": items, "next_cursor": next})
	r.guard.mu.Unlock()
	if err != nil {
		http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
