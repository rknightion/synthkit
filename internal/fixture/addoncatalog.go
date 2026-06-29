// SPDX-License-Identifier: AGPL-3.0-only

package fixture

// AddonWorkloads returns a fresh slice of Workload templates for the named addon key.
// Each template has Name/Namespace/Replicas/Controller/Container set to match the real
// pod-shape captured from a live reference cluster (2026-06-16 recon). PodNames and
// NodeIdx are left nil — they are minted at resolve time via WorkloadPodNames.
//
// Unknown key returns nil. The returned slice is always a fresh copy — callers may mutate
// it without affecting future calls.
//
// Covered addon keys: cert_manager, core_dns, external_dns, load_balancer_controller,
// cluster_autoscaler, karpenter, argocd, envoy_gateway, metrics_server, ebs_csi.
//
// NOT covered (stay in k8scluster's existing substrate emission as name-only DaemonSets):
// aws-node, kube-proxy, node-exporter, ebs-csi-node. alloy-metrics/alloy-logs are carried by
// MonitoringBaselineWorkloads() (gated on k8s_monitoring), not here.
func AddonWorkloads(key string) []Workload {
	switch key {
	case "ebs_csi":
		// ebs-csi-controller is a 2-replica Deployment in kube-system (live reference capture,
		// 2026-06-17: 2 pods `ebs-csi-controller-*`). It is a multi-container pod (csi-attacher,
		// csi-provisioner, csi-resizer, csi-snapshotter, liveness-probe + the primary ebs-plugin);
		// the primary business container is "ebs-plugin". The ebs-csi-node DaemonSet stays name-only
		// in k8scluster (substrateDaemonSets) — no construct needs pod correlation for it.
		return []Workload{
			{Name: "ebs-csi-controller", Namespace: "kube-system", Replicas: 2, Controller: "", Container: "ebs-plugin"},
		}
	case "cert_manager":
		return []Workload{
			{Name: "cert-manager", Namespace: "cert-manager", Replicas: 2, Controller: "", Container: "cert-manager-controller"}, // real scrape container (svc-cert-manager.md), not the deploy name
			{Name: "cert-manager-webhook", Namespace: "cert-manager", Replicas: 1, Controller: "", Container: "cert-manager-webhook"},
			{Name: "cert-manager-cainjector", Namespace: "cert-manager", Replicas: 1, Controller: "", Container: "cert-manager-cainjector"},
		}
	case "core_dns":
		return []Workload{
			{Name: "coredns", Namespace: "kube-system", Replicas: 2, Controller: "", Container: "coredns"},
		}
	case "external_dns":
		return []Workload{
			{Name: "external-dns", Namespace: "external-dns", Replicas: 1, Controller: "", Container: "external-dns"},
		}
	case "load_balancer_controller":
		return []Workload{
			{Name: "aws-load-balancer-controller", Namespace: "kube-system", Replicas: 2, Controller: "", Container: "aws-load-balancer-controller"},
		}
	case "cluster_autoscaler":
		return []Workload{
			{Name: "cluster-autoscaler", Namespace: "kube-system", Replicas: 1, Controller: "", Container: "cluster-autoscaler"},
		}
	case "karpenter":
		return []Workload{
			// karpenter deploy name is "karpenter"; the main container is "controller" (M8).
			{Name: "karpenter", Namespace: "kube-system", Replicas: 2, Controller: "", Container: "controller"},
		}
	case "argocd":
		// 7 components per live reference recon (svc-group-b.md):
		// - argocd-application-controller: StatefulSet (ordinal pod naming)
		// - argocd-server, argocd-repo-server: 2 replicas each
		// - argocd-applicationset-controller, argocd-notifications-controller,
		//   argocd-dex-server, argocd-redis: 1 replica each
		// argocd-redis has a redis_exporter sidecar; primary container is "redis".
		return []Workload{
			{Name: "argocd-application-controller", Namespace: "argocd", Replicas: 1, Controller: "statefulset", Container: "application-controller"},
			{Name: "argocd-server", Namespace: "argocd", Replicas: 2, Controller: "", Container: "server"},
			{Name: "argocd-repo-server", Namespace: "argocd", Replicas: 2, Controller: "", Container: "repo-server"},
			{Name: "argocd-applicationset-controller", Namespace: "argocd", Replicas: 1, Controller: "", Container: "applicationset-controller"},
			{Name: "argocd-notifications-controller", Namespace: "argocd", Replicas: 1, Controller: "", Container: "notifications-controller"},
			{Name: "argocd-dex-server", Namespace: "argocd", Replicas: 1, Controller: "", Container: "dex-server"},
			// redis primary container is "redis"; the redis_exporter sidecar is targeted
			// separately via k8saddon.StampPodsContainer when emitting redis_exporter metrics.
			{Name: "argocd-redis", Namespace: "argocd", Replicas: 1, Controller: "", Container: "redis"},
		}
	case "envoy_gateway":
		// Two surfaces (svc-group-b.md):
		// - envoy-gateway: control plane deploy (1 pod)
		// - envoy-<gw-ns>-<gw-name>-<hash>: data-plane proxy deploy (2 pods).
		//   First cut: one static proxy deployment named "envoy-default-eg-<hash>".
		//   Dynamic proxy deployments (per-gateway) are a Phase 2.H elaboration.
		return []Workload{
			{Name: "envoy-gateway", Namespace: "envoy-gateway-system", Replicas: 1, Controller: "", Container: "envoy-gateway"},
			{Name: "envoy-default-eg-proxy", Namespace: "envoy-gateway-system", Replicas: 2, Controller: "", Container: "envoy"},
		}
	case "metrics_server":
		// metrics-server is also in BaselineWorkloads() (always-present baseline).
		// AddonWorkloads returns the same shape so blueprint authors may declare it
		// explicitly in addons: [...] if they want to be self-documenting. The resolver
		// deduplicates (namespace,name) across SubstrateWorkloads, so no double-emit.
		return []Workload{
			{Name: "metrics-server", Namespace: "kube-system", Replicas: 2, Controller: "", Container: "metrics-server"},
		}
	default:
		return nil
	}
}

