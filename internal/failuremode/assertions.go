// SPDX-License-Identifier: AGPL-3.0-only

package failuremode

import (
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Direction describes the expected movement of an emitted observation while a
// failure mode is active. It deliberately describes values, rather than control
// state: scenario activation is not evidence that a signal changed.
type Direction string

const (
	DirectionIncrease Direction = "increase"
	DirectionDecrease Direction = "decrease"
)

// Assertion is one sourced, queryable observation for a failure mode. Query is
// an instant PromQL or LogQL metric query. The {{target}} placeholder is replaced
// with the declared effect target and {{blueprint}} with the scenario's blueprint
// (a blueprint-scoped family carries that label; a substrate-scoped one does not);
// callers must escape both for their query API.
//
// AnySeries marks an observation whose carrying series cannot be named from the effect target. A
// resource-axis target is a declared resource, not a service, function or queue label, and the
// blueprint (not the harness) binds the resource to its consumers. The query is then evaluated as
// an instant vector with the {{target}} placeholder unused: the assertion passes when at least one
// series present both before and during the activation moves in Direction, and every series that
// moved returns near its baseline after deactivation.
//
// SiblingQuery is optional. When set, it proves that an environment-scoped
// effect moved only its target by evaluating the same observation for a supplied
// sibling environment. The runner keeps the assertion catalogue at this leaf
// seam so neither constructs nor the control plane acquire scenario policy.
type Assertion struct {
	Mode         string
	QueryAPI     string
	Query        string
	SiblingQuery string
	AnySeries    bool
	// MinActive is an absolute floor for Increase assertions: an observation counts as moved only
	// once it reaches this value, in addition to the proportional rule. It exists for observations
	// that sit at zero when healthy, where "any positive value" is a 0 -> small flip that unrelated
	// telemetry produces. Zero keeps the proportional rule alone. A floored baseline below the floor
	// clears once the observation is back under the floor.
	MinActive float64
	// Scopes narrow the observation to the consumers a blueprint binds to the targeted resource,
	// where the series carry the consumers' identity (see Scope). Empty means the observation
	// cannot be narrowed: the series that moves is not the consumer's own.
	Scopes []ConsumerScope
	// ScopeJoin joins the per-kind scoped queries: " or " (the default) unions series, " + " sums
	// single aggregates.
	ScopeJoin string
	Direction Direction
	Source    string
	Priority  int
}

// Consumer kinds a ConsumerScope names, as the control schema reports a resource's consumers.
// ConsumerAppCallers is not a schema kind: it is the caller of a consumer service, flattened by
// the client that reads the schema.
const (
	ConsumerApp        = "app"
	ConsumerAppCallers = "app.callers"
	ConsumerLambda     = "lambda"
	ConsumerSQS        = "sqs"
	ConsumerSolace     = "solace"
	ConsumerAgentcore  = "agentcore"
	ConsumerRDS        = "rds"
	ConsumerDocDB      = "docdb"
)

// Consumer is one bound consumer of a resource: its kind and its emitted identity as label name to
// value (the control schema's ResourceConsumer.Identity).
type Consumer struct {
	Kind     string
	Identity map[string]string
}

// ConsumerScope is the observation restricted to what one kind of consumer emits. Query carries
// one {{consumers.<label>}} placeholder per identity label it filters on; Scope replaces each with
// a regex alternation of the matching consumers' values. Kinds lists every consumer kind the
// query is valid for.
type ConsumerScope struct {
	Kinds []string
	Query string
}

// assertions are deliberately narrow, stable observations for the modes used
// by the shipped scenario blueprint. Every name and label comes from signals/;
// query expressions only combine those existing emitted fields.
var assertions = []Assertion{
	{Mode: "agentcore_throttle", QueryAPI: "prometheus", Query: "sum(aws_bedrock_agentcore_throttles_sum)", Direction: DirectionIncrease, Source: "signals/agentcore.md [slug: agentcore-invocation]", Priority: 100},
	{Mode: "slow_query_storm", QueryAPI: "prometheus", Query: "max(aws_docdb_read_latency_average{dimension_DBClusterIdentifier=\"{{target}}\"} or aws_rds_read_latency_average{dimension_DBInstanceIdentifier=\"{{target}}\"})", Direction: DirectionIncrease, Source: "signals/cw.md [slug: cw-docdb] and [slug: cw-rds]", Priority: 100},
	{Mode: "connection_saturation", QueryAPI: "prometheus", Query: "max(aws_docdb_database_connections_average{dimension_DBClusterIdentifier=\"{{target}}\"} or aws_rds_database_connections_average{dimension_DBInstanceIdentifier=\"{{target}}\"})", Direction: DirectionIncrease, Source: "signals/cw.md [slug: cw-docdb] and [slug: cw-rds]", Priority: 90},
	{Mode: "error_spike", QueryAPI: "prometheus", Query: "sum(rate(traces_spanmetrics_calls_total{service=\"{{target}}\",span_kind=\"SPAN_KIND_SERVER\",status_code=\"STATUS_CODE_ERROR\"}[2m])) or vector(0)", Direction: DirectionIncrease, Source: "signals/apm.md [slug: apm-calls]", Priority: 80},
	{Mode: "latency_storm", QueryAPI: "prometheus", Query: "avg(histogram_avg(rate(traces_spanmetrics_latency{service=\"{{target}}\",span_kind=\"SPAN_KIND_SERVER\"}[2m])) and histogram_count(rate(traces_spanmetrics_latency{service=\"{{target}}\",span_kind=\"SPAN_KIND_SERVER\"}[2m])) > 0)", Direction: DirectionIncrease, Source: "signals/apm.md [slug: apm-latency]", Priority: 70},
	{Mode: "node_not_ready", QueryAPI: "prometheus", Query: "min(kube_node_status_condition{cluster=\"{{target}}\",condition=\"Ready\",status=\"true\"})", Direction: DirectionDecrease, Source: "signals/k8s.md", Priority: 100},
	{Mode: "pod_crashloop", QueryAPI: "prometheus", Query: "clamp_min(sum(kube_pod_status_phase{cluster=\"{{target}}\",phase=\"Pending\"}) - sum(kube_pod_container_status_waiting{cluster=\"{{target}}\"}), 0)", Direction: DirectionIncrease, Source: "signals/k8s.md", Priority: 110},
	{Mode: "oom_kill", QueryAPI: "prometheus", Query: "sum(rate(kube_pod_container_status_restarts_total{cluster=\"{{target}}\"}[2m])) or vector(0)", Direction: DirectionIncrease, Source: "signals/k8s.md", Priority: 100},
	{Mode: "eval_quality_degraded", QueryAPI: "prometheus", Query: "avg(langsmith_eval_faithfulness_ratio{env=\"{{target}}\"})", SiblingQuery: "avg(langsmith_eval_faithfulness_ratio{env=\"{{sibling}}\"})", Direction: DirectionDecrease, Source: "signals/langsmith.md [slug: langsmith-eval]", Priority: 100},
	{Mode: "portkey_scrape_degraded", QueryAPI: "prometheus", Query: "avg(portkey_api_error_rate{env=\"{{target}}\"})", SiblingQuery: "avg(portkey_api_error_rate{env=\"{{sibling}}\"})", Direction: DirectionIncrease, Source: "signals/portkey.md [slug: portkey-analytics]", Priority: 100},
	{Mode: "web_vitals_degraded", QueryAPI: "loki", Query: "avg(avg_over_time({kind=\"measurement\"} |= \"type=web-vitals\" | logfmt | app_name=\"{{target}}\" | unwrap value_lcp [75s]))", Direction: DirectionIncrease, Source: "signals/logs.md [slug: logs-faro-rum]", Priority: 100},
	// Causal resource-axis modes (internal/causal; design 3.2 and 5). Their effect targets are
	// declared resource names, so none of these queries uses {{target}}: each observes the signal
	// family the mode moves across the whole stack (see Assertion.AnySeries for per-series ones).
	// Priorities mirror the legacy modes a migrated scenario replaces (latency_storm 70,
	// error_spike 80), so a scenario that mixes legacy and resource effects selects as before.
	{Mode: "dependency_slow", QueryAPI: "prometheus", Query: serverLatencyByService, AnySeries: true, Scopes: []ConsumerScope{appLatencyScope}, Direction: DirectionIncrease, Source: "signals/apm.md [slug: apm-latency]; a slow dependency stretches the hop of every node that calls it", Priority: 70},
	{Mode: "pool_exhausted", QueryAPI: "prometheus", Query: serverLatencyByService, AnySeries: true, Scopes: []ConsumerScope{appLatencyScope}, Direction: DirectionIncrease, Source: "signals/apm.md [slug: apm-latency]; acquire waits stretch the hop of every node that uses the pool", Priority: 70},
	{Mode: "rule_regression", QueryAPI: "prometheus", Query: "(sum(rate(traces_spanmetrics_calls_total{blueprint=\"{{blueprint}}\",span_kind=~\"SPAN_KIND_SERVER|SPAN_KIND_CLIENT\",status_code=\"STATUS_CODE_ERROR\"}[2m])) or vector(0)) / sum(rate(traces_spanmetrics_calls_total{blueprint=\"{{blueprint}}\",span_kind=~\"SPAN_KIND_SERVER|SPAN_KIND_CLIENT\"}[2m]))", Direction: DirectionIncrease, Source: "signals/apm.md [slug: apm-calls]; a rule with a 5xx error override fails SERVER spans, the default 4xx rejection fails the caller's CLIENT span (a 4xx SERVER span stays UNSET, so a rule met at an entry node with no caller is not visible in span metrics)", Priority: 80},
	{Mode: "dependency_unavailable", QueryAPI: "prometheus", Query: clientErrorRate, Direction: DirectionIncrease, Source: "signals/apm.md [slug: apm-calls]; a connect failure is a transport failure on the caller's CLIENT span", Priority: 80},
	{Mode: "credential_expired", QueryAPI: "prometheus", Query: clientErrorRate, Direction: DirectionIncrease, Source: "signals/apm.md [slug: apm-calls]; a handshake or 401 failure fails the caller's CLIENT span", Priority: 80},
	{Mode: "quota_reduced", QueryAPI: "prometheus", Query: "sum(rate(traces_service_graph_request_failed_total{blueprint=\"{{blueprint}}\"}[2m]) or rate(traces_service_graph_request_failed_total{client_blueprint=\"{{blueprint}}\"}[2m])) or vector(0)", Scopes: []ConsumerScope{{Kinds: []string{ConsumerApp}, Query: quotaFailedEdges}}, Direction: DirectionIncrease, Source: "signals/apm.md [slug: apm-service-graph]; a 429 answer fails the caller's edge; the scoped writer stamps a bare blueprint label on synthkit-emitted edges and a generator-derived edge carries client_blueprint, so both are read", Priority: 80},
	{Mode: "concurrency_reduced", QueryAPI: "prometheus", Query: "sum(aws_lambda_throttles_sum) or vector(0)", MinActive: throttleFloor, ScopeJoin: " + ", Scopes: []ConsumerScope{
		{Kinds: []string{ConsumerLambda}, Query: `sum(aws_lambda_throttles_sum{dimension_FunctionName=~"{{consumers.dimension_FunctionName}}"}) or vector(0)`},
		{Kinds: []string{ConsumerAgentcore}, Query: `sum(aws_bedrock_agentcore_throttles_sum{name=~"{{consumers.name}}"}) or vector(0)`},
	}, Direction: DirectionIncrease, Source: "signals/cw.md (aws_lambda_throttles); a reduced concurrency limit raises the bound function's Throttles", Priority: 90},
	{Mode: "consumer_slowdown", QueryAPI: "prometheus", Query: "aws_sqs_approximate_age_of_oldest_message_maximum or solace_queue_spool_usage_msgs", AnySeries: true, MinActive: backlogFloor, Scopes: []ConsumerScope{
		{Kinds: []string{ConsumerSQS}, Query: `aws_sqs_approximate_age_of_oldest_message_maximum{dimension_QueueName=~"{{consumers.dimension_QueueName}}"}`},
		{Kinds: []string{ConsumerSolace}, Query: `solace_queue_spool_usage_msgs{instance=~"{{consumers.instance}}",vpn_name=~"{{consumers.vpn_name}}",queue_name=~"{{consumers.queue_name}}"}`},
	}, Direction: DirectionIncrease, Source: "signals/cw.md (aws_sqs_approximate_age_of_oldest_message) and signals/solace.md (solace_queue_spool_usage_msgs); a slower consumer ages and deepens the bound queue", Priority: 90},
}

const (
	// throttleFloor is the least Throttles Sum (per 60-second emission) that counts for
	// concurrency_reduced. A healthy bound function throttles nothing and a legacy function with a
	// fractional throttles_per_min flips between 0 and 1 per period. A bound function carries
	// declared demand of 1 rps or more (design examples run 2 to 15 rps, 120 to 900 arrivals a
	// minute); once a cut limit bites, the reject share is 1 - limit/offered, so even a modest
	// shortfall rejects well over 5 a minute.
	throttleFloor = 5
	// backlogFloor is the least oldest-message age (seconds) or queue depth (messages) that counts
	// for consumer_slowdown. The harness settles 130 s after activation, so the oldest age cannot
	// exceed about that; 30 s needs the consumer to have lost roughly a quarter of the time since
	// activation (service rate at or under ~0.77 of demand), and 30 messages is the same deficit at
	// 1 msg/s. It rejects the sub-second ages and one-message blips an idle unrelated queue shows.
	backlogFloor = 30
	// serverLatencyByService is the per-series mean SERVER span latency. The 0.05/s floor (about six
	// observations in the two-minute window) keeps a nearly idle series from passing on noise.
	serverLatencyByService = "histogram_avg(rate(traces_spanmetrics_latency{" + serverSpans + "}[2m])) and histogram_count(rate(traces_spanmetrics_latency{" + serverSpans + "}[2m])) > 0.05"
	serverSpans            = `blueprint="{{blueprint}}",span_kind="SPAN_KIND_SERVER"`
	// scopedServerSpans is serverSpans narrowed to the services a stretch reaches and to the
	// environment the resource is declared in (span metrics carry both labels).
	scopedServerSpans = serverSpans + `,service=~"{{consumers.service}}",deployment_environment_name=~"{{consumers.deployment_environment_name}}"`
	// quotaFailedEdges is the service-graph failure rate of the edges into the services that use
	// the quota; the server label names the called service.
	quotaFailedEdges = `sum(rate(traces_service_graph_request_failed_total{blueprint="{{blueprint}}",server=~"{{consumers.service}}"}[2m]) or rate(traces_service_graph_request_failed_total{client_blueprint="{{blueprint}}",server=~"{{consumers.service}}"}[2m])) or vector(0)`
	clientErrorRate  = "sum(rate(traces_spanmetrics_calls_total{blueprint=\"{{blueprint}}\",span_kind=\"SPAN_KIND_CLIENT\",status_code=\"STATUS_CODE_ERROR\"}[2m])) or vector(0)"
)

// appLatencyScope narrows a server-latency observation to the services that use the resource and
// to their callers: a service without a server span of its own (a database or external leaf) shows
// its stretch on the services above it, whose request includes its hop.
var appLatencyScope = ConsumerScope{
	Kinds: []string{ConsumerApp, ConsumerAppCallers},
	Query: "histogram_avg(rate(traces_spanmetrics_latency{" + scopedServerSpans + "}[2m])) and histogram_count(rate(traces_spanmetrics_latency{" + scopedServerSpans + "}[2m])) > 0.05",
}

var placeholder = regexp.MustCompile(`\{\{consumers\.([A-Za-z0-9_]+)\}\}`)

// Scope narrows the assertion to the given consumers: for each scope whose kinds some consumer
// matches, the scope's query is rendered from those consumers' identity values (escaped as a
// PromQL string holding a regex, which the query API anchors), and the rendered queries are joined
// by ScopeJoin. A scope filtering on one label renders one regex alternation of the values; a
// scope filtering on several renders the query once per distinct identity, joined by " or ", so a
// series pairing one consumer's value of a label with another's is not selected. It returns the
// narrowed assertion and the number of consumers the rendered queries select. A scope renders only
// for consumers that carry every label its query filters on, so a partial identity never widens
// the match. When no scope renders the assertion is returned unchanged with 0, and the caller keeps
// its blueprint-wide observation.
func (a Assertion) Scope(consumers []Consumer) (Assertion, int) {
	var parts []string
	matched := 0
	for _, sc := range a.Scopes {
		var labels []string
		for _, m := range placeholder.FindAllStringSubmatch(sc.Query, -1) {
			if !slices.Contains(labels, m[1]) {
				labels = append(labels, m[1])
			}
		}
		sort.Strings(labels)
		var tuples [][]string // each consumer's escaped values, in label order
		n := 0
		for _, c := range consumers {
			if !kindIn(sc.Kinds, c.Kind) || !hasLabels(c.Identity, labels) {
				continue
			}
			n++
			t := make([]string, len(labels))
			for i, label := range labels {
				t[i] = promString(regexp.QuoteMeta(c.Identity[label]))
			}
			tuples = append(tuples, t)
		}
		if n == 0 {
			continue
		}
		matched += n
		parts = append(parts, renderScope(sc.Query, labels, tuples))
	}
	if len(parts) == 0 {
		return a, 0
	}
	join := a.ScopeJoin
	if join == "" {
		join = " or "
	}
	query := parts[0]
	if len(parts) > 1 {
		query = "(" + strings.Join(parts, ")"+join+"(") + ")"
	}
	scoped := a
	scoped.Query, scoped.Scopes, scoped.ScopeJoin = query, nil, ""
	return scoped, matched
}

// renderScope fills a scope query's placeholders: one alternation per label when it filters on a
// single label, one rendering per distinct value tuple otherwise.
func renderScope(query string, labels []string, tuples [][]string) string {
	fill := func(values []string) string {
		return placeholder.ReplaceAllStringFunc(query, func(ph string) string {
			return values[slices.Index(labels, placeholder.FindStringSubmatch(ph)[1])]
		})
	}
	if len(labels) == 1 {
		vs := make([]string, len(tuples))
		for i, t := range tuples {
			vs[i] = t[0]
		}
		sort.Strings(vs)
		return fill([]string{strings.Join(slices.Compact(vs), "|")})
	}
	keys := map[string][]string{}
	for _, t := range tuples {
		keys[strings.Join(t, "\x00")] = t
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	rendered := make([]string, len(sorted))
	for i, k := range sorted {
		rendered[i] = fill(keys[k])
	}
	if len(rendered) == 1 {
		return rendered[0]
	}
	return "(" + strings.Join(rendered, ") or (") + ")"
}

func kindIn(kinds []string, kind string) bool { return slices.Contains(kinds, kind) }

func hasLabels(identity map[string]string, labels []string) bool {
	for _, label := range labels {
		if _, ok := identity[label]; !ok {
			return false
		}
	}
	return true
}

// promString escapes a value for the inside of a double-quoted PromQL or LogQL string.
func promString(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// Assertions returns a copy so callers cannot mutate the catalogue.
func Assertions() []Assertion { return append([]Assertion(nil), assertions...) }

// AssertionFor returns the best available observation for one resolved scenario.
// Effects are considered independently, then ordered by assertion priority so a
// scenario with several effects selects the most direct reversible observation
// (for example, a crash-loop gauge instead of a cumulative profile total).
func AssertionFor(modes []string) (Assertion, bool) {
	byMode := make(map[string]Assertion, len(assertions))
	for _, a := range assertions {
		byMode[a.Mode] = a
	}
	var candidates []Assertion
	for _, mode := range modes {
		if a, ok := byMode[mode]; ok {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return Assertion{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Priority > candidates[j].Priority })
	return candidates[0], true
}
