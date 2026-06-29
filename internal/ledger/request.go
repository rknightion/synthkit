// SPDX-License-Identifier: AGPL-3.0-only

// Package ledger is the request-correlation spine: a per-blueprint master clock mints
// correlated synthetic Requests; workloads PROJECT them into metrics/traces/logs/RUM.
// Nothing outside this package mints request-scoped IDs (HARD RULE — ARCHITECTURE I9):
// that is the only way one correlation_id threads across every signal class despite
// independent tickers.
//
// request.go is part of the Phase-0 frozen seam (single owner: the wiring pass).
// The Ledger implementation (ring buffer, Mint, Active) lives in ledger.go (Phase-1).
package ledger

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
)

// Correlation bundles the request-scoped keys minted together for one logical request.
// These are span/log FIELDS — never Mimir labels or Loki stream labels (ARCHITECTURE
// I14).
type Correlation struct {
	CorrelationID string // universal UUID stamped on every signal of the request
	TraceID       string // W3C 128-bit (32 hex chars)
	SpanID        string // W3C 64-bit (16 hex) — the backend SERVER span; children parent to it
	BrowserSpanID string // W3C 64-bit (16 hex) — the browser (RUM) root fetch span; set for
	// browser-origin requests only: the RUM lane emits the browser span with this id
	// (parent="") and the backend SERVER span parents to it.
	SessionID string
	RequestID string

	// AI request-correlation join keys (Spec 2b) — span/log FIELDS, never labels (I14). Minted for
	// every request (cheap; only the AI hop/log lanes read them). PortkeyTraceID is Portkey's
	// own parallel trace id (the gateway span + Portkey export log + LangSmith run all carry
	// it — the cross-system join the W3C trace_id can't provide because Portkey doesn't
	// propagate W3C). RunID is the LangSmith run UUID (one run per request).
	PortkeyTraceID string
	RunID          string
}

// NewCorrelation mints a fresh, internally-consistent key-set for one request.
// Request-scoped IDs are random per run (fixture identity, by contrast, is
// deterministic — see internal/fixture).
func NewCorrelation() Correlation {
	return Correlation{
		CorrelationID:  uuid(),
		TraceID:        hexN(16),
		SpanID:         hexN(8),
		BrowserSpanID:  hexN(8),
		SessionID:      uuid(),
		RequestID:      uuid(),
		PortkeyTraceID: uuid(),
		RunID:          uuid(),
	}
}

// Outcome is a request's terminal status (drives error metrics, log status codes, span
// status).
type Outcome int

const (
	OutcomeSuccess     Outcome = iota // 2xx
	OutcomeClientError                // 4xx
	OutcomeServerError                // 5xx
	OutcomeThrottled                  // 429
)

func (o Outcome) String() string {
	switch o {
	case OutcomeClientError:
		return "client_error"
	case OutcomeServerError:
		return "server_error"
	case OutcomeThrottled:
		return "throttled"
	default:
		return "success"
	}
}

// AICall carries the gen_ai identity of an AI hop (Call.AI; nil for db/cache/service hops).
// Same shape as fixture.AICall — the workload maps across (neither base package imports the
// other). The trace/log/metric lanes read it to stamp gen_ai.* span attrs, gen_ai_client_*
// series, and the correlated AI logs.
type AICall struct {
	Op       string // genai operation value (chat, invoke_agent, …)
	Provider string // gen_ai.provider.name (gateway/model hops)
	Model    string // gen_ai.request.model (gateway/model hops)
	Subject  string // SpanName subject (agent/tool/workflow/data-source name)
}

// Call is one downstream client hop the request makes (a CLIENT span + service-graph
// edge). Target identity comes from the blueprint's resolved fixtures — the workload
// never invents instance names. Span IDs are minted here (I9), one per hop, so the trace
// and log lanes reference the same id by construction.
type Call struct {
	Kind   string // "db" | "cache" | "service" | AI hop kinds (llm_gateway|llm_model|agent|tool|workflow|retrieval)
	Target string // resolved instance/service name — the cross-signal join key
	Engine string // "postgres" | "mysql" | "redis"; "" for service/AI hops

	// SpanID is this hop's CLIENT span (caller side). PeerSpanID is the callee SERVER
	// span for "service" AND "llm_gateway" hops (the callee/gateway appears as its own
	// Tempo node — the Path-B connected gateway span); "" otherwise.
	SpanID     string
	PeerSpanID string

	// AI is the gen_ai identity for AI hops (nil otherwise).
	AI *AICall
	// ParentHopIndex is the index (into Request.Calls) of the hop this one nests under;
	// -1 means it is a direct child of the backend SERVER span. A child's CLIENT span
	// parents to the parent hop's INNER span (PeerSpanID if set, else SpanID).
	ParentHopIndex int

	Duration time.Duration
	Failed   bool
}