// BaselineWorkloads returns the always-present kube-system baseline workloads that every
// k8s-enabled blueprint implicitly carries, regardless of the addons: list. These are
// non-construct substrate pods whose pod-shape (kube_pod_*/cadvisor) must always be
// present: coredns (DNS) and metrics-server (HPA/kubectl top). Both are returned as fresh
// Workload templates (PodNames/NodeIdx nil, minted at resolve time).
func BaselineWorkloads() []Workload {
	return []Workload{
		{Name: "coredns", Namespace: "kube-system", Replicas: 2, Controller: "", Container: "coredns"},
		{Name: "metrics-server", Namespace: "kube-system", Replicas: 2, Controller: "", Container: "metrics-server"},
	}
}

// MonitoringBaselineWorkloads returns the always-present Alloy collector pods a k8s-monitoring
// deploy carries in the "monitoring" namespace. Gated by the caller on k8s_monitoring being
// enabled (resolver Pass-2c) — they must NOT leak onto monitoring-disabled clusters. Shapes are
// live reference captures (k8s-monitoring 4.x, 2026-06-17): alloy-metrics is a StatefulSet
// (pods `…-alloy-metrics-0/1/2`, replica count = metricsReplicas, never node-scaled); alloy-logs is
// a DaemonSet (one pod per node, `…-alloy-logs-<5hex>`). Both run the primary container "alloy".
// metricsReplicas ≤ 0 falls back to 1 (matches K8sRoster's MetricsReplicas default).
func MonitoringBaselineWorkloads(metricsReplicas int) []Workload {
	if metricsReplicas <= 0 {
		metricsReplicas = 1
	}
	return []Workload{
		{Name: "alloy-metrics", Namespace: "monitoring", Replicas: metricsReplicas, Controller: "statefulset", Container: "alloy"},
		{Name: "alloy-logs", Namespace: "monitoring", Replicas: 0, Controller: "daemonset", Container: "alloy"},
	}
}
