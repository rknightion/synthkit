// SPDX-License-Identifier: AGPL-3.0-only

package cw

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

type metricStreamReportCapture struct {
	report core.CloudWatchMetricStreamReport
}

func (c *metricStreamReportCapture) Write(context.Context, []otlp.MetricResource) error { return nil }

func (c *metricStreamReportCapture) RecordCloudWatchMetricStreamReport(report core.CloudWatchMetricStreamReport) {
	c.report = report
}

func TestStreamTablesHaveNoDuplicateBases(t *testing.T) {
	if len(streamTableDuplicateBases) != 0 {
		t.Fatalf("duplicate bases across stream tables: %v", streamTableDuplicateBases)
	}
}

func TestMergeStreamTablesDetectsDuplicateBases(t *testing.T) {
	original := slices.Clone(streamTableDuplicateBases)
	t.Cleanup(func() { streamTableDuplicateBases = original })
	mergeStreamTables(
		streamTable{entries: map[string]StreamEntry{"duplicate": {}}},
		streamTable{dimensions: map[string]map[string]string{"duplicate": {}}},
	)
	if !slices.Equal(streamTableDuplicateBases, []string{"duplicate"}) {
		t.Fatalf("duplicate bases=%v, want [duplicate]", streamTableDuplicateBases)
	}
}

func TestStreamTablesOwnTheirEntryNamespaces(t *testing.T) {
	tests := []struct {
		name       string
		table      streamTable
		namespaces []string
	}{
		{
			name: "cwinfra", table: streamTableCWInfra(),
			namespaces: []string{"AWS/ApplicationELB", "AWS/NetworkELB", "AWS/ELB", "AWS/EBS", "AWS/EKS", "AWS/Firehose", "AWS/Lambda", "AWS/NATGateway", "AWS/PrivateLinkEndpoints", "AWS/PrivateLinkServices", "AWS/S3", "AWS/SQS"},
		},
		{name: "rds-family", table: streamTableRDSFamily(), namespaces: []string{"AWS/RDS", "AWS/DocDB", "AWS/Neptune"}},
		{name: "cache-search-ec2", table: streamTableCacheSearchEC2(), namespaces: []string{"AWS/ElastiCache", "AWS/AOSS", "AWS/EC2"}},
		{name: "data-pipelines", table: streamTableDataPipelines(), namespaces: []string{"AWS/MWAA", "AmazonMWAA", "Glue"}},
		{name: "genai", table: streamTableGenAI(), namespaces: []string{"AWS/Bedrock", "AWS/Bedrock/Agents", "AWS/Bedrock/Guardrails"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for base, entry := range tt.table.entries {
				if !slices.ContainsFunc(tt.namespaces, func(namespace string) bool {
					return entry.Namespace == namespace || strings.HasPrefix(entry.Namespace, namespace+"/")
				}) {
					t.Errorf("%s namespace %q is outside owned namespaces %v", base, entry.Namespace, tt.namespaces)
				}
			}
		})
	}
}

// TestStreamEntriesUseCloudWatchMangledKeys guards the pairing between each manually
// sourced CloudWatch MetricName and its existing remote-write base. The production path
// deliberately does not derive names: this test mirrors the naming law in signals/cw.md
// (lower-to-upper boundaries split; an uppercase run stays with the following word) so a
// copied or mistyped lookup key cannot make an otherwise plausible entry look verified.
func TestStreamEntriesUseCloudWatchMangledKeys(t *testing.T) {
	bases := make([]string, 0, len(streamEntries))
	for base := range streamEntries {
		bases = append(bases, base)
	}
	slices.Sort(bases)
	for _, base := range bases {
		entry := streamEntries[base]
		if want := cloudWatchMangledBase(entry.Namespace, entry.MetricName); base != want {
			t.Errorf("base %q pairs %s/%s; want %q", base, entry.Namespace, entry.MetricName, want)
		}
	}
}

func TestFirehoseQuotaEntriesUseDocumentedCountUnit(t *testing.T) {
	for _, base := range []string{
		"aws_firehose_put_requests_per_second_limit",
		"aws_firehose_records_per_second_limit",
	} {
		entry, ok := Lookup(base)
		if !ok {
			t.Fatalf("Lookup(%q) not found", base)
		}
		if entry.Unit != "{Count}" {
			t.Errorf("Lookup(%q).Unit = %q, want {Count}", base, entry.Unit)
		}
	}
}

