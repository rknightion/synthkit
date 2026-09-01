// SPDX-License-Identifier: AGPL-3.0-only

// Package allowlist loads version-pinned upstream metric allow-lists and projects
// whole Prometheus metric families at the owning emission boundary.
package allowlist

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"regexp"
	"strings"
)

const (
	// K8sMonitoringChartVersion is the chart release that supplied the embedded
	// allow-list YAML. Refresh this data only from the matching release artefact.
	K8sMonitoringChartVersion = "4.5.0"
	// K8sMonitoringChartURL is the immutable release artefact used to pin the YAML.
	K8sMonitoringChartURL = "https://github.com/grafana/helm-charts/releases/download/k8s-monitoring-4.5.0/k8s-monitoring-4.5.0.tgz"
	// K8sMonitoringChartSHA256 verifies that release artefact.
	K8sMonitoringChartSHA256 = "82cacb812c260f8ac2384322c89e82bedf3d5f00ce3133564965c0ad7a83b670"
)

// Source identifies one pinned YAML file and its source artefact provenance.
type Source struct {
	Path       string
	Upstream   string
	SHA256     string
	EntryCount int
}

const (
	SourceCadvisor          = "cadvisor"
	SourceKubeStateMetrics  = "kube-state-metrics"
	SourceKubelet           = "kubelet"
	SourceKubeletProbes     = "kubelet-probes"
	SourceKubeletResource   = "kubelet-resource"
	SourceOpenCost          = "opencost"
	SourceKepler            = "kepler"
	SourceWindowsExporter   = "windows-exporter"
	SourceNodeExporter      = "node-exporter"
	NodeExporterDefault     = NodeExporterMode("default")
	NodeExporterIntegration = NodeExporterMode("integration")
)

// NodeExporterMode selects one of the chart's independently switchable
// node-exporter allow-lists. The empty mode leaves node-exporter unfiltered unless
// ClusterMetrics is selected, in which case the chart-default mode is used.
type NodeExporterMode string

// K8sMonitoringSelection is the construct-level configuration surface for the
// k8s-monitoring allow-list projection. It is deliberately independent of a
// blueprint: the composition root decodes the public declaration into this config.
type K8sMonitoringSelection struct {
	ClusterMetrics bool             `yaml:"cluster_metrics"`
	NodeExporter   NodeExporterMode `yaml:"node_exporter"`
}

// Provenance returns the pinned chart version and the selected, stable variant
// identifier for inventory comparison. An unfiltered selection returns empty
// values because it did not select an allow-list at all.
func (selection K8sMonitoringSelection) Provenance() (version, variant string) {
	if !selection.ClusterMetrics && selection.NodeExporter == "" {
		return "", ""
	}
	nodeExporter := selection.NodeExporter
	if nodeExporter == "" && selection.ClusterMetrics {
		nodeExporter = NodeExporterDefault
	}
	parts := make([]string, 0, 2)
	if selection.ClusterMetrics {
		parts = append(parts, "cluster-metrics")
	}
	if nodeExporter != "" {
		parts = append(parts, "node-exporter="+string(nodeExporter))
	}
	return K8sMonitoringChartVersion, strings.Join(parts, ",")
}

// Projection applies the selected upstream lists by collector source. A source not
// selected remains untouched, so choosing a host-metrics node-exporter list does not
// accidentally remove unrelated cluster-metrics families.
type Projection struct {
	filters map[string]matcher
}

type matcher []*regexp.Regexp

// Enabled reports whether any source is projected.
func (p Projection) Enabled() bool { return len(p.filters) != 0 }

// Keep reports whether a metric family survives the list selected for source. The
// decision is only a function of source and metric family name; callers retain or
// drop every series in that family together.
func (p Projection) Keep(source, name string) bool {
	patterns, selected := p.filters[source]
	if !selected {
		return true
	}
	for _, pattern := range patterns {
		if pattern.MatchString(name) {
			return true
		}
	}
	return false
}

