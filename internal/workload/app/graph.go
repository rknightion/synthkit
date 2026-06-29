// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"strings"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/telemetryspec"
	"github.com/rknightion/synthkit/internal/telemetryspec/profiles"
)

// nodeKind describes a service-type's default trace semantics. The registry is additive; an
// unknown/unimplemented type falls back to a generic instrumented service (resolveNodeKind),
// mirroring the hopStampers db-fallback rather than silently mis-shaping.
type nodeKind struct {
	rootKind   otlp.SpanKind // span kind when this node is the graph entry/root
	serverSpan bool          // emits its OWN SERVER span when called (an instrumented service)
	leaf       bool          // cannot have downstream calls (db/cache — the caller's CLIENT span is the leaf)
}

// nodeKinds is the service-type registry (§1.1). New types are additive.
var nodeKinds = map[string]nodeKind{
	"frontend":  {rootKind: otlp.KindClient, serverSpan: false, leaf: false}, // browser/RUM entry — a CLIENT root
	"web":       {rootKind: otlp.KindServer, serverSpan: true},
	"grpc":      {rootKind: otlp.KindServer, serverSpan: true},
	"worker":    {rootKind: otlp.KindConsumer, serverSpan: true},
	"job":       {rootKind: otlp.KindInternal, serverSpan: true},
	"stream":    {rootKind: otlp.KindConsumer, serverSpan: true},
	"gateway":   {rootKind: otlp.KindServer, serverSpan: true},
	"llm":       {rootKind: otlp.KindServer, serverSpan: true},
	"agent":     {rootKind: otlp.KindServer, serverSpan: true},
	"tool":      {rootKind: otlp.KindServer, serverSpan: true},
	"workflow":  {rootKind: otlp.KindServer, serverSpan: true},
	"retrieval": {rootKind: otlp.KindServer, serverSpan: true},
	"db":        {rootKind: otlp.KindServer, serverSpan: false, leaf: true},
	"cache":     {rootKind: otlp.KindServer, serverSpan: false, leaf: true},
}

// resolveNodeKind maps a declared type to its semantics, defaulting to a generic instrumented
// service for unknown types (§1.1 default fallback).
func resolveNodeKind(t string) nodeKind {
	if nk, ok := nodeKinds[t]; ok {
		return nk
	}
	return nodeKind{rootKind: otlp.KindServer, serverSpan: true}
}

// reservedMetricLabels / reservedStreamLabels / reservedSpanAttrs are the keys the workload
// auto-stamps as node identity (interp.go metricBaseLabels / streamBaseLabels + project.go
// spanAttrs). An author DSL declaration must NOT reuse them — checkReservedKeys rejects at load
// (review H1). Keep in sync with the stamping functions in interp.go/project.go.
var reservedMetricLabels = map[string]bool{
	"service": true, "service_name": true, "namespace": true, "k8s_namespace_name": true,
	semconv.LabelDeploymentEnvironmentName: true, "cluster": true, "k8s_cluster_name": true, "job": true,
}

var reservedStreamLabels = map[string]bool{
	"service_name": true, "env": true, "cluster": true, "namespace": true,
	"job": true, "source": true, "level": true,
}

var reservedSpanAttrs = map[string]bool{
	semconv.AttrCorrelationID: true, "request_id": true, "session_id": true,
}

// checkReservedKeys rejects any author label/stream-label/attribute that collides with an
// auto-stamped node-identity key (review H1 — prevents silent identity clobbering / dup series).
func checkReservedKeys(node string, n *node) error {
	for _, m := range n.metrics {
		for k := range m.Labels {
			if reservedMetricLabels[k] {
				return fmt.Errorf("app service %q metric %q: label %q is a reserved node-identity key (auto-stamped) — choose a different key", node, m.Name, k)
			}
		}
	}
	for _, l := range n.logs {
		for k := range l.StreamLabels {
			if reservedStreamLabels[k] {
				return fmt.Errorf("app service %q log %q: stream_label %q is a reserved node-identity key (auto-stamped) — choose a different key", node, l.Source, k)
			}
		}
	}
	for _, sp := range n.spans {
		for k := range sp.Attributes {
			if reservedSpanAttrs[k] {
				return fmt.Errorf("app service %q span %q: attribute %q is a reserved correlation key (auto-stamped) — choose a different key", node, sp.NameTemplate, k)
			}
		}
	}
	return nil
}

