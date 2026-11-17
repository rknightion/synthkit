// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// Push-observer sink names for the two request/response endpoints. They are
// plain strings on pushhook.Event.Sink; selfobs maps an unrecognised sink to the
// "other" signal class, so no pushhook change is needed.
const (
	ObserveSinkHooks   = "sigil_hooks"
	ObserveSinkRatings = "sigil_ratings"
)

const (
	hooksEvaluatePath    = "/api/v1/hooks:evaluate"
	hookTimeoutHeader    = "X-Agento11y-Hook-Timeout-Ms"
	defaultHookTimeout   = 15 * time.Second
	maxHookTimeoutMS     = 119999
	maxHookRequestBytes  = 4 << 20
	maxHookResponseBytes = 4 << 20
	hookPhasePreflight   = "preflight"
	hookActionAllow      = "allow"
	hookActionDeny       = "deny"
)

// Hooks calls the Agent Observability request-path guard, POST /api/v1/hooks:evaluate.
// It is the concrete nativesigil.GenerationStages for a bound pipeline.
//
// Policy this type enforces, from loop51 D19:
//   - preflight only, one call per produced tool result, attributed to the planner
//     (the producing generation's agent, model and correlation IDs);
//   - AfterGeneration is a no-op: no postflight request is built or sent;
//   - an explicit deny, an empty or unknown action, and a transformed_input with no
//     single recoverable matching tool result all return a zero decision (not
//     allowed) and no error, so the caller's fail_open never reopens them;
//   - transport failures, non-200 statuses, undecodable bodies and context expiry
//     return an error. The caller owns the hook-local fail_open choice.
//
// The original tool-result content appears only in the authenticated request body.
// Errors and observer events never carry request or response content.
//
// Hooks emits no telemetry of its own. The backend emits every hook metric and any
// guard workflow step from the real call; synthkit must never forge them.
type Hooks struct {
	endpoint string
	auth     string
	tenantID string
	hc       *http.Client
	delivery httpretry.Delivery

	// Observe receives one sanitized event per admitted evaluation request. Items is 1
	// when a well-formed 200 response was decoded (allow or deny alike), else 0. Calls
	// whose context is already done are silent.
	Observe pushhook.Observer
}

var _ nativesigil.GenerationStages = (*Hooks)(nil)

// NewHooks returns a hook client for the base URL endpoint, authenticating like the
// ingest sink (HTTP Basic tenantID:token plus X-Scope-OrgID).
func NewHooks(endpoint, tenantID, token string) (*Hooks, error) {
	base, err := normalizeBaseURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("sigil hooks: %w", err)
	}
	return &Hooks{
		endpoint: base,
		auth:     basicAuth(tenantID, token),
		tenantID: tenantID,
		hc:       newNoRedirectClient(defaultHookTimeout),
		delivery: httpretry.DefaultDelivery(),
	}, nil
}

// SetDelivery installs the HA delivery policy before use.
func (h *Hooks) SetDelivery(d httpretry.Delivery) { h.delivery = d; d.ConfigureClient(h.hc) }

// AfterGeneration is deliberately empty: D19 admits no postflight call. It still
// honours cancellation so a stopped run is never reported as completed.
func (h *Hooks) AfterGeneration(ctx context.Context, _ nativesigil.Generation) error {
	return ctx.Err()
}

// BeforeToolResult sends one preflight evaluation for result and maps the verdict.
func (h *Hooks) BeforeToolResult(ctx context.Context, stage nativesigil.GenerationStage, result nativesigil.ToolResult) (decision nativesigil.ToolResultDecision, terminalErr error) {
	if err := ctx.Err(); err != nil {
		return nativesigil.ToolResultDecision{}, err
	}
	body, err := json.Marshal(toolResultHookRequest(stage, result))
	if err != nil {
		return nativesigil.ToolResultDecision{}, operationalerr.New(operationalerr.CodeInternal)
	}
	if len(body) > maxHookRequestBytes {
		return nativesigil.ToolResultDecision{}, operationalerr.New(operationalerr.CodeRejected)
	}

	timeoutMS, err := hookTimeoutMillis(ctx, h.hc.Timeout)
	if err != nil {
		return nativesigil.ToolResultDecision{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint+hooksEvaluatePath, bytes.NewReader(body))
	if err != nil {
		return nativesigil.ToolResultDecision{}, operationalerr.New(operationalerr.CodeInternal)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", h.auth)
	req.Header.Set(hookTimeoutHeader, strconv.FormatInt(timeoutMS, 10))
	if h.tenantID != "" {
		req.Header.Set("X-Scope-OrgID", h.tenantID)
	}

	// Admission: from here the evaluation counts as a request. Refusals above (marshal
	// failure, oversized body, expired budget, request construction) emit no event.
	start := time.Now()
	status, decoded := 0, false
	defer func() {
		h.observe(ctx, decoded, status, time.Since(start), terminalErr)
	}()

	var response []byte
	status, err = h.delivery.Send(ctx, h.hc, req, func(resp *http.Response) (int, error) {
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxHookResponseBytes+1))
		if err != nil {
			return resp.StatusCode, err
		}
		if len(data) > maxHookResponseBytes {
			return resp.StatusCode, errors.New("hook response too large")
		}
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, fmt.Errorf("hook evaluate returned HTTP %d", resp.StatusCode)
		}
		response = data
		return resp.StatusCode, nil
	})
	if err != nil {
		return nativesigil.ToolResultDecision{}, httpretry.Preserve(err, operationalerr.New(operationalerr.Classify(status, err)))
	}

	decision, err = decodeHookDecision(response, result)
	if err != nil {
		return nativesigil.ToolResultDecision{}, err
	}
	decoded = true
	return decision, nil
}

