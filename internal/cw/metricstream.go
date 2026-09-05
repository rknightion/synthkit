// SPDX-License-Identifier: AGPL-3.0-only

package cw

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// The five calls below are the frozen ownership seam for the namespace-group lanes.
var streamEntries, streamDimensions = mergeStreamTables(
	streamTableCWInfra(),        // internal/cw/streamtable_cwinfra.go        L1
	streamTableRDSFamily(),      // internal/cw/streamtable_rdsfamily.go      L2: RDS, DocDB, Neptune
	streamTableCacheSearchEC2(), // internal/cw/streamtable_cachesearchec2.go L3: ElastiCache, AOSS, EC2
	streamTableDataPipelines(),  // internal/cw/streamtable_datapipelines.go  L4: MWAA, Glue
	streamTableGenAI(),          // internal/cw/streamtable_genai.go          L5: Bedrock, AgentCore
)

type streamTable struct {
	entries    map[string]StreamEntry
	dimensions map[string]map[string]string
}

// streamTableDuplicateBases is deliberately test-enforced rather than an init-time panic: a
// duplicate table entry fails the repository gate without making the package impossible to run.
var streamTableDuplicateBases []string

func mergeStreamTables(tables ...streamTable) (map[string]StreamEntry, map[string]map[string]string) {
	entries := make(map[string]StreamEntry)
	dimensions := make(map[string]map[string]string)
	seen := make(map[string]struct{})
	duplicates := make(map[string]struct{})
	for _, table := range tables {
		bases := make(map[string]struct{}, len(table.entries)+len(table.dimensions))
		for base := range table.entries {
			bases[base] = struct{}{}
		}
		for base := range table.dimensions {
			bases[base] = struct{}{}
		}
		for base := range bases {
			if _, ok := seen[base]; ok {
				duplicates[base] = struct{}{}
			}
			seen[base] = struct{}{}
		}
		for base, entry := range table.entries {
			entries[base] = entry
		}
		for base, allowed := range table.dimensions {
			dimensions[base] = allowed
		}
	}
	streamTableDuplicateBases = streamTableDuplicateBases[:0]
	for base := range duplicates {
		streamTableDuplicateBases = append(streamTableDuplicateBases, base)
	}
	slices.Sort(streamTableDuplicateBases)
	return entries, dimensions
}

// StreamName returns "amazonaws.com/{Namespace}/{MetricName}" for a doc-verified pair.
func StreamName(namespace, metricName string) string {
	return "amazonaws.com/" + namespace + "/" + metricName
}

// StreamEntry is the doc-verified CloudWatch identity behind a remote-write base.
type StreamEntry struct{ Namespace, MetricName, Unit string }

// Lookup returns an entry only after its AWS reference has been verified.
func Lookup(mangledBase string) (StreamEntry, bool) {
	e, ok := streamEntries[mangledBase]
	return e, ok
}

// EmitStream appends a captured-format Summary datapoint, omitting empty Dimensions.
func EmitStream(res *otlp.MetricResource, e StreamEntry, dims map[string]string, s StatSet, ts time.Time) {
	attrs := map[string]any{"Namespace": e.Namespace, "MetricName": e.MetricName}
	if len(dims) > 0 {
		attrs["Dimensions"] = dims
	}
	// CloudWatch Summary semantics require sum = average × sample count. The legacy
	// remote-write batch remains untouched; only the native conversion repairs callers that
	// represented a per-period value in both Sum and Average.
	summarySum := s.Sum
	if s.SampleCount > 0 {
		summarySum = s.Average * s.SampleCount
	}
	res.Metrics = append(res.Metrics, otlp.Metric{
		Name: StreamName(e.Namespace, e.MetricName), Unit: e.Unit, Kind: otlp.MetricSummary,
		Summaries: []otlp.SummaryPoint{{
			Attrs: attrs, Time: ts, Count: uint64(s.SampleCount), Sum: summarySum,
			Quantiles: map[float64]float64{0: s.Minimum, 1: s.Maximum},
		}},
	})
}

