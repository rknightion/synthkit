// SPDX-License-Identifier: AGPL-3.0-only

package allowlist

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestK8sMonitoringPinnedSources(t *testing.T) {
	if K8sMonitoringChartVersion != "4.5.0" {
		t.Fatalf("chart version = %q, want 4.5.0", K8sMonitoringChartVersion)
	}
	if K8sMonitoringChartSHA256 != "82cacb812c260f8ac2384322c89e82bedf3d5f00ce3133564965c0ad7a83b670" {
		t.Fatalf("chart SHA-256 = %q", K8sMonitoringChartSHA256)
	}
	for _, source := range K8sMonitoringSources() {
		data, err := chartFS.ReadFile(source.Path)
		if err != nil {
			t.Fatalf("read %s: %v", source.Path, err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != source.SHA256 {
			t.Errorf("%s SHA-256 = %s, want %s", source.Path, got, source.SHA256)
		}
		patterns, err := parseYAMLList(data)
		if err != nil {
			t.Errorf("parse %s: %v", source.Path, err)
			continue
		}
		if len(patterns) != source.EntryCount {
			t.Errorf("%s entries = %d, want %d", source.Path, len(patterns), source.EntryCount)
		}
	}
}

func TestK8sMonitoringProjectionSelections(t *testing.T) {
	tests := []struct {
		name      string
		selection K8sMonitoringSelection
		checks    []struct {
			source string
			metric string
			keep   bool
		}
	}{
		{
			name: "full emission is the zero value",
			checks: []struct {
				source string
				metric string
				keep   bool
			}{{SourceKubeStateMetrics, "kube_endpoint_info", true}, {SourceNodeExporter, "node_cpu_guest_seconds_total", true}},
		},
		{
			name:      "cluster defaults filter each selected collector",
			selection: K8sMonitoringSelection{ClusterMetrics: true},
			checks: []struct {
				source string
				metric string
				keep   bool
			}{
				{SourceKubeStateMetrics, "kube_pod_info", true},
				{SourceKubeStateMetrics, "kube_endpoint_info", false},
				{SourceCadvisor, "container_cpu_usage_seconds_total", true},
				{SourceCadvisor, "container_fs_usage_bytes", false},
				{SourceNodeExporter, "node_cpu_guest_seconds_total", true},
				{SourceNodeExporter, "node_disk_read_bytes_total", false},
			},
		},
		{
			name:      "node exporter integration is distinguishable",
			selection: K8sMonitoringSelection{NodeExporter: NodeExporterIntegration},
			checks: []struct {
				source string
				metric string
				keep   bool
			}{
				{SourceNodeExporter, "node_disk_read_bytes_total", true},
				{SourceNodeExporter, "node_cpu_guest_seconds_total", false},
				{SourceKubeStateMetrics, "kube_endpoint_info", true},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection, err := NewK8sMonitoringProjection(test.selection)
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range test.checks {
				if got := projection.Keep(check.source, check.metric); got != check.keep {
					t.Errorf("Keep(%q, %q) = %t, want %t", check.source, check.metric, got, check.keep)
				}
			}
		})
	}
}

func TestK8sMonitoringProjectionRejectsUnknownNodeExporterMode(t *testing.T) {
	_, err := NewK8sMonitoringProjection(K8sMonitoringSelection{NodeExporter: "all"})
	if err == nil {
		t.Fatal("unknown node-exporter mode was accepted")
	}
}
