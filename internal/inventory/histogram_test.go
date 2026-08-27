// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"reflect"
	"testing"
)

func TestClassicHistogramFamilyTrimsOnlyComponentSuffixes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		base   string
		folded bool
	}{
		{name: "request_duration_seconds_bucket", base: "request_duration_seconds", folded: true},
		{name: "request_duration_seconds_sum", base: "request_duration_seconds", folded: true},
		{name: "request_duration_seconds_count", base: "request_duration_seconds", folded: true},
		{name: "request_duration_seconds", folded: false},
		{name: "requests_total", folded: false},
		{name: "_bucket", folded: false},
		// CloudWatch five-stat expansion emits a real `_sum` GAUGE family; the suffix alone
		// must never be treated as a fold.
		{name: "aws_rds_cpuutilization_sum", base: "aws_rds_cpuutilization", folded: true},
	} {
		base, folded := ClassicHistogramFamily(tc.name)
		if folded != tc.folded || (folded && base != tc.base) {
			t.Fatalf("ClassicHistogramFamily(%q) = (%q, %v), want (%q, %v)", tc.name, base, folded, tc.base, tc.folded)
		}
	}
}

func TestClassicHistogramProofRequiresAnObservedBucketSeries(t *testing.T) {
	t.Parallel()
	proof := ProveClassicHistogramsFromSeries([]map[string]string{
		{"__name__": "request_duration_seconds_bucket", "le": "0.5"},
		{"__name__": "request_duration_seconds_sum"},
		{"__name__": "request_duration_seconds_count"},
		// A CloudWatch stat family: a `_sum` with no bucket series anywhere.
		{"__name__": "aws_rds_cpuutilization_sum", "namespace": "AWS/RDS"},
		{"__name__": "aws_rds_cpuutilization_sample_count", "namespace": "AWS/RDS"},
		// A `_bucket` name with no `le` label is not a bucket series.
		{"__name__": "token_bucket_bucket"},
		{"__name__": "token_bucket_sum"},
	})

	for _, name := range []string{
		"request_duration_seconds_bucket", "request_duration_seconds_sum", "request_duration_seconds_count",
	} {
		family, ok := proof.Family(name)
		if !ok || family != "request_duration_seconds" {
			t.Fatalf("proof.Family(%q) = (%q, %v), want (request_duration_seconds, true)", name, family, ok)
		}
	}
	for _, name := range []string{
		"aws_rds_cpuutilization_sum", "aws_rds_cpuutilization_sample_count",
		"token_bucket_bucket", "token_bucket_sum", "request_duration_seconds",
	} {
		if family, ok := proof.Family(name); ok {
			t.Fatalf("proof.Family(%q) folded to %q; an unproven family must keep its own name so a "+
				"genuinely missing family still reports as a coverage gap", name, family)
		}
	}
}

func TestClassicHistogramEvidenceRecordsFiniteBoundsOnly(t *testing.T) {
	t.Parallel()
	bounded := ClassicHistogramEvidence(map[string]string{"le": "0.25"})
	if bounded == nil || !bounded.Classic || !reflect.DeepEqual(bounded.BucketBounds, []float64{0.25}) {
		t.Fatalf("evidence=%+v, want classic with bound 0.25", bounded)
	}
	for _, labels := range []map[string]string{{"le": "+Inf"}, {"job": "api"}, {"le": "not-a-number"}} {
		evidence := ClassicHistogramEvidence(labels)
		if evidence == nil || !evidence.Classic || len(evidence.BucketBounds) != 0 {
			t.Fatalf("evidence(%v)=%+v, want classic with no finite bound", labels, evidence)
		}
	}
}

func TestFoldClassicHistogramMetricsFoldsOnlyProvenFamilies(t *testing.T) {
	t.Parallel()
	schema := New()
	schema.AddMetric("kubeproxy_sync_proxy_rules_duration_seconds_bucket", "", InstrumentUnknown,
		map[string]string{"cluster": "", "le": ""}, nil)
	schema.AddMetric("kubeproxy_sync_proxy_rules_duration_seconds_sum", "", InstrumentUnknown,
		map[string]string{"cluster": "", "ip_family": ""}, nil)
	schema.AddMetric("kubeproxy_sync_proxy_rules_duration_seconds_count", "", InstrumentUnknown,
		map[string]string{"cluster": ""}, nil)
	schema.AddMetric("aws_rds_cpuutilization_sum", "", InstrumentGauge, map[string]string{"region": ""}, nil)

	folded := FoldClassicHistogramMetrics(schema)

	names := make([]string, 0, len(folded.Metrics))
	for _, metric := range folded.Metrics {
		names = append(names, metric.Name)
	}
	want := []string{"aws_rds_cpuutilization_sum", "kubeproxy_sync_proxy_rules_duration_seconds"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names=%v, want %v", names, want)
	}
	family := folded.Metrics[1]
	if family.Histogram == nil || !family.Histogram.Classic {
		t.Fatalf("histogram=%+v, want the classic representation the bucket series proves", family.Histogram)
	}
	labelKeys := make([]string, 0, len(family.Labels))
	for _, label := range family.Labels {
		labelKeys = append(labelKeys, label.Key)
	}
	if !reflect.DeepEqual(labelKeys, []string{"cluster", "ip_family", "le"}) {
		t.Fatalf("label keys=%v, want the union of the folded components", labelKeys)
	}
}

func TestFoldClassicHistogramMetricsNeverMasksAMissingFamily(t *testing.T) {
	t.Parallel()
	// Every stat family CloudWatch expands, with no bucket series anywhere in the document.
	schema := New()
	for _, suffix := range []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"} {
		schema.AddMetric("aws_ec2_cpu_utilization"+suffix, "", InstrumentGauge, map[string]string{"region": ""}, nil)
	}
	before := make([]string, 0, len(schema.Metrics))
	for _, metric := range schema.Metrics {
		before = append(before, metric.Name)
	}
	after := make([]string, 0, len(schema.Metrics))
	for _, metric := range FoldClassicHistogramMetrics(schema).Metrics {
		after = append(after, metric.Name)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("names=%v, want the unfolded stat families %v; folding a stat suffix would hide "+
			"a family synthkit does not emit behind one nobody emits", after, before)
	}
	if _, ok := ClassicHistogramFamily("aws_ec2_cpu_utilization_sample_count"); !ok {
		t.Fatal("guard: _sample_count does end in _count, so only the proof keeps it unfolded")
	}
}
