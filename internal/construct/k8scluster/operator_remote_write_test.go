// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/allowlist"
	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func TestPrometheusOperatorRemoteWriteEnvelopeIsExplicitlyFamilyScoped(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane = fixture.ControlPlane{ApiServer: true, KubeProxy: true}
	c := buildConstructWithConfig(t, &k8scluster.Config{PrometheusOperatorRemoteWrite: &k8scluster.PrometheusOperatorRemoteWrite{
		Prometheus:        "monitoring/operator-demo",
		PrometheusReplica: "operator-demo-0",
	}}, cl)
	mc := &coretest.MetricCapture{}
	tick(t, c, mc, &coretest.LogCapture{})

	for _, family := range []string{
		"apiserver_request_total",
		"kubelet_runtime_operations_total",
		"kubeproxy_sync_proxy_rules_duration_seconds_bucket",
	} {
		series := findSeries(mc, family)
		if len(series) == 0 {
			t.Fatalf("%s: expected captured family", family)
		}
		for _, sample := range series {
			if got := sample.Labels["job"]; got != "apiserver" {
				t.Errorf("%s job = %q, want captured apiserver", family, got)
			}
			if got := sample.Labels["service"]; got != "kubernetes" {
				t.Errorf("%s service = %q, want captured kubernetes", family, got)
			}
			if got := sample.Labels["prometheus"]; got != "monitoring/operator-demo" {
				t.Errorf("%s prometheus = %q", family, got)
			}
			if got := sample.Labels["prometheus_replica"]; got != "operator-demo-0" {
				t.Errorf("%s prometheus_replica = %q", family, got)
			}
		}
	}

	for _, sample := range findSeries(mc, "kube_node_info") {
		if _, got := sample.Labels["prometheus"]; got {
			t.Errorf("unobserved kube_node_info gained prometheus label: %v", sample.Labels)
		}
		if got := sample.Labels["job"]; got != "integrations/kubernetes/kube-state-metrics" {
			t.Errorf("unobserved kube_node_info job = %q", got)
		}
	}
}

func TestPrometheusOperatorRemoteWritePreservesAllowListFiltering(t *testing.T) {
	c := buildConstructWithConfig(t, &k8scluster.Config{
		DefaultAllowLists: &allowlist.K8sMonitoringSelection{ClusterMetrics: true},
		PrometheusOperatorRemoteWrite: &k8scluster.PrometheusOperatorRemoteWrite{
			Prometheus:        "monitoring/operator-demo",
			PrometheusReplica: "operator-demo-0",
		},
	}, coretest.Cluster())
	mc := &coretest.MetricCapture{}
	tick(t, c, mc, &coretest.LogCapture{})

	if got := findSeries(mc, "kubelet_pleg_relist_duration_seconds_sum"); len(got) != 0 {
		t.Fatalf("allow-list excluded sum retained after Operator envelope: %d series", len(got))
	}
	if got := findSeries(mc, "kubelet_pleg_relist_duration_seconds_bucket"); len(got) == 0 {
		t.Fatal("allow-list allowed bucket was dropped")
	}
}

func TestPrometheusOperatorRemoteWriteRequiresBothExternalIdentities(t *testing.T) {
	_, err := k8scluster.New(&k8scluster.Config{PrometheusOperatorRemoteWrite: &k8scluster.PrometheusOperatorRemoteWrite{
		Prometheus: "monitoring/operator",
	}}, &fixture.Set{Cluster: coretest.Cluster()})
	if err == nil {
		t.Fatal("New accepted incomplete Prometheus Operator remote-write identity")
	}
}
