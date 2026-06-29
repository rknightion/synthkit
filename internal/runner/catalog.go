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
	"github.com/rknightion/synthkit/internal/workload/app"
	"github.com/rknightion/synthkit/internal/workload/webservice"
)

// Catalog assembles the v1 registry — the ONLY place construct/workload kinds are
// wired into the framework (single-owner wiring file; no init() self-registration
// anywhere). The blueprint loader validates YAML against exactly this set.
func Catalog() *core.Registry {
	reg := core.NewRegistry()

	// Topology constructs (resolver-emitted; empty configs, fixture-driven).
	reg.RegisterConstruct(k8scluster.Registration())
	reg.RegisterConstruct(k8sprofiling.Registration())
	reg.RegisterConstruct(ec2.Registration())
	reg.RegisterConstruct(cwinfra.Registration())
	reg.RegisterConstruct(rds.Registration())
	reg.RegisterConstruct(host.Registration())
	reg.RegisterConstruct(elasticache.Registration())
	reg.RegisterConstruct(dbo11ymysql.Registration())
	reg.RegisterConstruct(dbo11ypg.Registration())
	reg.RegisterConstruct(docdb.Registration())
	reg.RegisterConstruct(neptune.Registration())
	reg.RegisterConstruct(aoss.Registration())
	reg.RegisterConstruct(mwaa.Registration())
	reg.RegisterConstruct(glue.Registration())
	reg.RegisterConstruct(bedrock.Registration())
	reg.RegisterConstruct(agentcore.Registration())

	// Cluster add-ons (blueprint addons: list).
	reg.RegisterConstruct(lbc.Registration())
	reg.RegisterConstruct(extdns.Registration())
	reg.RegisterConstruct(coredns.Registration())
	reg.RegisterConstruct(vpccni.Registration())
	reg.RegisterConstruct(certmanager.Registration())
	reg.RegisterConstruct(etcd.Registration())
	reg.RegisterConstruct(clusterautoscaler.Registration())
	reg.RegisterConstruct(karpenter.Registration())
	reg.RegisterConstruct(argocd.Registration())
	reg.RegisterConstruct(envoygateway.Registration())
	reg.RegisterConstruct(ebscsi.Registration())
	reg.RegisterConstruct(ksmingress.Registration())
	reg.RegisterConstruct(alloyhealth.Registration())

	// Feature constructs (blueprint features: map).
	reg.RegisterConstruct(sm.Registration())
	reg.RegisterConstruct(fleetmgmt.Registration())
	reg.RegisterConstruct(cloudflare.Registration())
	reg.RegisterConstruct(cspazure.Registration())
	reg.RegisterConstruct(cspgcp.Registration())

	// AI integration constructs (blueprint integrations: map — Spec 2b scrape/poll sources).
	reg.RegisterConstruct(portkeygateway.Registration())
	reg.RegisterConstruct(portkeypoller.Registration())
	reg.RegisterConstruct(langsmithplatform.Registration())
	reg.RegisterConstruct(langsmitheval.Registration())
	reg.RegisterConstruct(snowflake.Registration())
	reg.RegisterConstruct(qualificationpipeline.Registration())
	reg.RegisterConstruct(beylaagent.Registration())
	reg.RegisterConstruct(nettopo.Registration())

	// Workloads.
	reg.RegisterWorkload(webservice.Registration())
	reg.RegisterWorkload(app.Registration())

	return reg
}
