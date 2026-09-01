// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"testing"

	"github.com/rknightion/synthkit/internal/allowlist"
	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core"
)

func TestCatalogCarriesSelectedAllowListProducerProvenance(t *testing.T) {
	reg, ok := Catalog().Construct("k8s_cluster")
	if !ok {
		t.Fatal("k8s_cluster registration missing")
	}
	cfg := &k8scluster.Config{DefaultAllowLists: &allowlist.K8sMonitoringSelection{
		ClusterMetrics: true, NodeExporter: allowlist.NodeExporterIntegration,
	}}
	got, err := resolveMetricProducers(reg.Kind, cfg, []core.SignalClass{core.Metrics}, reg.MetricProducer, reg.OTLPMetricProducer, reg.MetricAllowListProvenance)
	if err != nil {
		t.Fatal(err)
	}
	if got.promRW != producerPromRW || got.promRWAllowListVersion != allowlist.K8sMonitoringChartVersion || got.promRWAllowListVariant != "cluster-metrics,node-exporter=integration" {
		t.Fatalf("producer provenance=%+v, want explicit promrw chart selection", got)
	}
}
