// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"
)

func TestParseYAMLStatsAndFamilyExpansion(t *testing.T) {
	contract, err := ParseSignals(map[string]string{
		"signals/cw.md": "```yaml signals\n" +
			"family: aws_applicationelb\n" +
			"scope: blueprint\n" +
			"sink: promrw\n" +
			"stats: [_sum, _average]\n" +
			"metrics:\n" +
			"  - {root: request_count, type: gauge, unit: count, v: ok}\n" +
			"info_series: aws_applicationelb_info\n" +
			"```\n",
	})
	if err != nil {
		t.Fatalf("ParseSignals() error = %v", err)
	}

	if got, want := contract.YAMLBlocks, 1; got != want {
		t.Fatalf("YAMLBlocks = %d, want %d", got, want)
	}
	dump, err := ParseDump(strings.NewReader("== metrics: series name → label keys ==\n" +
		"aws_applicationelb_request_count_sum  {[job]}\n" +
		"aws_applicationelb_request_count_average  {[job]}\n" +
		"aws_applicationelb_info  {[job]}\n"))
	if err != nil {
		t.Fatalf("ParseDump() error = %v", err)
	}
	report := Compare(contract, dump)
	if got, want := report.Resolved, 3; got != want {
		t.Errorf("Resolved = %d, want %d", got, want)
	}
	if got := len(report.UnresolvedNames); got != 0 {
		t.Errorf("UnresolvedNames = %v, want none", report.UnresolvedNames)
	}
}

func TestParseProseAlternativesAndDynamicNames(t *testing.T) {
	contract, err := ParseSignals(map[string]string{
		"signals/host.md": "## Native receiver\n" +
			"The families are `system.cpu.load_average.{1m,5m,15m}` and `system.cpu.time`.\n" +
			"| `system.disk.{io,merged}` | counter |\n",
	})
	if err != nil {
		t.Fatalf("ParseSignals() error = %v", err)
	}
	dump, err := ParseDump(strings.NewReader("== otlp metrics: series name → attribute keys ==\n" +
		"system.cpu.load_average.1m  {[host.name]}\n" +
		"system.cpu.load_average.5m  {[host.name]}\n" +
		"system.cpu.load_average.15m  {[host.name]}\n" +
		"system.cpu.time  {[cpu state]}\n" +
		"system.disk.io  {[device direction]}\n" +
		"system.disk.merged  {[device direction]}\n"))
	if err != nil {
		t.Fatalf("ParseDump() error = %v", err)
	}
	report := Compare(contract, dump)
	if got, want := report.Resolved, 6; got != want {
		t.Errorf("Resolved = %d, want %d", got, want)
	}
	if got := len(report.UnresolvedNames); got != 0 {
		t.Errorf("UnresolvedNames = %v, want none", report.UnresolvedNames)
	}
}

func TestNativeOTLPIsComparedAsItsOwnSection(t *testing.T) {
	contract, err := ParseSignals(map[string]string{
		"signals/otlp-metrics.md": "## Native OTLP metrics\n" +
			"| Family | Instrument | Unit |\n" +
			"|---|---|---|\n" +
			"| `http.server.request.duration` | histogram | `s` |\n" +
			"```yaml signals\n" +
			"family: http_server_request_duration_seconds\n" +
			"scope: blueprint\n" +
			"sink: otlp\n" +
			"metrics:\n" +
			"  - {root: http.server.request.duration, type: histogram, unit: seconds, v: ok}\n" +
			"```\n",
	})
	if err != nil {
		t.Fatalf("ParseSignals() error = %v", err)
	}
	dump, err := ParseDump(strings.NewReader("== metrics: series name → label keys ==\n" +
		"http_server_request_duration_seconds_bucket  {[le]}\n" +
		"== otlp metrics: series name → attribute keys ==\n" +
		"http.server.request.duration  {[http.route]}\n"))
	if err != nil {
		t.Fatalf("ParseDump() error = %v", err)
	}
	report := Compare(contract, dump)
	if got, want := report.Resolved, 1; got != want {
		t.Errorf("Resolved = %d, want %d", got, want)
	}
	if got, want := report.UnresolvedNames, []string{"http_server_request_duration_seconds_bucket"}; !equalStrings(got, want) {
		t.Errorf("UnresolvedNames = %v, want %v", got, want)
	}
}

func TestParseGapsAreNamedWithoutLosingRecoverableRoots(t *testing.T) {
	contract, err := ParseSignals(map[string]string{
		"signals/events.md": "```yaml signals\n" +
			"  family: event_stream\n" +
			"  scope: substrate\n" +
			"  sink: loki\n" +
			"  stream_labels:\n" +
			"    source: events\n" +
			"  stream_labels:\n" +
			"    level: info\n" +
			"```\n",
	})
	if err != nil {
		t.Fatalf("ParseSignals() error = %v", err)
	}
	if got, want := len(contract.ParseGaps), 1; got != want {
		t.Fatalf("ParseGaps = %d, want %d", got, want)
	}
	gap := contract.ParseGaps[0]
	if gap.File != "signals/events.md" || gap.Family != "event_stream" {
		t.Fatalf("ParseGap = %+v, want file and family", gap)
	}
}

func TestDumpParserRetainsLabelAndAttributeShapeBySection(t *testing.T) {
	dump, err := ParseDump(strings.NewReader("== metrics: series name → label keys ==\n" +
		"foo_total  {[instance job]}\n" +
		"== otlp metrics: series name → attribute keys ==\n" +
		"foo.bar  {[http.route service.name]}\n"))
	if err != nil {
		t.Fatalf("ParseDump() error = %v", err)
	}
	if got := dump.Metrics[DumpPrometheus]["foo_total"].Keys; !equalStrings(got, []string{"instance", "job"}) {
		t.Errorf("Prometheus keys = %v", got)
	}
	if got := dump.Metrics[DumpOTLPMetrics]["foo.bar"].Keys; !equalStrings(got, []string{"http.route", "service.name"}) {
		t.Errorf("OTLP keys = %v", got)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
