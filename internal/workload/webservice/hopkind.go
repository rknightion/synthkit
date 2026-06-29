// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/semconv"
)

// hopStamper shapes the spans for one downstream-hop kind. It is the single extension
// point for new hop kinds: future hop kinds register here, delegating their attrs to a
// shared mechanic lib (db.* semconv inline; gen_ai.* via internal/genai). Keep stampers stateless.
type hopStamper interface {
	// spanName is the CLIENT span name for this hop.
	spanName(c ledger.Call) string
	// clientAttrs are the CLIENT span attributes (semconv per kind) + correlation, which
	// the caller stamps. Implementations must NOT add high-cardinality keys.
	clientAttrs(w *Workload, r *ledger.Request, c ledger.Call) map[string]any
	// peerResource returns the callee SERVER-span resource attributes and emit=true when a
	// separate callee resource/SERVER span should be emitted (service hops + the llm_gateway
	// connected-gateway span, Path-B). db/cache return emit=false (the hop is a leaf CLIENT
	// span in the caller's resource).
	peerResource(w *Workload, c ledger.Call) (attrs map[string]any, emit bool)
	// peerSpan returns the SERVER span name + attrs for the peer node (only consulted when
	// peerResource emit=true). service hops use a generic "handle" span; the llm_gateway hop
	// uses the gen_ai server-side view carrying portkey_trace_id.
	peerSpan(w *Workload, r *ledger.Request, c ledger.Call) (name string, attrs map[string]any)
}

var hopStampers = map[string]hopStamper{
	"db":                   dbStamper{},
	"cache":                cacheStamper{},
	"service":              serviceStamper{},
	fixture.KindLLMGateway: aiStamper{kind: fixture.KindLLMGateway},
	fixture.KindLLMModel:   aiStamper{kind: fixture.KindLLMModel},
	fixture.KindAgent:      aiStamper{kind: fixture.KindAgent},
	fixture.KindTool:       aiStamper{kind: fixture.KindTool},
	fixture.KindWorkflow:   aiStamper{kind: fixture.KindWorkflow},
	fixture.KindRetrieval:  aiStamper{kind: fixture.KindRetrieval},
}

// genaiOp / genaiSubject extract the gen_ai operation + SpanName subject from a hop's AI
// carrier (nil-safe). The subject falls back to the model for a bare model hop. Shared by
// the trace stampers and the span-metric naming (metrics.go) so both agree.
func genaiOp(ai *ledger.AICall) string {
	if ai == nil {
		return ""
	}
	return ai.Op
}

func genaiSubject(ai *ledger.AICall) string {
	if ai == nil {
		return ""
	}
	if ai.Subject != "" {
		return ai.Subject
	}
	return ai.Model
}

// asCallSpec adapts a ledger.Call to the callSpec the db.* span helpers in traces.go/metrics.go
// take (clientSpanName/dbOperation are NOT changed — metrics.go still calls them with a callSpec
// from w.m.calls, so changing their signature would break that call site). The helpers read only
// Kind/Target/Engine.
func asCallSpec(c ledger.Call) callSpec {
	return callSpec{Kind: c.Kind, Target: c.Target, Engine: c.Engine}
}

// dbStamper / cacheStamper reuse the db.* semconv helpers (which stay in traces.go/metrics.go).
type dbStamper struct{}

func (dbStamper) spanName(c ledger.Call) string { return clientSpanName(asCallSpec(c)) }
func (dbStamper) clientAttrs(w *Workload, r *ledger.Request, c ledger.Call) map[string]any {
	return w.dbSpanAttrs(r, c)
}
func (dbStamper) peerResource(*Workload, ledger.Call) (map[string]any, bool) { return nil, false }
func (dbStamper) peerSpan(*Workload, *ledger.Request, ledger.Call) (string, map[string]any) {
	return "", nil // never emitted (peerResource emit=false)
}

type cacheStamper struct{}

func (cacheStamper) spanName(c ledger.Call) string { return clientSpanName(asCallSpec(c)) }
func (cacheStamper) clientAttrs(w *Workload, r *ledger.Request, c ledger.Call) map[string]any {
	return w.dbSpanAttrs(r, c)
}
func (cacheStamper) peerResource(*Workload, ledger.Call) (map[string]any, bool) { return nil, false }
func (cacheStamper) peerSpan(*Workload, *ledger.Request, ledger.Call) (string, map[string]any) {
	return "", nil
}