// node is a validated graph node: its declaration + resolved kind + effective (profile+inline) specs.
type node struct {
	decl    ServiceNode
	kind    nodeKind
	metrics []telemetryspec.MetricSpec
	logs    []telemetryspec.LogSpec
	spans   []telemetryspec.SpanSpec
	// agenticFlow, when non-nil, makes projectTraces emit a nested in-process gen_ai span subtree
	// (invoke_workflow→invoke_agent→execute_tool*→chat) under this node's structural span.
	agenticFlow *AgenticFlow
	// dbIdentity, when non-nil, is the resolved per-env RDS/cache identity for a db/cache leaf that
	// declared db_instance — its stable-semconv db-CLIENT span attrs (db.system.name / db.namespace
	// / server.address). nil ⇒ the leaf carries no DB identity. Resolved in build() from the binding
	// (the resolver owns the per-env identity; the workload only reads it — no minting).
	dbIdentity map[string]any
}

// graph is the validated service graph: nodes in declaration order + a name index + the entry node.
type graph struct {
	nodes  []*node
	byName map[string]*node
	entry  *node
}

// entryHasRumFaro reports whether the entry node carries the rum_faro catalog profile. When
// true (and the binding carries a Faro sink) the RUM beacon lane is active (app.rumEnabled).
// The check looks at the declared Profiles slice rather than the compiled spec slices so it
// fires even when the profile compiles to zero specs (which it intentionally does for
// Metrics/Logs after the 2026-06-16 gauge/log removal).
func (g *graph) entryHasRumFaro() bool {
	if g.entry == nil {
		return false
	}
	for _, p := range g.entry.decl.Profiles {
		if p == "rum_faro" {
			return true
		}
	}
	return false
}

// buildGraph resolves + validates the declared services into a graph: unique names, one entry,
// edges reference declared nodes, leaves make no calls, every effective spec passes the DSL
// capability matrix. Loud load errors (like the rest of the loader).
func buildGraph(services []ServiceNode) (*graph, error) {
	if len(services) == 0 {
		return nil, fmt.Errorf("app: no services declared")
	}
	g := &graph{byName: make(map[string]*node, len(services))}
	for i := range services {
		s := services[i]
		if strings.TrimSpace(s.Name) == "" {
			return nil, fmt.Errorf("app: service[%d] has no name", i)
		}
		if _, dup := g.byName[s.Name]; dup {
			return nil, fmt.Errorf("app: duplicate service node %q", s.Name)
		}
		n := &node{decl: s, kind: resolveNodeKind(s.Type), agenticFlow: s.AgenticFlow}
		// Effective specs = each composed profile's specs, then the node's inline specs.
		for _, pn := range s.Profiles {
			p, ok := profiles.Lookup(pn)
			if !ok {
				known := profiles.Names()
				return nil, fmt.Errorf("app: service %q references unknown profile %q (known: %s)", s.Name, pn, strings.Join(known, ", "))
			}
			n.metrics = append(n.metrics, p.Metrics...)
			n.logs = append(n.logs, p.Logs...)
			n.spans = append(n.spans, p.Spans...)
		}
		n.metrics = append(n.metrics, s.Metrics...)
		n.logs = append(n.logs, s.Logs...)
		n.spans = append(n.spans, s.Spans...)
		// When this node runs an agentic flow, the agent-flow emitter owns the chat <model> span (as
		// the LLM leaf under invoke_agent). Drop the gen_ai_client profile's flat chat SpanSpec so the
		// chat span isn't emitted twice — its METRICS (gen_ai_client_token_usage/operation_duration,
		// appended above from p.Metrics) are kept. Match by the profile's exact chat shape only.
		if n.agenticFlow != nil {
			n.spans = dropChatSpanSpec(n.spans)
		}
		for _, m := range n.metrics {
			if err := m.Validate(); err != nil {
				return nil, fmt.Errorf("app service %q: %w", s.Name, err)
			}
		}
		for _, l := range n.logs {
			if err := l.Validate(); err != nil {
				return nil, fmt.Errorf("app service %q: %w", s.Name, err)
			}
		}
		for _, sp := range n.spans {
			if err := sp.Validate(); err != nil {
				return nil, fmt.Errorf("app service %q: %w", s.Name, err)
			}
		}
		// Reserved-key guard (review H1): the workload auto-stamps the node identity on every
		// series/stream/span. An author label/stream-label/attr that reuses an identity key would
		// silently clobber the identity (collapsing two nodes' series into duplicates, or breaking
		// substrate-identity scoping). Reject at load, loudly, like the rest of the loader.
		if err := checkReservedKeys(s.Name, n); err != nil {
			return nil, err
		}
		g.nodes = append(g.nodes, n)
		g.byName[s.Name] = n
	}

	// Entry: the single node with entry: true, or the sole node when only one is declared.
	for _, n := range g.nodes {
		if n.decl.Entry {
			if g.entry != nil {
				return nil, fmt.Errorf("app: multiple entry nodes (%q and %q)", g.entry.decl.Name, n.decl.Name)
			}
			g.entry = n
		}
	}
	if g.entry == nil {
		if len(g.nodes) == 1 {
			g.entry = g.nodes[0]
		} else {
			return nil, fmt.Errorf("app: no entry node — set `entry: true` on the request entry service")
		}
	}

	// Edges: reference declared nodes, no self-call, leaves (db/cache) make no calls.
	for _, n := range g.nodes {
		if n.kind.leaf && len(n.decl.Calls) > 0 {
			return nil, fmt.Errorf("app: leaf node %q (type %q) cannot make downstream calls", n.decl.Name, n.decl.Type)
		}
		// db_instance names the backing per-env database/cache — only meaningful on a db/cache leaf
		// (the caller's CLIENT span is where the DB identity lands). Reject it on instrumented nodes.
		if n.decl.DBInstance != "" && !n.kind.leaf {
			return nil, fmt.Errorf("app: service %q (type %q) sets db_instance but is not a db/cache leaf — db_instance is only valid on a db/cache node", n.decl.Name, n.decl.Type)
		}
		for _, c := range n.decl.Calls {
			if c == n.decl.Name {
				return nil, fmt.Errorf("app: service %q calls itself", n.decl.Name)
			}
			if _, ok := g.byName[c]; !ok {
				return nil, fmt.Errorf("app: service %q calls unknown node %q", n.decl.Name, c)
			}
		}
	}
	return g, nil
}

