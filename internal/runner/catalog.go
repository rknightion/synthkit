// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"github.com/rknightion/synthkit/internal/construct/agentcore"
	"github.com/rknightion/synthkit/internal/construct/alloyhealth"
	"github.com/rknightion/synthkit/internal/construct/aoss"
	"github.com/rknightion/synthkit/internal/construct/argocd"
	"github.com/rknightion/synthkit/internal/construct/bedrock"
	"github.com/rknightion/synthkit/internal/construct/beylaagent"
	"github.com/rknightion/synthkit/internal/construct/certmanager"
	"github.com/rknightion/synthkit/internal/construct/cloudflare"
	"github.com/rknightion/synthkit/internal/construct/clusterautoscaler"
	"github.com/rknightion/synthkit/internal/construct/coredns"
	"github.com/rknightion/synthkit/internal/construct/cspazure"
	"github.com/rknightion/synthkit/internal/construct/cspgcp"
	"github.com/rknightion/synthkit/internal/construct/cwinfra"
	"github.com/rknightion/synthkit/internal/construct/dbo11ymysql"
	"github.com/rknightion/synthkit/internal/construct/dbo11ypg"
	"github.com/rknightion/synthkit/internal/construct/docdb"
	"github.com/rknightion/synthkit/internal/construct/ebscsi"
	"github.com/rknightion/synthkit/internal/construct/ec2"
	"github.com/rknightion/synthkit/internal/construct/elasticache"
	"github.com/rknightion/synthkit/internal/construct/envoygateway"
	"github.com/rknightion/synthkit/internal/construct/etcd"
	"github.com/rknightion/synthkit/internal/construct/extdns"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/construct/glue"
	"github.com/rknightion/synthkit/internal/construct/host"
	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/construct/k8sprofiling"
	"github.com/rknightion/synthkit/internal/construct/karpenter"
	"github.com/rknightion/synthkit/internal/construct/ksmingress"
	"github.com/rknightion/synthkit/internal/construct/langsmitheval"
	"github.com/rknightion/synthkit/internal/construct/langsmithplatform"
	"github.com/rknightion/synthkit/internal/construct/lbc"
	"github.com/rknightion/synthkit/internal/construct/mwaa"
	"github.com/rknightion/synthkit/internal/construct/neptune"
	"github.com/rknightion/synthkit/internal/construct/nettopo"
	"github.com/rknightion/synthkit/internal/construct/portkeygateway"
	"github.com/rknightion/synthkit/internal/construct/portkeypoller"
	"github.com/rknightion/synthkit/internal/construct/qualificationpipeline"
	"github.com/rknightion/synthkit/internal/construct/rds"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/construct/snowflake"
	"github.com/rknightion/synthkit/internal/construct/vpccni"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/workload/aiagent"
	"github.com/rknightion/synthkit/internal/workload/app"
	"github.com/rknightion/synthkit/internal/workload/webservice"
)

const (
	producerPromRW              = "promrw"
	producerUnlabelled          = "unlabelled"
	producerDBO11y              = "dbo11y"
	producerAzureMonitorManaged = "azure-monitor-managed"
	producerAzureMonitorAlloy   = "azure-monitor-alloy"
	producerOTLPNative          = "otlp-native"
)

func fixedMetricProducer(name string) func(any) string {
	return func(any) string { return name }
}

func withMetricProducer(reg core.ConstructReg, producer string) core.ConstructReg {
	reg.MetricProducer = fixedMetricProducer(producer)
	return reg
}

func withMetricProducers(reg core.ConstructReg, metric, native string) core.ConstructReg {
	reg.MetricProducer = fixedMetricProducer(metric)
	reg.OTLPMetricProducer = fixedMetricProducer(native)
	return reg
}

func k8sMetricAllowListProvenance(cfg any) (version, variant string) {
	configured, ok := cfg.(*k8scluster.Config)
	if !ok || configured == nil || configured.DefaultAllowLists == nil {
		return "", ""
	}
	return configured.DefaultAllowLists.Provenance()
}

func withWorkloadMetricProducers(reg core.WorkloadReg, metric, native string) core.WorkloadReg {
	reg.MetricProducer = fixedMetricProducer(metric)
	reg.OTLPMetricProducer = fixedMetricProducer(native)
	return reg
}

// azureMetricProducer follows the blueprint's explicit ingestion_path. The
// configuration value is direct producer wiring; no metric name, prefix, or
// emitted label is inspected.
func azureMetricProducer(cfg any) string {
	configured, ok := cfg.(*cspazure.Config)
	if !ok || configured == nil {
		return ""
	}
	switch configured.IngestionPath {
	case "serverless":
		return producerAzureMonitorManaged
	case "azure_exporter":
		return producerAzureMonitorAlloy
	default:
		return ""
	}
}

// Catalog assembles the v1 registry — the ONLY place construct/workload kinds are
// wired into the framework (single-owner wiring file; no init() self-registration
// anywhere). The blueprint loader validates YAML against exactly this set.
func Catalog() *core.Registry {
	reg := core.NewRegistry()

	// Topology constructs (resolver-emitted; empty configs, fixture-driven).
	k8s := withMetricProducers(k8scluster.Registration(), producerPromRW, producerOTLPNative)
	k8s.MetricAllowListProvenance = k8sMetricAllowListProvenance
	reg.RegisterConstruct(k8s)
	reg.RegisterConstruct(withMetricProducer(k8sprofiling.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(ec2.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(cwinfra.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(rds.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducers(host.Registration(), producerPromRW, producerOTLPNative))
	reg.RegisterConstruct(withMetricProducer(elasticache.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(dbo11ymysql.Registration(), producerDBO11y))
	reg.RegisterConstruct(withMetricProducer(dbo11ypg.Registration(), producerDBO11y))
	reg.RegisterConstruct(withMetricProducer(docdb.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(neptune.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(aoss.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(mwaa.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(glue.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(bedrock.Registration(), producerUnlabelled))
	reg.RegisterConstruct(withMetricProducer(agentcore.Registration(), producerUnlabelled))

	// Cluster add-ons (blueprint addons: list).
	reg.RegisterConstruct(withMetricProducer(lbc.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(extdns.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(coredns.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(vpccni.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(certmanager.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(etcd.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(clusterautoscaler.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(karpenter.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(argocd.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(envoygateway.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(ebscsi.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(ksmingress.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(alloyhealth.Registration(), producerPromRW))

	// Feature constructs (blueprint features: map).
	reg.RegisterConstruct(withMetricProducer(sm.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(fleetmgmt.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(cloudflare.Registration(), producerPromRW))
	azure := cspazure.Registration()
	azure.MetricProducer = azureMetricProducer
	reg.RegisterConstruct(azure)
	reg.RegisterConstruct(withMetricProducer(cspgcp.Registration(), producerUnlabelled))

	// AI integration constructs (blueprint integrations: map — Spec 2b scrape/poll sources).
	reg.RegisterConstruct(withMetricProducer(portkeygateway.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(portkeypoller.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(langsmithplatform.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(langsmitheval.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(snowflake.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(qualificationpipeline.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(beylaagent.Registration(), producerPromRW))
	reg.RegisterConstruct(withMetricProducer(nettopo.Registration(), producerPromRW))

	// Workloads.
	reg.RegisterWorkload(withWorkloadMetricProducers(webservice.Registration(), producerPromRW, producerOTLPNative))
	reg.RegisterWorkload(withWorkloadMetricProducers(app.Registration(), producerPromRW, producerOTLPNative))
	reg.RegisterWorkload(withWorkloadMetricProducers(aiagent.Registration(), producerPromRW, producerOTLPNative))

	return reg
}