func TestFirehoseSuccessIsWithheldForIncompatibleConstructSemantics(t *testing.T) {
	if entry, ok := Lookup("aws_firehose_delivery_to_http_endpoint_success"); ok {
		t.Fatalf("Lookup returned incompatible Firehose success mapping: %#v", entry)
	}
}

func TestNATGatewayPeakPacketsUsesDocumentedCountUnit(t *testing.T) {
	entry, ok := Lookup("aws_natgateway_peak_packets_per_second")
	if !ok {
		t.Fatal("Lookup did not return NAT Gateway PeakPacketsPerSecond")
	}
	if entry.Unit != "{Count}" {
		t.Fatalf("Lookup unit = %q, want {Count}", entry.Unit)
	}
}

func cloudWatchMangledBase(namespace, metricName string) string {
	namespace = strings.TrimPrefix(namespace, "AWS/")
	namespace = strings.ReplaceAll(namespace, "/", "_")
	if namespace == "Glue" {
		// Glue's CloudWatch names carry a literal "glue." prefix that is already
		// represented by the namespace prefix in the Prometheus family name.
		metricName = strings.TrimPrefix(metricName, "glue.")
	}
	return "aws_" + strings.ToLower(namespace) + "_" + cloudWatchMangledPart(metricName)
}

func cloudWatchMangledPart(name string) string {
	runes := []rune(name)
	var out []rune
	separator := func() {
		if len(out) > 0 && out[len(out)-1] != '_' {
			out = append(out, '_')
		}
	}
	for i, r := range runes {
		switch {
		case r == '%':
			separator()
			out = append(out, []rune("percent")...)
		case r == '_' || r == '.' || r == '-' || unicode.IsSpace(r):
			separator()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])):
			separator()
			out = append(out, unicode.ToLower(r))
		default:
			out = append(out, unicode.ToLower(r))
		}
	}
	for len(out) > 0 && out[len(out)-1] == '_' {
		out = out[:len(out)-1]
	}
	return string(out)
}