// resolveDBIdentities wires each db/cache leaf that declared db_instance to its env's RDS/cache
// fixture, deriving the stable-semconv db-CLIENT span attrs (db.system.name / db.namespace /
// server.address). Resolution mirrors the blueprint's same-env SERVICE resolution: prefer the
// per-env instance "<db_instance>-<lower(env)>" (env names are UPPERCASE, db names use a lowercase
// suffix → case-insensitive), falling back to an exact name match. A db_instance that resolves to
// no declared database is a loud build error (the blueprint listed a backing db that does not exist
// in this env). Deterministic: no rand; identity comes from the resolver-built fixtures.
func (g *graph) resolveDBIdentities(env string, dbs []*fixture.DB) error {
	byName := make(map[string]*fixture.DB, len(dbs))
	for _, db := range dbs {
		byName[db.Name] = db
	}
	for _, n := range g.nodes {
		base := n.decl.DBInstance
		if base == "" {
			continue
		}
		db := byName[base] // exact match (primary)
		if env != "" {
			if perEnv, ok := byName[base+"-"+strings.ToLower(env)]; ok {
				db = perEnv // same-env instance (preferred), mirroring the service pattern
			}
		}
		if db == nil {
			declared := make([]string, 0, len(dbs))
			for _, d := range dbs {
				declared = append(declared, d.Name)
			}
			return fmt.Errorf("app: service %q db_instance %q resolves to no database in env %q (declared: %s)",
				n.decl.Name, base, env, strings.Join(declared, ", "))
		}
		n.dbIdentity = dbSpanIdentity(db)
	}
	return nil
}

// dbSpanIdentity derives the stable-semconv db-CLIENT span attributes for a resolved database
// fixture: db.system.name (engine → semconv system), db.namespace (the logical database), and
// server.address (the RDS endpoint FQDN — its first DNS label is the db_instance_identifier).
// Faithful to real OTel/Beyla DB instrumentation. The deprecated db.name is intentionally NOT
// emitted (db.namespace supersedes it in current semconv).
func dbSpanIdentity(db *fixture.DB) map[string]any {
	a := map[string]any{"db.system.name": dbSystemName(db.Engine)}
	if len(db.Databases) > 0 {
		a["db.namespace"] = db.Databases[0]
	}
	if host := instanceKeyHost(db.InstanceKey); host != "" {
		a["server.address"] = host
	}
	return a
}

// dbSystemName maps a DB engine to the stable OTel db.system.name value.
func dbSystemName(engine string) string {
	switch engine {
	case "mysql":
		return "mysql"
	case "redis":
		return "redis"
	case "postgres":
		return "postgresql"
	}
	return "postgresql"
}

// instanceKeyHost extracts the endpoint FQDN host from a dbo11y InstanceKey
// (pg "postgresql://host:5432/db"; mysql "tcp(host:3306)/db"). Returns "" if unparseable. The host
// is reused from the resolver's buildDBFixture — never re-derived (its first DNS label is the RDS
// db_instance_identifier).
func instanceKeyHost(key string) string {
	if key == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(key, "postgresql://"); ok { // postgresql://host:5432/db
		if i := strings.IndexAny(rest, ":/"); i >= 0 {
			return rest[:i]
		}
		return rest
	}
	if i := strings.Index(key, "tcp("); i >= 0 { // tcp(host:3306)/db
		rest := key[i+len("tcp("):]
		if j := strings.IndexAny(rest, ":)"); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}