// serviceStamper models an HTTP service-to-service hop: an http.* CLIENT span on the caller
// plus a SERVER span in the callee's own resource block (so the callee is a real Tempo node;
// Tempo's own service-graph processor then derives the backend→callee edge from those spans).
// The callee's namespace/env/cluster default to the caller's (same-cluster microservice
// assumption; cross-cluster is out of v1 scope).
type serviceStamper struct{}

func (serviceStamper) spanName(c ledger.Call) string { return "POST /" + c.Target }
func (serviceStamper) clientAttrs(w *Workload, r *ledger.Request, c ledger.Call) map[string]any {
	a := map[string]any{
		"http.method":    "POST",
		"http.route":     "/" + c.Target,
		"server.address": c.Target,
		"peer.service":   c.Target,
		"net.peer.name":  c.Target,
	}
	w.stampCorrelation(a, r)
	return a
}
func (serviceStamper) peerResource(w *Workload, c ledger.Call) (map[string]any, bool) {
	return calleeResourceAttrs(w, c.Target), true
}
func (serviceStamper) peerSpan(w *Workload, r *ledger.Request, c ledger.Call) (string, map[string]any) {
	return "handle " + c.Target, w.peerServerAttrs(r)
}

// calleeResourceAttrs is the resource-block identity for a peer service node (a service
// hop's callee or an llm_gateway's gateway). Same-cluster microservice assumption.
// Legacy deployment.environment DROPPED — deployment.environment.name only (clean cutover).
// §5 canon attrs (context/use_case/team) inherited from the caller workload when non-empty.
func calleeResourceAttrs(w *Workload, name string) map[string]any {
	attrs := map[string]any{
		semconv.AttrServiceName:               name,
		semconv.AttrServiceNamespace:          w.namespace,
		semconv.AttrServiceVersion:            w.version,
		semconv.AttrDeploymentEnvironmentName: w.env,
		"k8s.cluster.name":                    w.cluster,
		"k8s.namespace.name":                  w.namespace,
		"k8s.deployment.name":                 name,
		"telemetry.sdk.language":              backendSDKLanguage,
		"telemetry.sdk.name":                  "opentelemetry",
	}
	// §5 canon attrs (§5): omit when empty (I13).
	if w.context != "" {
		attrs[semconv.AttrContext] = w.context
	}
	if w.useCase != "" {
		attrs[semconv.AttrUseCase] = w.useCase
	}
	if w.team != "" {
		attrs[semconv.AttrTeam] = w.team
	}
	return attrs
}

// aiStamper shapes a gen_ai AI hop's CLIENT span, delegating vocabulary to internal/genai.
// The llm_gateway kind additionally emits a connected SERVER span (Path-B): the gateway
// appears as its own Tempo node reusing the hop's PeerSpanID, carrying the gen_ai
// server-side attrs + portkey_trace_id (the golden-thread join to the Portkey export log).
type aiStamper struct{ kind string }

func (aiStamper) spanName(c ledger.Call) string {
	return genai.SpanName(genaiOp(c.AI), genaiSubject(c.AI))
}
func (aiStamper) clientAttrs(w *Workload, r *ledger.Request, c ledger.Call) map[string]any {
	return w.aiSpanAttrs(r, c)
}
func (s aiStamper) peerResource(w *Workload, c ledger.Call) (map[string]any, bool) {
	if s.kind != fixture.KindLLMGateway {
		return nil, false // model/agent/tool/workflow/retrieval hops are leaf CLIENT spans
	}
	return calleeResourceAttrs(w, c.Target), true
}
func (s aiStamper) peerSpan(w *Workload, r *ledger.Request, c ledger.Call) (string, map[string]any) {
	// gateway SERVER span: the gen_ai server-side view of the proxied call, connected by
	// trace id, carrying portkey_trace_id so the Portkey export log joins the gateway node.
	a := map[string]any{genai.AttrOperationName: genaiOp(c.AI)}
	model := ""
	if c.AI != nil {
		model = c.AI.Model
		if c.AI.Provider != "" {
			a[genai.AttrProviderName] = c.AI.Provider
		}
		if model != "" {
			a[genai.AttrRequestModel] = model
			a[genai.AttrResponseModel] = model
		}
	}
	a["portkey_trace_id"] = r.PortkeyTraceID
	w.stampCorrelation(a, r)
	return genai.SpanName(genaiOp(c.AI), model), a
}