func (h *Hooks) observe(ctx context.Context, decoded bool, status int, duration time.Duration, err error) {
	if h.Observe == nil {
		return
	}
	items := 0
	if decoded {
		items = 1
	}
	h.Observe(ctx, pushhook.Event{
		Sink: ObserveSinkHooks, Items: items, Status: status, Duration: duration,
		ErrorCode: operationalerr.Classify(status, err),
	})
}

// hookTimeoutMillis resolves the evaluator budget sent to the server: the smaller of the
// caller's remaining deadline and the client's effective HTTP timeout, since the client gives
// up at the HTTP timeout whatever the caller allows. With neither, the SDK default applies.
// The server honours 1..119999 and falls back to its own default for anything else, so the
// value is clamped into range.
func hookTimeoutMillis(ctx context.Context, httpTimeout time.Duration) (int64, error) {
	timeout := httpTimeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	ms := timeout.Milliseconds()
	if ms <= 0 {
		return 0, context.DeadlineExceeded
	}
	if ms > maxHookTimeoutMS {
		ms = maxHookTimeoutMS
	}
	return ms, nil
}

// Wire types. They mirror the request shape the server's hand-written decoder reads and
// the SDK serializes: snake_case part kinds, embedded JSON for tool_call.input_json and
// tool_result.content_json, and base64 for tools[].input_schema_json. See
// testdata/hooks/PROVENANCE.txt and the SDK's conformance/hooks/README.md.

type hookEvaluateRequest struct {
	Phase   string      `json:"phase"`
	Context hookContext `json:"context"`
	Input   hookInput   `json:"input"`
}