// StreamReport records the unverified remote-write bases withheld from metric-stream output.
// SkippedBases is keyed by base, so repeated dimension values count once.
type StreamReport struct {
	Emitted      int
	SkippedBases map[string]struct{}
}

// MetricStreams converts one CloudWatch construct's existing five-stat batch into the captured
// Metric Streams Summary form. Only dimension_* labels become the nested Dimensions map;
// CloudWatch scrape labels are transport metadata and never become datapoint attributes.
func MetricStreams(cloud *fixture.Cloud, batch []promrw.Series) ([]otlp.MetricResource, StreamReport) {
	report := StreamReport{SkippedBases: map[string]struct{}{}}
	type grouped struct {
		base   string
		labels map[string]string
		stats  StatSet
		seen   map[string]bool
		ts     time.Time
	}
	groups := map[string]*grouped{}
	for _, series := range batch {
		base, suffix, ok := streamBase(series.Name)
		if !ok {
			continue
		}
		key := base + "\x00" + state.LabelSig(series.Labels)
		group := groups[key]
		if group == nil {
			group = &grouped{base: base, labels: series.Labels, seen: map[string]bool{}, ts: series.T}
			groups[key] = group
		}
		group.seen[suffix] = true
		switch suffix {
		case "_sum":
			group.stats.Sum = series.Value
		case "_average":
			group.stats.Average = series.Value
		case "_maximum":
			group.stats.Maximum = series.Value
		case "_minimum":
			group.stats.Minimum = series.Value
		case "_sample_count":
			group.stats.SampleCount = series.Value
		}
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	resource := otlp.MetricResource{Attrs: map[string]any{
		"cloud.provider": "aws", "cloud.account.id": cloud.AccountID, "cloud.region": cloud.Region,
	}, PreserveEmptyScope: true}
	for _, key := range keys {
		group := groups[key]
		if !allStats(group.seen) {
			report.SkippedBases[group.base] = struct{}{}
			continue
		}
		entry, ok := Lookup(group.base)
		if !ok {
			report.SkippedBases[group.base] = struct{}{}
			continue
		}
		EmitStream(&resource, entry, dimensions(group.base, group.labels), group.stats, group.ts)
		report.Emitted++
	}
	if len(resource.Metrics) == 0 {
		return nil, report
	}
	return []otlp.MetricResource{resource}, report
}

// WriteMetricStreams sends the verified portion of a CloudWatch batch through the native OTLP
// writer. A batch containing only unverified bases intentionally produces no request.
func WriteMetricStreams(ctx context.Context, writer core.OTLPMetricWriter, cloud *fixture.Cloud, batch []promrw.Series) (StreamReport, error) {
	resources, report := MetricStreams(cloud, batch)
	if recorder, ok := writer.(core.CloudWatchMetricStreamReportRecorder); ok {
		skipped := make([]string, 0, len(report.SkippedBases))
		for base := range report.SkippedBases {
			skipped = append(skipped, base)
		}
		slices.Sort(skipped)
		recorder.RecordCloudWatchMetricStreamReport(core.CloudWatchMetricStreamReport{SkippedBases: skipped})
	}
	// A declared native lane can be exercised without a configured delivery sink (for
	// example by the full-estate integration capture, which only installs the legacy
	// Prometheus writer). Match the other optional writers' nil behaviour rather than
	// dereferencing a missing sink after rendering the batch.
	if len(resources) == 0 || writer == nil {
		return report, nil
	}
	return report, writer.Write(ctx, resources)
}

func streamBase(name string) (string, string, bool) {
	for _, suffix := range StatSuffixes {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix), suffix, true
		}
	}
	return "", "", false
}

func allStats(seen map[string]bool) bool {
	for _, suffix := range StatSuffixes {
		if !seen[suffix] {
			return false
		}
	}
	return true
}

func dimensions(base string, labels map[string]string) map[string]string {
	allowed := streamDimensions[base]
	if len(allowed) == 0 {
		return nil
	}
	var out map[string]string
	for label, dimension := range allowed {
		value := labels[label]
		if value == "" || value == "NA" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[dimension] = value
	}
	return out
}
