// SPDX-License-Identifier: AGPL-3.0-only

package failuremode

import "sort"

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
// with the declared effect target; callers must escape it for their query API.
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
	Direction    Direction
	Source       string
	Priority     int
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
