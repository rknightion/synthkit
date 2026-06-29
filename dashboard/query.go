// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"fmt"
	"sort"
	"strings"
)

// Selector builds a PromQL label matcher set. For ScopeBlueprint it prepends
// blueprint="<label>"; ScopeSubstrate omits it. extra matchers are appended in sorted
// key order. An absent dimension is OMITTED — never "" (matches the emit-side invariant).
func Selector(sig MetricSignal, label string, extra map[string]string) string {
	var parts []string
	if sig.Scope == ScopeBlueprint && label != "" {
		parts = append(parts, fmt.Sprintf(`blueprint=%q`, label))
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if extra[k] == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf(`%s=%q`, k, extra[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// RateExpr is the counter query form: sum by (group) (rate(name<sel>[$__rate_interval])).
// When groupBy is empty the "by ()" clause is omitted: sum (...).
func RateExpr(name, selector string, groupBy []string) string {
	return fmt.Sprintf("sum%s (rate(%s%s[$__rate_interval]))", aggBy(groupBy), name, selector)
}

// ClassicHistogramQuantile is the CLASSIC histogram query (hand-mangled _bucket): le is
// always part of the grouping. base is the family base (no _bucket suffix).
func ClassicHistogramQuantile(q float64, base, selector string, groupBy []string) string {
	groups := append([]string{"le"}, groupBy...)
	return fmt.Sprintf("histogram_quantile(%s, sum by (%s) (rate(%s_bucket%s[$__rate_interval])))",
		trimFloat(q), strings.Join(groups, ", "), base, selector)
}

// CWGauge graphs a CloudWatch per-period statistic as a GAUGE (never rate). stat is one of
// sum|average|maximum|minimum|sample_count (without the leading underscore).
func CWGauge(base, stat, selector string, groupBy []string) string {
	return fmt.Sprintf("avg by (%s) (%s_%s%s)", strings.Join(groupBy, ", "), base, stat, selector)
}

func trimFloat(f float64) string {
	return fmt.Sprintf("%g", f)
}

// aggBy renders " by (a, b)" for a non-empty group list, or "" when empty — so callers emit
// `sum (...)` rather than the noisier `sum by () (...)`.
func aggBy(groupBy []string) string {
	if len(groupBy) == 0 {
		return ""
	}
	return " by (" + strings.Join(groupBy, ", ") + ")"
}

// NativeHistogramQuantile is the NATIVE (exponential) histogram query: NO le, NO _bucket —
// histogram_quantile over the bare series. Used for the Tempo-metrics-generator-derived span
// histograms synthkit dual-emits (see signals/apm.md [slug: apm-latency]).
func NativeHistogramQuantile(q float64, name, selector string, groupBy []string) string {
	return fmt.Sprintf("histogram_quantile(%s, sum%s (rate(%s%s[$__rate_interval])))",
		trimFloat(q), aggBy(groupBy), name, selector)
}

// GaugeExpr graphs a gauge as an average over the selected series (avg, not rate).
func GaugeExpr(name, selector string, groupBy []string) string {
	return fmt.Sprintf("avg%s (%s%s)", aggBy(groupBy), name, selector)
}

// NativeHistogramRate returns the PromQL rate expression for a native (exponential) histogram.
// Unlike ClassicHistogramQuantile there is no _bucket suffix and no le label — native histograms
// carry their bucket layout internally. The result is suitable as input to NativeHistogramHeatmap
// via PromTarget. selector is a label-matcher set (e.g. from Selector()).
func NativeHistogramRate(name, selector string) string {
	return fmt.Sprintf("sum(rate(%s%s[$__rate_interval]))", name, selector)
}
