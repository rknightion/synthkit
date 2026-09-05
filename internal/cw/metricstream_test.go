// SPDX-License-Identifier: AGPL-3.0-only

package cw

import (
	"context"
	"testing"
	"time"

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
	batch = append(batch, statBatch("aws_rds_freeable_memory", map[string]string{"namespace": "AWS/RDS"}, StatSet{Sum: 1, Maximum: 1, Minimum: 1, SampleCount: 1}, now)...)
	resources, report := MetricStreams(&fixture.Cloud{}, batch)
	if report.Emitted != 1 {
		t.Fatalf("emitted=%d", report.Emitted)
	}
	if _, ok := report.SkippedBases["aws_rds_freeable_memory"]; !ok || len(report.SkippedBases) != 1 {
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
	batch = append(batch, statBatch("aws_rds_freeable_memory", map[string]string{"namespace": "AWS/RDS"}, StatSet{
		Sum: 1, Average: 1, Maximum: 1, Minimum: 1, SampleCount: 1,
	}, now)...)
	writer := &metricStreamReportCapture{}
	if _, err := WriteMetricStreams(context.Background(), writer, &fixture.Cloud{}, batch); err != nil {
		t.Fatalf("WriteMetricStreams error = %v", err)
	}
	if got, want := writer.report.SkippedBases, []string{"aws_rds_freeable_memory"}; len(got) != len(want) || got[0] != want[0] {
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
