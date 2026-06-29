// SPDX-License-Identifier: AGPL-3.0-only

// Package cw centralizes the AWS CloudWatch metric-stream emission MECHANIC shared by
// every AWS construct (ec2, rds, elasticache, cwinfra, and the wave of AWS families to
// come). It owns three things and nothing else:
//
//   - the canonical CloudWatch per-period statistic suffix set (StatSuffixes) — the
//     trap-prone tail of the naming law (I6): _sum, _average, _maximum, _minimum,
//     _sample_count, spelled in exactly one place so no construct can typo them;
//   - the per-period-GAUGE rule (I5): every stat series is written with state.Set, never
//     state.Add — CloudWatch _sum is a per-period total, never a cumulative counter, so
//     rate()/increase() must never apply. EmitStats makes Add structurally impossible;
//   - per-suffix label isolation (I17 safety): each of the five series gets its own cloned
//     label map, so they never alias one map and a later clone-before-stamp stays correct.
//
// It deliberately owns NO value policy. The relationship between the five stats (is _sum =
// avg×count, or is the value already the per-period sum? does _maximum spread by a fixed
// factor or shape noise? is sample_count 60 or 1?) is genuinely per-metric and stays in
// each construct: the caller computes a StatSet from the metric's real semantics and hands
// it here. cw guarantees the suffixes, the gauge semantics, and the label isolation — not
// the numbers.
//
// It also does NOT generate metric base names. CloudWatch metric names carry published
// traps (cpuutilization has no underscore; 5_xx; un_healthy; ebsread_bytes) and the hard
// rule is NEVER invent a metric name — source it verbatim from signals/cw.md. So the caller
// passes the exact, sourced base (e.g. "aws_rds_cpuutilization"); cw only appends the
// statistic suffix. An algorithmic name mangler would invent names and is intentionally
// absent.
package cw

import "github.com/rknightion/synthkit/internal/state"

// StatSuffixes is the canonical CloudWatch per-period statistic suffix set, in the stable
// emission order. Exposed so constructs and tests reference one source of truth.
var StatSuffixes = []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"}

// StatSet holds the five per-period statistics CloudWatch reports for one metric sample.
// The caller fills it from the metric's real semantics; cw does not interpret the values.
type StatSet struct {
	Sum         float64
	Average     float64
	Maximum     float64
	Minimum     float64
	SampleCount float64
}

// EmitStats writes the five CloudWatch stat series for base (e.g. "aws_rds_cpuutilization")
// as per-period GAUGES via state.Set — never Add (I5). Each suffix gets its own cloned
// label map so the five series never alias one map (I17 clone-before-stamp safety).
func EmitStats(st *state.State, base string, lbls map[string]string, s StatSet) {
	st.Set(base+"_sum", cloneLabels(lbls), s.Sum)
	st.Set(base+"_average", cloneLabels(lbls), s.Average)
	st.Set(base+"_maximum", cloneLabels(lbls), s.Maximum)
	st.Set(base+"_minimum", cloneLabels(lbls), s.Minimum)
	st.Set(base+"_sample_count", cloneLabels(lbls), s.SampleCount)
}

// cloneLabels returns an independent copy so callers may reuse/mutate the source map and
// the five emitted series never share one map.
func cloneLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