func TestMetricStreamsUsesCapturedSummaryForm(t *testing.T) {
	now := time.Unix(1, 0)
	labels := map[string]string{
		"account_id": "111122223333", "region": "us-east-1", "namespace": "AWS/EC2",
		"dimension_InstanceId": "i-0123", "dimension_Endpoint_Type": "Interface", "job": "cloud/aws/ec2",
	}
	batch := statBatch("aws_ec2_cpuutilization", labels, StatSet{Sum: 90, Average: 45, Maximum: 60, Minimum: 30, SampleCount: 2}, now)
	resources, report := MetricStreams(&fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"}, batch)
	if report.Emitted != 1 || len(report.SkippedBases) != 0 {
		t.Fatalf("report=%+v", report)
	}
	if len(resources) != 1 || !resources[0].PreserveEmptyScope || len(resources[0].Metrics) != 1 {
		t.Fatalf("resources=%+v", resources)
	}
	metric := resources[0].Metrics[0]
	if metric.Name != "amazonaws.com/AWS/EC2/CPUUtilization" || metric.Unit != "%" || metric.Kind != otlp.MetricSummary {
		t.Fatalf("metric=%+v", metric)
	}
	point := metric.Summaries[0]
	if point.Count != 2 || point.Sum != 90 || point.Quantiles[0] != 30 || point.Quantiles[1] != 60 {
		t.Fatalf("summary=%+v", point)
	}
	if got, ok := point.Attrs["Dimensions"].(map[string]string); !ok || got["InstanceId"] != "i-0123" || len(got) != 1 {
		t.Fatalf("Dimensions=%#v", point.Attrs["Dimensions"])
	}
}

func TestMetricStreamsOmitsEmptyDimensionsAndCountsUnverifiedBase(t *testing.T) {
	now := time.Unix(1, 0)
	batch := statBatch("aws_rds_database_connections", map[string]string{"namespace": "AWS/RDS"}, StatSet{Sum: 6, Average: 6, Maximum: 6, Minimum: 6, SampleCount: 1}, now)
	batch = append(batch, statBatch("aws_docdb_read_latency", map[string]string{"namespace": "AWS/DocDB"}, StatSet{Sum: 1, Maximum: 1, Minimum: 1, SampleCount: 1}, now)...)
	resources, report := MetricStreams(&fixture.Cloud{}, batch)
	if report.Emitted != 1 {
		t.Fatalf("emitted=%d", report.Emitted)
	}
	if _, ok := report.SkippedBases["aws_docdb_read_latency"]; !ok || len(report.SkippedBases) != 1 {
		t.Fatalf("skipped=%v", report.SkippedBases)
	}
	if _, ok := resources[0].Metrics[0].Summaries[0].Attrs["Dimensions"]; ok {
		t.Fatalf("empty dimensions must be omitted: %#v", resources[0].Metrics[0].Summaries[0].Attrs)
	}
}

func TestMetricStreamsRepairsSummaryArithmeticWithoutChangingLegacyBatch(t *testing.T) {
	now := time.Unix(1, 0)
	labels := map[string]string{"namespace": "AWS/RDS"}
	batch := statBatch("aws_rds_database_connections", labels, StatSet{
		Sum: 7, Average: 7, Maximum: 7, Minimum: 7, SampleCount: 60,
	}, now)
	resources, _ := MetricStreams(&fixture.Cloud{}, batch)
	point := resources[0].Metrics[0].Summaries[0]
	if point.Sum != 420 || point.Count != 60 {
		t.Fatalf("native summary=(sum=%v,count=%d), want sum=420 count=60", point.Sum, point.Count)
	}
	if batch[0].Value != 7 {
		t.Fatalf("legacy _sum changed to %v, want 7", batch[0].Value)
	}
}

func TestWriteMetricStreamsWithoutWriterDoesNotPanic(t *testing.T) {
	now := time.Unix(1, 0)
	batch := statBatch("aws_ec2_cpuutilization", map[string]string{"namespace": "AWS/EC2"}, StatSet{
		Sum: 90, Average: 45, Maximum: 60, Minimum: 30, SampleCount: 2,
	}, now)

	report, err := WriteMetricStreams(nil, nil, &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"}, batch)
	if err != nil {
		t.Fatalf("WriteMetricStreams error = %v", err)
	}
	if report.Emitted != 1 || len(report.SkippedBases) != 0 {
		t.Fatalf("report = %+v, want one emitted verified base and no skipped bases", report)
	}
}

func TestWriteMetricStreamsReportsSkippedBasesToWriter(t *testing.T) {
	now := time.Unix(1, 0)
	batch := statBatch("aws_ec2_cpuutilization", map[string]string{"namespace": "AWS/EC2"}, StatSet{
		Sum: 2, Average: 1, Maximum: 1, Minimum: 1, SampleCount: 2,
	}, now)
	batch = append(batch, statBatch("aws_docdb_read_latency", map[string]string{"namespace": "AWS/DocDB"}, StatSet{
		Sum: 1, Average: 1, Maximum: 1, Minimum: 1, SampleCount: 1,
	}, now)...)
	writer := &metricStreamReportCapture{}
	if _, err := WriteMetricStreams(context.Background(), writer, &fixture.Cloud{}, batch); err != nil {
		t.Fatalf("WriteMetricStreams error = %v", err)
	}
	if got, want := writer.report.SkippedBases, []string{"aws_docdb_read_latency"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("reported skipped bases=%v, want %v", got, want)
	}
}

func statBatch(base string, labels map[string]string, stats StatSet, now time.Time) []promrw.Series {
	return []promrw.Series{
		{Name: base + "_sum", Labels: labels, Value: stats.Sum, T: now},
		{Name: base + "_average", Labels: labels, Value: stats.Average, T: now},
		{Name: base + "_maximum", Labels: labels, Value: stats.Maximum, T: now},
		{Name: base + "_minimum", Labels: labels, Value: stats.Minimum, T: now},
		{Name: base + "_sample_count", Labels: labels, Value: stats.SampleCount, T: now},
	}
}