// NewK8sMonitoringProjection returns the projection represented by selection.
// Full emission remains the zero-value configuration.
func NewK8sMonitoringProjection(selection K8sMonitoringSelection) (Projection, error) {
	if selection.NodeExporter != "" && selection.NodeExporter != NodeExporterDefault && selection.NodeExporter != NodeExporterIntegration {
		return Projection{}, fmt.Errorf("k8s-monitoring node_exporter allow-list %q: want default or integration", selection.NodeExporter)
	}
	filters := map[string]matcher{}
	if selection.ClusterMetrics {
		for source := range clusterMetricFiles {
			m, err := loadMatcher(clusterMetricFiles[source])
			if err != nil {
				return Projection{}, err
			}
			filters[source] = m
		}
	}

	mode := selection.NodeExporter
	if mode == "" && selection.ClusterMetrics {
		mode = NodeExporterDefault
	}
	if mode != "" {
		m, err := loadMatcher(nodeExporterFiles[mode])
		if err != nil {
			return Projection{}, err
		}
		filters[SourceNodeExporter] = m
	}
	return Projection{filters: filters}, nil
}

// K8sMonitoringSources returns a copy of every embedded upstream source record.
func K8sMonitoringSources() []Source {
	out := make([]Source, len(k8sMonitoringSources))
	copy(out, k8sMonitoringSources)
	return out
}

//go:embed chart/k8s-monitoring/4.5.0/cluster-metrics/cadvisor.yaml
//go:embed chart/k8s-monitoring/4.5.0/cluster-metrics/kube-state-metrics.yaml
//go:embed chart/k8s-monitoring/4.5.0/cluster-metrics/kubelet.yaml
//go:embed chart/k8s-monitoring/4.5.0/cluster-metrics/kubelet_probes.yaml
//go:embed chart/k8s-monitoring/4.5.0/cluster-metrics/kubelet_resource.yaml
//go:embed chart/k8s-monitoring/4.5.0/cluster-metrics/opencost.yaml
//go:embed chart/k8s-monitoring/4.5.0/cluster-metrics/kepler.yaml
//go:embed chart/k8s-monitoring/4.5.0/cluster-metrics/windows-exporter.yaml
//go:embed chart/k8s-monitoring/4.5.0/host-metrics/node-exporter.yaml
//go:embed chart/k8s-monitoring/4.5.0/host-metrics/node-exporter-integration.yaml
var chartFS embed.FS

const upstreamRoot = "k8s-monitoring/charts"

var k8sMonitoringSources = []Source{
	{Path: "chart/k8s-monitoring/4.5.0/cluster-metrics/cadvisor.yaml", Upstream: upstreamRoot + "/feature-cluster-metrics/default-allow-lists/cadvisor.yaml", SHA256: "754694ca98673af65b08c0522f3bbe25f8c5a08a224571a870dc07e0c4b9aa61", EntryCount: 19},
	{Path: "chart/k8s-monitoring/4.5.0/cluster-metrics/kube-state-metrics.yaml", Upstream: upstreamRoot + "/feature-cluster-metrics/default-allow-lists/kube-state-metrics.yaml", SHA256: "5ad7366a73a23a916e6b655c58a0a4d870f39b66db0d6deb54b12e2574924b12", EntryCount: 43},
	{Path: "chart/k8s-monitoring/4.5.0/cluster-metrics/kubelet.yaml", Upstream: upstreamRoot + "/feature-cluster-metrics/default-allow-lists/kubelet.yaml", SHA256: "68fc6ebd127d6476c6a61bdc1bd338a2a27833e6e9050bbfb8c302aac0d6083f", EntryCount: 36},
	{Path: "chart/k8s-monitoring/4.5.0/cluster-metrics/kubelet_probes.yaml", Upstream: upstreamRoot + "/feature-cluster-metrics/default-allow-lists/kubelet_probes.yaml", SHA256: "70ba5ec661377b72555b9bd47bf8b59443aba6e12021d3b3c178181418b9e671", EntryCount: 1},
	{Path: "chart/k8s-monitoring/4.5.0/cluster-metrics/kubelet_resource.yaml", Upstream: upstreamRoot + "/feature-cluster-metrics/default-allow-lists/kubelet_resource.yaml", SHA256: "b450edd1276cd178769bf7e7e0f9e68c15c4bfa060a6d1b1658c4f45dd491d7c", EntryCount: 2},
	{Path: "chart/k8s-monitoring/4.5.0/cluster-metrics/opencost.yaml", Upstream: upstreamRoot + "/feature-cluster-metrics/default-allow-lists/opencost.yaml", SHA256: "f91cfd2501d4f62413b88eb51f7f1e1cb64f1939a1624adf250475119c73b2ea", EntryCount: 25},
	{Path: "chart/k8s-monitoring/4.5.0/cluster-metrics/kepler.yaml", Upstream: upstreamRoot + "/feature-cluster-metrics/default-allow-lists/kepler.yaml", SHA256: "2b826a8ad39f6ecdbe5fcd549b843bc089348c4843bd66ec299697ef46c265a5", EntryCount: 1},
	{Path: "chart/k8s-monitoring/4.5.0/cluster-metrics/windows-exporter.yaml", Upstream: upstreamRoot + "/feature-cluster-metrics/default-allow-lists/windows-exporter.yaml", SHA256: "30576c3e384697eb289bd88d48040f564d6476f9eb6243dd93d184d15943ae87", EntryCount: 5},
	{Path: "chart/k8s-monitoring/4.5.0/host-metrics/node-exporter.yaml", Upstream: upstreamRoot + "/feature-host-metrics/default-allow-lists/node-exporter.yaml", SHA256: "9af6dd7dad5bf9de682cfbc19143a40e40bafd00c6d51018684ff2b67dedb8e8", EntryCount: 10},
	{Path: "chart/k8s-monitoring/4.5.0/host-metrics/node-exporter-integration.yaml", Upstream: upstreamRoot + "/feature-host-metrics/default-allow-lists/node-exporter-integration.yaml", SHA256: "e75cf0ffd218ef39d811474a24377b0e6073606e2429c5b39828396a8f34733c", EntryCount: 155},
}