// Request is the per-request source of truth for the whole signal fan-out. FROZEN
// seam — projection lanes read these fields; do not reshape without a wiring pass
// across every reader.
type Request struct {
	Correlation

	// Identity dimensions — fixed at mint so every signal slices identically.
	Workload string // workload instance name (e.g. "acme-api")
	Env      string // environment name (e.g. "prod")
	Cluster  string // cluster the workload runs on (e.g. "acme-prod-use1")
	Route    string // endpoint route (e.g. "GET /v1/items")

	// Model/Provider are the request-level gen_ai choice (one model+provider per request, flowing
	// through every gen_ai hop). Set by minters that drive an LLM flow (the app workload picks them
	// from its declared model/provider sets); "" for non-AI requests. These are span/log FIELDS —
	// never labels (the gen_ai model/provider METRIC labels use a fixed enum domain, I32). A per-hop
	// Call.AI value (web_service's AI hops) overrides these at that hop.
	Model    string // gen_ai.request.model (request-level)
	Provider string // gen_ai.provider.name (request-level)

	// Timing. Start is the mint instant and stays the ledger windowing key.
	Start    time.Time
	Duration time.Duration

	// RenderOffset jitters each trace's COMPLETION time back from the mint instant so a
	// Mint batch's ends spread across [Start−RenderJitterWindow, Start] instead of stacking.
	// It is DETERMINISTIC per request (derived from TraceID at mint) so every projection lane
	// renders the same waterfall. Span lanes time their tree from RenderStart() (BACKDATED to
	// completion — see RenderStart); Active() windowing keys on Start (do NOT use RenderStart
	// for windowing).
	RenderOffset time.Duration

	// Facts — the consistent story every projection lane must agree on.
	Outcome       Outcome
	StatusCode    int    // concrete HTTP status (200, 400, 429, 500, …)
	ErrorKind     string // "" on success; e.g. "timeout" | "upstream_5xx" (log detail)
	BrowserOrigin bool   // wrapped in a RUM session/user action (Faro lane + browser span)
	Calls         []Call // downstream db/cache hops, in order
}

// RenderStart is the base timestamp span/log lanes use to time a request's tree. Traces are
// BACKDATED to completion: synthkit fabricates each whole trace at render time, but times it so the
// trace ENDS at ~Start (≈ now) — matching real spans, which export on completion and so end in the
// PAST. The tree therefore starts a full Duration before that, jittered back by RenderOffset:
//
//	RenderStart = Start − Duration − RenderOffset   (trace end = RenderStart+Duration = Start − RenderOffset ≤ Start)
//
// Duration is read at CALL time, so RenderStart reflects the minter's FINAL duration (incl. any
// hop-cover growth). Backdating keeps long agentic spans' start times genuinely old (exercising the
// metrics-generator ingestion slack) without a deferred-emission queue. Active()/retention key on
// Start, never RenderStart (windowing must not move with the backdate).
func (r *Request) RenderStart() time.Time { return r.Start.Add(-r.Duration).Add(-r.RenderOffset) }

// Minter is a workload instance's contribution to the blueprint's master clock. The
// Ledger calls every registered Minter once per master tick; volume MUST be
// cadence-invariant (scale by tickSec/ReferenceTickSeconds — minting twice as often
// mints half as much each time).
type Minter interface {
	// Workload returns the instance name stamped onto minted requests (the dispatch
	// key for ProjectBatch).
	Workload() string
	// Mint fabricates this tick's correlated requests with Start=now. Implementations
	// use eng for shape/noise/incident draws and NewCorrelation for IDs.
	Mint(now time.Time, tickSec float64, eng *shape.Engine) []*Request
}

// ReferenceTickSeconds is the window Minter volume parameters are expressed against.
// A Minter producing "n requests per reference window" mints n × tickSec/30 per call.
const ReferenceTickSeconds = 30.0

// uuid returns a random RFC-4122-ish v4 string (cosmetic only — synthetic data).
func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// hexN returns 2*n hex chars from n random bytes (W3C trace/span ids).
func hexN(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// NewSpanID mints a fresh W3C 64-bit span id (16 hex chars). Per-hop span ids are minted
// here so nothing outside the ledger package mints request-scoped ids (I9).
func NewSpanID() string { return hexN(8) }

// NewTraceID mints a fresh W3C 128-bit trace id (32 hex chars). Used by multi-turn workloads
// (e.g. the aiagent sigil workload) that own one trace per conversation-turn — minted here so
// nothing outside the ledger package mints request-scoped ids (I9).
func NewTraceID() string { return hexN(16) }
