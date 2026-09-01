// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/internal/allowlist"
	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core/coretest"
)

func TestDefaultAllowListsClusterMetricsFiltersWholeFamilies(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstructWithConfig(t, &k8scluster.Config{
		DefaultAllowLists: &allowlist.K8sMonitoringSelection{ClusterMetrics: true},
	}, cl)
	mc := &coretest.MetricCapture{}
	tick(t, c, mc, &coretest.LogCapture{})

	for _, name := range []string{
		"kube_pod_info",
		"container_cpu_usage_seconds_total",
		"node_cpu_seconds_total",
		"kubelet_running_pods",
	} {
		if !hasSeries(mc, name) {
			t.Errorf("allowed family %q was dropped", name)
		}
	}
	for _, name := range []string{
		"container_fs_usage_bytes",
		"node_disk_read_bytes_total",
	} {
		if got := mc.Find(name); len(got) != 0 {
			t.Errorf("disallowed family %q kept %d series", name, len(got))
		}
	}
}

func TestDefaultAllowListsNodeExporterModesAreDistinct(t *testing.T) {
	tests := []struct {
		name string
		mode allowlist.NodeExporterMode
		keep []string
		drop []string
	}{
		{
			name: "default",
			mode: allowlist.NodeExporterDefault,
			keep: []string{"node_cpu_guest_seconds_total"},
			drop: []string{"node_disk_read_bytes_total"},
		},
		{
			name: "integration",
			mode: allowlist.NodeExporterIntegration,
			keep: []string{"node_disk_read_bytes_total"},
			drop: []string{"node_cpu_guest_seconds_total"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := buildConstructWithConfig(t, &k8scluster.Config{
				DefaultAllowLists: &allowlist.K8sMonitoringSelection{NodeExporter: test.mode},
			}, coretest.Cluster())
			mc := &coretest.MetricCapture{}
			tick(t, c, mc, &coretest.LogCapture{})
			for _, name := range test.keep {
				if !hasSeries(mc, name) {
					t.Errorf("mode %q dropped %q", test.mode, name)
				}
			}
			for _, name := range test.drop {
				if got := mc.Find(name); len(got) != 0 {
					t.Errorf("mode %q kept %d series for %q", test.mode, len(got), name)
				}
			}
		})
	}
}

func TestDefaultAllowListsAbsentPreservesFullEmission(t *testing.T) {
	base := coretest.Cluster()
	legacy := buildConstruct(t, base)
	configured := buildConstructWithConfig(t, &k8scluster.Config{}, coretest.Cluster())
	legacyMetrics, configuredMetrics := &coretest.MetricCapture{}, &coretest.MetricCapture{}
	tick(t, legacy, legacyMetrics, &coretest.LogCapture{})
	tick(t, configured, configuredMetrics, &coretest.LogCapture{})
	if !reflect.DeepEqual(legacyMetrics.Names(), configuredMetrics.Names()) {
		t.Errorf("empty default_allow_lists changed families\nlegacy: %v\nconfig: %v", legacyMetrics.Names(), configuredMetrics.Names())
	}
}