type hookContext struct {
	AgentName      string            `json:"agent_name,omitempty"`
	AgentVersion   string            `json:"agent_version,omitempty"`
	Model          *hookModel        `json:"model,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	ConversationID string            `json:"conversation_id,omitempty"`
	TraceID        string            `json:"trace_id,omitempty"`
	SpanID         string            `json:"span_id,omitempty"`
}

type hookModel struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

type hookInput struct {
	Messages            []hookMessage `json:"messages,omitempty"`
	Tools               []hookTool    `json:"tools,omitempty"`
	SystemPrompt        string        `json:"system_prompt,omitempty"`
	ConversationPreview string        `json:"conversation_preview,omitempty"`
}

// hookTool carries a request tool definition. InputSchemaJSON is a []byte so encoding/json
// writes base64, which is what the server decodes (raw JSON there fails the whole request).
type hookTool struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Type            string `json:"type,omitempty"`
	InputSchemaJSON []byte `json:"input_schema_json,omitempty"`
	Deferred        bool   `json:"deferred,omitempty"`
}

type hookMessage struct {
	Role  string     `json:"role"`
	Name  string     `json:"name,omitempty"`
	Parts []hookPart `json:"parts"`
}

type hookPart struct {
	Kind       string          `json:"kind"`
	Text       string          `json:"text,omitempty"`
	Thinking   string          `json:"thinking,omitempty"`
	ToolCall   *hookToolCall   `json:"tool_call,omitempty"`
	ToolResult *hookToolResult `json:"tool_result,omitempty"`
}

type hookToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	InputJSON json.RawMessage `json:"input_json,omitempty"`
}

type hookToolResult struct {
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	IsError     bool            `json:"is_error,omitempty"`
	Content     string          `json:"content,omitempty"`
	ContentJSON json.RawMessage `json:"content_json,omitempty"`
}

// toolResultHookRequest builds the preflight request for one produced tool result. Every
// identity field comes from the stage (the planner's ledger-minted IDs); nothing is minted
// here. The stage carries no system prompt, tool definitions or tags, so none are sent.
func toolResultHookRequest(stage nativesigil.GenerationStage, result nativesigil.ToolResult) hookEvaluateRequest {
	ctx := hookContext{
		AgentName:      stage.AgentName,
		AgentVersion:   stage.AgentVersion,
		ConversationID: stage.ConversationID,
		TraceID:        stage.TraceID,
		SpanID:         stage.SpanID,
	}
	if stage.Provider != "" || stage.Model != "" {
		ctx.Model = &hookModel{Provider: stage.Provider, Name: stage.Model}
	}
	return hookEvaluateRequest{
		Phase:   hookPhasePreflight,
		Context: ctx,
		Input: hookInput{Messages: []hookMessage{{
			Role: "tool",
			Name: result.Name,
			Parts: []hookPart{{
				Kind: "tool_result",
				ToolResult: &hookToolResult{
					ToolCallID:  result.ToolCallID,
					Name:        result.Name,
					IsError:     result.IsError,
					Content:     result.Content,
					ContentJSON: hookRequestPayloadJSON(result.ContentJSON),
				},
			}},
		}}},
	}
}

// hookRequestPayloadJSON keeps a request-side payload valid JSON. An unparsable payload
// travels as a JSON string so its text stays visible to rules, as the SDK does.
func hookRequestPayloadJSON(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	if json.Valid(raw) {
		return json.RawMessage(raw)
	}
	return hookJSONString(string(raw))
}

// Response wire types. Role is not read: only tool_result parts matter, and the server
// may send the role as a string or as an enum integer.

type hookEvaluateResponse struct {
	Action           string             `json:"action"`
	TransformedInput *hookResponseInput `json:"transformed_input,omitempty"`
}

type hookResponseInput struct {
	Messages            []hookResponseMessage `json:"messages,omitempty"`
	Tools               []json.RawMessage     `json:"tools,omitempty"`
	SystemPrompt        string                `json:"system_prompt,omitempty"`
	ConversationPreview string                `json:"conversation_preview,omitempty"`
}

type hookResponseMessage struct {
	Parts []hookResponsePart `json:"parts,omitempty"`
}

type hookResponsePart struct {
	Kind       string                  `json:"kind"`
	ToolResult *hookResponseToolResult `json:"tool_result,omitempty"`
}

type hookResponseToolResult struct {
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	IsError     bool            `json:"is_error,omitempty"`
	Content     string          `json:"content,omitempty"`
	ContentJSON json.RawMessage `json:"content_json,omitempty"`
}

// decodeHookDecision maps a 200 response body to the stage decision for original.
func decodeHookDecision(body []byte, original nativesigil.ToolResult) (nativesigil.ToolResultDecision, error) {
	var wire hookEvaluateResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return nativesigil.ToolResultDecision{}, operationalerr.New(operationalerr.CodeRejected)
	}
	switch wire.Action {
	case hookActionAllow:
	case hookActionDeny:
		return nativesigil.ToolResultDecision{}, nil
	default:
		// Includes an empty action. The SDK treats that as allow; D19 requires a zero
		// decision to fail closed.
		return nativesigil.ToolResultDecision{}, nil
	}
	if !wire.TransformedInput.present() {
		return nativesigil.ToolResultDecision{Allowed: true}, nil
	}
	var matches []*nativesigil.ToolResult
	for _, message := range wire.TransformedInput.Messages {
		for _, part := range message.Parts {
			tr := part.ToolResult
			if tr == nil || (part.Kind != "tool_result" && part.Kind != "") {
				continue
			}
			if tr.ToolCallID != original.ToolCallID || tr.Name != original.Name {
				continue
			}
			matches = append(matches, &nativesigil.ToolResult{
				ToolCallID:  tr.ToolCallID,
				Name:        tr.Name,
				Content:     tr.Content,
				ContentJSON: decodeHookResponseJSON(tr.ContentJSON),
				IsError:     tr.IsError,
			})
		}
	}
	if len(matches) != 1 {
		// A transform was reported but cannot be applied to exactly this result. Letting
		// the original through would bypass a redaction, so fail closed.
		return nativesigil.ToolResultDecision{}, nil
	}
	return nativesigil.ToolResultDecision{Allowed: true, Transformed: matches[0]}, nil
}

// present reports whether the server described a transform at all. An entirely empty
// transformed_input is not one, matching the SDK.
func (in *hookResponseInput) present() bool {
	return in != nil && (len(in.Messages) > 0 || len(in.Tools) > 0 || in.SystemPrompt != "" || in.ConversationPreview != "")
}

// decodeHookResponseJSON recovers a response-side payload. The server marshals its bytes
// fields with encoding/json, so they arrive base64-encoded inside a JSON string. The
// result is always one valid JSON document: base64 that is not JSON, and text that is
// neither base64 nor JSON, are kept as a JSON string. Same rule in every SDK; see
// conformance/hooks/README.md.
func decodeHookResponseJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		if json.Valid(raw) {
			return raw
		}
		return nil
	}
	if text == "" {
		return nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
		if json.Valid(decoded) {
			return decoded
		}
		return hookJSONString(string(decoded))
	}
	if json.Valid([]byte(text)) {
		return []byte(text)
	}
	return hookJSONString(text)
}

func hookJSONString(text string) json.RawMessage {
	quoted, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return quoted
}

func basicAuth(tenantID, token string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(tenantID+":"+token))
}

// normalizeBaseURL checks endpoint is an absolute http(s) base URL and drops a trailing slash.
func normalizeBaseURL(endpoint string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if trimmed == "" {
		return "", errors.New("endpoint must not be empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("endpoint must be an absolute http(s) URL")
	}
	return trimmed, nil
}

// newNoRedirectClient never follows a redirect: the request carries credentials and a
// verdict must come from the endpoint that was configured.
func newNoRedirectClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