var clusterMetricFiles = map[string]Source{
	SourceCadvisor:         k8sMonitoringSources[0],
	SourceKubeStateMetrics: k8sMonitoringSources[1],
	SourceKubelet:          k8sMonitoringSources[2],
	SourceKubeletProbes:    k8sMonitoringSources[3],
	SourceKubeletResource:  k8sMonitoringSources[4],
	SourceOpenCost:         k8sMonitoringSources[5],
	SourceKepler:           k8sMonitoringSources[6],
	SourceWindowsExporter:  k8sMonitoringSources[7],
}

var nodeExporterFiles = map[NodeExporterMode]Source{
	NodeExporterDefault:     k8sMonitoringSources[8],
	NodeExporterIntegration: k8sMonitoringSources[9],
}

func loadMatcher(source Source) (matcher, error) {
	data, err := chartFS.ReadFile(source.Path)
	if err != nil {
		return nil, fmt.Errorf("read pinned k8s-monitoring allow-list %s: %w", source.Path, err)
	}
	gotSHA := fmt.Sprintf("%x", sha256.Sum256(data))
	if gotSHA != source.SHA256 {
		return nil, fmt.Errorf("pinned k8s-monitoring allow-list %s SHA-256 = %s, want %s", source.Path, gotSHA, source.SHA256)
	}
	patterns, err := parseYAMLList(data)
	if err != nil {
		return nil, fmt.Errorf("parse pinned k8s-monitoring allow-list %s: %w", source.Path, err)
	}
	if len(patterns) != source.EntryCount {
		return nil, fmt.Errorf("pinned k8s-monitoring allow-list %s entries = %d, want %d", source.Path, len(patterns), source.EntryCount)
	}
	result := make(matcher, 0, len(patterns))
	for _, pattern := range patterns {
		compiled, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			return nil, fmt.Errorf("compile %q: %w", pattern, err)
		}
		result = append(result, compiled)
	}
	return result, nil
}

func parseYAMLList(data []byte) ([]string, error) {
	var patterns []string
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			return nil, fmt.Errorf("line %d is not a YAML list item", lineNo+1)
		}
		pattern := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if pattern == "" {
			return nil, fmt.Errorf("line %d has an empty metric pattern", lineNo+1)
		}
		patterns = append(patterns, pattern)
	}
	if len(patterns) == 0 {
		return nil, fmt.Errorf("has no metric patterns")
	}
	return patterns, nil
}
