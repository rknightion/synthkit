// SPDX-License-Identifier: AGPL-3.0-only

// Package argocd implements the "argocd" construct.
//
// Kind:     "argocd"
// Scope:    core.ScopeSubstrate (cluster disambiguates; no blueprint label)
// Signals:  []core.SignalClass{core.Metrics, core.Logs}
// Interval: 60s
// Config:   Config{} (empty — all identity from fx.Cluster)
//
// Build requires fx.Cluster (non-nil).
//
// Signal contract (signals/k8s-addons.md [slug: k8s-argocd]):
//
//	49 metric names from a live reference cluster capture 2026-06-16 (svc-group-b.md §2).
//	Families: argocd_* + workqueue_*
//	Scrape endpoints (4 distinct jobs):
//	  job=argocd-metrics          → app-controller (port 8082) + applicationset (port 8080)
//	  job=argocd-server-metrics   → server (port 8083)
//	  job=argocd-repo-server-metrics → repo-server (port 8084)
//	Labels:   cluster + k8s_cluster_name on every series; NO blueprint label
//
// Per-component pod stamping (when SubstrateWorkloads populated):
//
//	application-controller (StatefulSet) → StampPods("argocd-application-controller", port 8082)
//	server                                → StampPods("argocd-server", port 8083)
//	repo-server                           → StampPods("argocd-repo-server", port 8084)
//	applicationset-controller             → StampPods("argocd-applicationset-controller", port 8080)
//	redis (redis_exporter sidecar)        → StampPodsContainer("argocd-redis","redis_exporter", port 9121)
//	notifications-controller and dex-server: no metrics scraped on the reference cluster → not stamped
//
// OMITTED (NOT scraped on the reference cluster):
//
//	controller_runtime_* — argocd does not emit it (confirmed absent in Mimir)
//	argocd_notifications_* — notifications-controller metrics not scraped
//
// Two redis histogram families (version artifact):
//
//	argocd_redis_request_duration_{bucket,count,sum}         — app-controller (has hostname label)
//	argocd_redis_request_duration_seconds_{bucket,count,sum} — repo-server (no hostname label)
//
// Logs (svc-group-b.md §2.C):
//
//	All 5 components write to stderr. Stream labels use OTel/Alloy convention:
//	  cluster / k8s_cluster_name / k8s_namespace_name="argocd" / k8s_container_name /
//	  k8s_pod_name / k8s_statefulset_name (app-controller) or k8s_deployment_name (others) /
//	  service_name / service_namespace="argocd" / detected_level / log_iostream="stderr".
//	Formats: logfmt for app-controller/server/repo-server/applicationset-controller;
//	         JSON for notifications-controller.
//	Pod names: derived from SubstrateWorkloads when populated; falls back to synthetic names.
//
// FALLBACK: nil SubstrateWorkloads → cluster-scoped series (no pod/namespace/container/instance).
//
// ARCHITECTURE invariants honoured:
//   - I3:  counters via state.Add (cumulative); gauges via state.Set
//   - I13: no empty/sentinel labels — absent dims are omitted
//   - I15: no high-card keys in Loki stream labels (app names, sync IDs in body only)
//   - I21: ScopeSubstrate — no blueprint label
package argocd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/k8saddon"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "argocd"
	interval = 60 * time.Second
)

// Metrics ports per svc-group-b.md §2.A scrape-endpoints table.
const (
	portAppController = 8082
	portServer        = 8083
	portRepoServer    = 8084
	portAppSet        = 8080
	portRedisExporter = 9121
)

// Config is the construct config struct (empty — all identity from fixtures).
type Config struct{}

// Construct renders Argo CD metrics for one cluster.
type Construct struct {
	clust *fixture.Cluster
	st    *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// New builds a Construct from cfg and the resolved fixtures.
// Returns an error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("argocd: fixture.Cluster is required (nil)")
	}
	return &Construct{
		clust: fx.Cluster,
		st:    state.NewState(),
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics + logs.
func (c *Construct) Signals() []core.SignalClass {
	return []core.SignalClass{core.Metrics, core.Logs}
}

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one Argo CD metric snapshot for the cluster.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	scale := interval.Seconds() / 30.0

	// base builds a fresh cluster-identity label map for a given job.
	// Each call returns an independent map — never share across series.
	base := func(job string) map[string]string {
		return map[string]string{
			"cluster":          cluster,
			"k8s_cluster_name": cluster,
			"job":              job,
		}
	}

	// ── per-component pod stamp maps ────────────────────────────────────────────
	// StampPods returns nil when the workload is absent → fallback emits base-only series.
	appCtrlMaps := k8saddon.StampPods(c.clust, "argocd-application-controller",
		base("argocd-metrics"), portAppController)
	serverMaps := k8saddon.StampPods(c.clust, "argocd-server",
		base("argocd-server-metrics"), portServer)
	repoMaps := k8saddon.StampPods(c.clust, "argocd-repo-server",
		base("argocd-repo-server-metrics"), portRepoServer)
	appSetMaps := k8saddon.StampPods(c.clust, "argocd-applicationset-controller",
		base("argocd-metrics"), portAppSet)
	// redis metrics come from the redis_exporter sidecar, whose real container name in
	// kube_pod_container_info is "metrics" (svc-group-b.md §2.B) — stamp that so the
	// series join the pod's container series.
	redisMaps := k8saddon.StampPodsContainer(c.clust, "argocd-redis", "metrics",
		base("argocd-metrics"), portRedisExporter)

	// mergeInto clones podMap and merges extra labels onto it.
	// Never mutates podMap or extra.
	mergeInto := func(podMap map[string]string, extra map[string]string) map[string]string {
		out := make(map[string]string, len(podMap)+len(extra))
		for k, v := range podMap {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	// forEach iterates over pods (or falls back to single base if nil).
	// fn receives a fresh merged label map per pod.
	forEach := func(maps []map[string]string, fallbackJob string, extra map[string]string, fn func(map[string]string)) {
		if len(maps) > 0 {
			for _, pm := range maps {
				fn(mergeInto(pm, extra))
			}
		} else {
			fn(mergeInto(base(fallbackJob), extra))
		}
	}

	// ── argocd_app_info (gauge, app-controller) ─────────────────────────────────
	// 25 apps — matches live reference cluster series count (live reference recon: argocd_app_info=25).
	// Mostly Synced/Healthy; a few OutOfSync/Progressing/Degraded for realism.
	// All in project "platform" per recon (live reference recon: project=platform on all).
	type appEntry struct {
		name, project, namespace, repo, healthStatus, syncStatus string
		autoSync                                                 bool
	}
	apps := []appEntry{
		// Core platform addons (Healthy/Synced)
		{"otel-demo", "platform", "otel-demo", "https://github.com/grafana/opentelemetry-demo", "Healthy", "Synced", true},
		{"cert-manager", "platform", "cert-manager", "https://charts.jetstack.io", "Healthy", "Synced", true},
		{"cnpg-operator", "platform", "cnpg-system", "https://cloudnative-pg.github.io/charts", "Healthy", "Synced", true},
		{"envoy-gateway", "platform", "envoy-gateway-system", "https://charts.envoyproxy.io", "Healthy", "Synced", true},
		{"envoy-gateway-crds", "platform", "envoy-gateway-system", "https://charts.envoyproxy.io", "Healthy", "Synced", true},
		{"karpenter-cr", "platform", "kube-system", "https://github.com/aws/karpenter", "Healthy", "Synced", true},
		{"external-dns", "platform", "external-dns", "https://github.com/kubernetes-sigs/external-dns", "Healthy", "Synced", true},
		{"aws-load-balancer-controller", "platform", "kube-system", "https://aws.github.io/eks-charts", "Healthy", "Synced", true},
		{"grafana-alloy", "platform", "monitoring", "https://grafana.github.io/helm-charts", "Healthy", "Synced", true},
		{"grafana-operator", "platform", "monitoring", "https://github.com/grafana/grafana-operator", "Healthy", "Synced", true},
		{"loki-stack", "platform", "monitoring", "https://grafana.github.io/helm-charts", "Healthy", "Synced", true},
		{"mimir-distributed", "platform", "monitoring", "https://grafana.github.io/helm-charts", "Healthy", "Synced", true},
		{"tempo-distributed", "platform", "monitoring", "https://grafana.github.io/helm-charts", "Healthy", "Synced", true},
		{"pyroscope", "platform", "monitoring", "https://grafana.github.io/helm-charts", "Healthy", "Synced", true},
		{"tailscale-operator", "platform", "tailscale", "https://pkgs.tailscale.com/helmcharts", "Healthy", "Synced", true},
		{"cluster-autoscaler", "platform", "kube-system", "https://kubernetes.github.io/autoscaler", "Healthy", "Synced", true},
		{"metrics-server", "platform", "kube-system", "https://kubernetes-sigs.github.io/metrics-server", "Healthy", "Synced", true},
		{"reloader", "platform", "kube-system", "https://stakater.github.io/stakater-charts", "Healthy", "Synced", true},
		{"secrets-store-csi-driver", "platform", "kube-system", "https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts", "Healthy", "Synced", true},
		{"vault-agent-injector", "platform", "vault", "https://helm.releases.hashicorp.com", "Healthy", "Synced", true},
		{"argo-rollouts", "platform", "argo-rollouts", "https://argoproj.github.io/argo-helm", "Healthy", "Synced", true},
		// Root / self-managed
		{"root", "platform", "argocd", "https://github.com/example-org/example-infra", "Healthy", "Synced", true},
		// A few apps with non-ideal states (realism — transient OutOfSync/Progressing/Degraded)
		{"synthkit", "platform", "synthkit", "https://github.com/example-org/example-infra", "Healthy", "OutOfSync", false},
		{"db-operator", "platform", "databases", "https://github.com/example-org/example-infra", "Progressing", "Synced", true},
		{"observability-stack", "platform", "monitoring", "https://github.com/example-org/example-infra", "Degraded", "OutOfSync", false},
	}
	for _, app := range apps {
		autosync := "false"
		if app.autoSync {
			autosync = "true"
		}
		extra := map[string]string{
			"name":             app.name,
			"project":          app.project,
			"dest_namespace":   app.namespace,
			"dest_server":      "https://kubernetes.default.svc",
			"repo":             app.repo,
			"health_status":    app.healthStatus,
			"sync_status":      app.syncStatus,
			"autosync_enabled": autosync,
		}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			c.st.Set("argocd_app_info", lbls, 1)
		})
	}

	// ── argocd_app_k8s_request_total (counter, app-controller) ───────────────────
	for _, verb := range []string{"list", "watch", "get"} {
		extra := map[string]string{"server": "https://kubernetes.default.svc", "verb": verb}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			c.st.Add("argocd_app_k8s_request_total", lbls, scale)
		})
	}

	// ── argocd_app_orphaned_resources_count (gauge, app-controller) ──────────────
	for _, app := range apps {
		extra := map[string]string{"project": app.project, "name": app.name}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			c.st.Set("argocd_app_orphaned_resources_count", lbls, 0)
		})
	}

	// ── argocd_app_reconcile (histogram, app-controller) ─────────────────────────
	// le: 0.25,0.5,1.0,2.0,4.0,8.0,16.0,+Inf; labels: dest_server only
	appReconcileBuckets := []float64{0.25, 0.5, 1.0, 2.0, 4.0, 8.0, 16.0}
	extra := map[string]string{"dest_server": "https://kubernetes.default.svc"}
	forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
		for range 5 {
			c.st.Observe("argocd_app_reconcile", lbls, appReconcileBuckets, state.LEBare, 0.5+w.Shape.Noise(0.3))
		}
	})

	// ── argocd_app_sync_duration_seconds_total (counter, app-controller) ─────────
	forEach(appCtrlMaps, "argocd-metrics", nil, func(lbls map[string]string) {
		c.st.Add("argocd_app_sync_duration_seconds_total", lbls, 2.0*scale)
	})

	// ── argocd_app_sync_total (counter, app-controller) ──────────────────────────
	for _, app := range apps {
		for _, phase := range []string{"Succeeded", "Failed"} {
			delta := scale * 0.9
			if phase == "Failed" {
				delta = scale * 0.05
			}
			extra := map[string]string{
				"name":    app.name,
				"project": app.project,
				"phase":   phase,
				"dry_run": "false",
			}
			forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
				c.st.Add("argocd_app_sync_total", lbls, delta)
			})
		}
	}

	// ── argocd_cluster_api_resource_objects (gauge, app-controller) ──────────────
	forEach(appCtrlMaps, "argocd-metrics", map[string]string{"server": "https://kubernetes.default.svc"}, func(lbls map[string]string) {
		c.st.Set("argocd_cluster_api_resource_objects", lbls, 850)
	})

	// ── argocd_cluster_api_resources (gauge, app-controller) ─────────────────────
	forEach(appCtrlMaps, "argocd-metrics", map[string]string{"server": "https://kubernetes.default.svc"}, func(lbls map[string]string) {
		c.st.Set("argocd_cluster_api_resources", lbls, 62)
	})

	// ── argocd_cluster_cache_age_seconds (gauge, app-controller) ─────────────────
	forEach(appCtrlMaps, "argocd-metrics", map[string]string{"server": "https://kubernetes.default.svc"}, func(lbls map[string]string) {
		c.st.Set("argocd_cluster_cache_age_seconds", lbls, float64(now.Unix()%3600))
	})

	// ── argocd_cluster_connection_status (gauge, app-controller) ─────────────────
	forEach(appCtrlMaps, "argocd-metrics", map[string]string{"server": "https://kubernetes.default.svc", "k8s_version": "v1.35.5"}, func(lbls map[string]string) {
		c.st.Set("argocd_cluster_connection_status", lbls, 1)
	})

	// ── argocd_cluster_events_total (counter, app-controller) ────────────────────
	forEach(appCtrlMaps, "argocd-metrics", map[string]string{"server": "https://kubernetes.default.svc"}, func(lbls map[string]string) {
		c.st.Add("argocd_cluster_events_total", lbls, scale)
	})

	// ── argocd_cluster_info (gauge, app-controller) ───────────────────────────────
	forEach(appCtrlMaps, "argocd-metrics", map[string]string{
		"k8s_version": "v1.35.5",
		"name":        "in-cluster",
		"server":      "https://kubernetes.default.svc",
	}, func(lbls map[string]string) {
		c.st.Set("argocd_cluster_info", lbls, 1)
	})

	// ── argocd_info (gauge, server) ───────────────────────────────────────────────
	forEach(serverMaps, "argocd-server-metrics", map[string]string{"version": "v3.4.3"}, func(lbls map[string]string) {
		c.st.Set("argocd_info", lbls, 1)
	})

	// ── argocd_kubectl_exec_pending (gauge, app-controller) ──────────────────────
	forEach(appCtrlMaps, "argocd-metrics", nil, func(lbls map[string]string) {
		c.st.Set("argocd_kubectl_exec_pending", lbls, 0)
	})

	// ── argocd_kubectl_exec_total (counter, app-controller) ──────────────────────
	for _, cmd := range []string{"apply", "auth", "create", "replace"} {
		extra := map[string]string{
			"command":  cmd,
			"hostname": "argocd-application-controller-0",
		}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			c.st.Add("argocd_kubectl_exec_total", lbls, scale*0.3)
		})
	}

	// ── argocd_kubectl_rate_limiter_duration_seconds (histogram, app-controller) ──
	kubectlDurationBuckets := []float64{0.005, 0.1, 0.5, 2.0, 8.0, 30.0}
	forEach(appCtrlMaps, "argocd-metrics", nil, func(lbls map[string]string) {
		for range 3 {
			c.st.Observe("argocd_kubectl_rate_limiter_duration_seconds", lbls, kubectlDurationBuckets, state.LEBare, 0.01+w.Shape.Noise(0.02))
		}
	})

	// ── argocd_kubectl_request_duration_seconds (histogram, app-controller) ───────
	for _, verb := range []string{"Create", "Delete", "Get", "List", "Patch", "Update"} {
		extra := map[string]string{
			"host": "172.20.0.1:443",
			"verb": verb,
		}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			for range 3 {
				c.st.Observe("argocd_kubectl_request_duration_seconds", lbls, kubectlDurationBuckets, state.LEBare, 0.05+w.Shape.Noise(0.1))
			}
		})
	}

	// ── argocd_kubectl_request_retries_total (counter, app-controller) ───────────
	forEach(appCtrlMaps, "argocd-metrics", map[string]string{"host": "172.20.0.1:443"}, func(lbls map[string]string) {
		c.st.Add("argocd_kubectl_request_retries_total", lbls, 0)
	})

	// ── argocd_kubectl_request_size_bytes (histogram, app-controller) ────────────
	reqSizeBuckets := []float64{0, 100, 1000, 10000, 100000, 1e6}
	for _, verb := range []string{"Create", "Get", "List", "Patch", "Update"} {
		extra := map[string]string{"host": "172.20.0.1:443", "verb": verb}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			for range 3 {
				c.st.Observe("argocd_kubectl_request_size_bytes", lbls, reqSizeBuckets, state.LEBare, float64(100+w.Shape.IntN(5000)))
			}
		})
	}

	// ── argocd_kubectl_requests_total (counter, app-controller) ──────────────────
	for _, code := range []string{"200", "201", "404", "429"} {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			delta := scale
			if code != "200" && code != "201" {
				delta = scale * 0.01
			}
			extra := map[string]string{
				"code":   code,
				"host":   "172.20.0.1:443",
				"method": method,
			}
			forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
				c.st.Add("argocd_kubectl_requests_total", lbls, delta)
			})
		}
	}

	// ── argocd_kubectl_response_size_bytes (histogram, app-controller) ───────────
	respSizeBuckets := []float64{0, 100, 1000, 10000, 100000, 1e6}
	for _, verb := range []string{"Get", "List"} {
		extra := map[string]string{"host": "172.20.0.1:443", "verb": verb}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			for range 3 {
				c.st.Observe("argocd_kubectl_response_size_bytes", lbls, respSizeBuckets, state.LEBare, float64(500+w.Shape.IntN(50000)))
			}
		})
	}

	// ── argocd_kubectl_transport_cache_entries (gauge, app-controller) ───────────
	forEach(appCtrlMaps, "argocd-metrics", nil, func(lbls map[string]string) {
		c.st.Set("argocd_kubectl_transport_cache_entries", lbls, 2)
	})

	// ── argocd_kubectl_transport_create_calls_total (counter, app-controller) ─────
	for _, result := range []string{"hit", "miss"} {
		extra := map[string]string{"result": result}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			c.st.Add("argocd_kubectl_transport_create_calls_total", lbls, scale*0.5)
		})
	}

	// ── argocd_redis_request_duration (histogram, app-controller — WITH hostname) ─
	// Version artifact: app-controller emits _duration_ (no _seconds_), with hostname label.
	redisDurationBuckets := []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.0}
	redisHostnameExtra := map[string]string{
		"hostname":  "argocd-application-controller-0",
		"initiator": "argocd-application-controller",
	}
	forEach(appCtrlMaps, "argocd-metrics", redisHostnameExtra, func(lbls map[string]string) {
		for range 5 {
			c.st.Observe("argocd_redis_request_duration", lbls, redisDurationBuckets, state.LEBare, 0.02+w.Shape.Noise(0.05))
		}
	})

	// ── argocd_redis_request_duration_seconds (histogram, repo-server — NO hostname) ─
	// Version artifact: repo-server emits _duration_seconds_, no hostname label.
	redisDurationSecondsBuckets := []float64{0.1, 0.25, 0.5, 1.0, 2.0}
	forEach(repoMaps, "argocd-repo-server-metrics", map[string]string{
		"initiator": "argocd-repo-server",
	}, func(lbls map[string]string) {
		for range 5 {
			c.st.Observe("argocd_redis_request_duration_seconds", lbls, redisDurationSecondsBuckets, state.LEBare, 0.03+w.Shape.Noise(0.05))
		}
	})

	// ── argocd_redis_request_total (counter, 3 emitters) ─────────────────────────
	// 3 initiators: app-controller (with hostname), repo-server, server.
	// app-controller initiator
	forEach(appCtrlMaps, "argocd-metrics", map[string]string{
		"initiator": "argocd-application-controller",
		"failed":    "false",
		"hostname":  "argocd-application-controller-0",
	}, func(lbls map[string]string) {
		c.st.Add("argocd_redis_request_total", lbls, scale*2)
	})
	// repo-server initiator
	forEach(repoMaps, "argocd-repo-server-metrics", map[string]string{
		"initiator": "argocd-repo-server",
		"failed":    "false",
	}, func(lbls map[string]string) {
		c.st.Add("argocd_redis_request_total", lbls, scale*2)
	})
	// server initiator
	forEach(serverMaps, "argocd-server-metrics", map[string]string{
		"initiator": "argocd-server",
		"failed":    "false",
	}, func(lbls map[string]string) {
		c.st.Add("argocd_redis_request_total", lbls, scale)
	})

	// ── argocd_repo_pending_request_total (gauge, repo-server) ───────────────────
	forEach(repoMaps, "argocd-repo-server-metrics", nil, func(lbls map[string]string) {
		c.st.Set("argocd_repo_pending_request_total", lbls, 0)
	})

	// ── argocd_resource_events_processed_in_batch (counter, app-controller) ───────
	forEach(appCtrlMaps, "argocd-metrics", nil, func(lbls map[string]string) {
		c.st.Add("argocd_resource_events_processed_in_batch", lbls, scale*5)
	})

	// ── argocd_resource_events_processing (histogram, app-controller) ────────────
	resourceEventBuckets := []float64{0.01, 0.05, 0.1, 0.5, 1.0, 5.0}
	forEach(appCtrlMaps, "argocd-metrics", nil, func(lbls map[string]string) {
		for range 5 {
			c.st.Observe("argocd_resource_events_processing", lbls, resourceEventBuckets, state.LEBare, 0.05+w.Shape.Noise(0.1))
		}
	})

	// ── argocd_git_request_total (counter, repo-server) ──────────────────────────
	for _, reqType := range []string{"fetch", "ls-remote"} {
		extra := map[string]string{
			"repo":         "https://github.com/grafana/opentelemetry-demo",
			"request_type": reqType,
		}
		forEach(repoMaps, "argocd-repo-server-metrics", extra, func(lbls map[string]string) {
			c.st.Add("argocd_git_request_total", lbls, scale*0.5)
		})
	}

	// ── argocd_git_request_duration_seconds (histogram, repo-server) ─────────────
	gitDurationBuckets := []float64{0.1, 0.25, 0.5, 1.0, 2.0, 4.0, 10.0, 20.0}
	for _, reqType := range []string{"fetch", "ls-remote"} {
		extra := map[string]string{
			"repo":         "https://github.com/grafana/opentelemetry-demo",
			"request_type": reqType,
		}
		forEach(repoMaps, "argocd-repo-server-metrics", extra, func(lbls map[string]string) {
			for range 3 {
				c.st.Observe("argocd_git_request_duration_seconds", lbls, gitDurationBuckets, state.LEBare, 0.3+w.Shape.Noise(0.5))
			}
		})
	}

	// ── workqueue_* (app-controller + applicationset-controller) ──────────────────
	// app-controller queues (recon §2.A).
	// NOTE: asserts_env is Asserts read-side injection — NOT an emitted label (removed).
	appCtrlQueues := []string{
		"app_hydration_queue",
		"app_operation_processing_queue",
		"app_reconciliation_queue",
		"manifest_hydration_queue",
		"project_reconciliation_queue",
	}
	for _, qname := range appCtrlQueues {
		extra := map[string]string{
			"controller": qname,
			"name":       qname,
		}
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			c.st.Add("workqueue_adds_total", lbls, scale)
			c.st.Set("workqueue_depth", lbls, 0)
			c.st.Set("workqueue_longest_running_processor_seconds", lbls, 0)
			c.st.Add("workqueue_retries_total", lbls, 0)
			c.st.Set("workqueue_unfinished_work_seconds", lbls, 0)
		})
		forEach(appCtrlMaps, "argocd-metrics", extra, func(lbls map[string]string) {
			c.st.Observe("workqueue_queue_duration_seconds", lbls, []float64{0.001, 0.01, 0.1, 1.0}, state.LEBare, 0.005+w.Shape.Noise(0.005))
			c.st.Observe("workqueue_work_duration_seconds", lbls, []float64{0.001, 0.01, 0.1, 1.0}, state.LEBare, 0.01+w.Shape.Noise(0.01))
		})
	}

	// applicationset-controller queue
	// NOTE: asserts_env is Asserts read-side injection — NOT an emitted label (removed).
	appSetExtra := map[string]string{
		"controller": "applicationset",
		"name":       "applicationset",
	}
	forEach(appSetMaps, "argocd-metrics", appSetExtra, func(lbls map[string]string) {
		c.st.Add("workqueue_adds_total", lbls, scale*0.2)
		c.st.Set("workqueue_depth", lbls, 0)
		c.st.Set("workqueue_longest_running_processor_seconds", lbls, 0)
		c.st.Add("workqueue_retries_total", lbls, 0)
		c.st.Set("workqueue_unfinished_work_seconds", lbls, 0)
		c.st.Observe("workqueue_queue_duration_seconds", lbls, []float64{0.001, 0.01, 0.1, 1.0}, state.LEBare, 0.005+w.Shape.Noise(0.005))
		c.st.Observe("workqueue_work_duration_seconds", lbls, []float64{0.001, 0.01, 0.1, 1.0}, state.LEBare, 0.01+w.Shape.Noise(0.01))
	})

	// redis_exporter metrics (via sidecar container=redis_exporter)
	// These are emitted under the redis_exporter's own namespace via redisMaps.
	// Only the pod-stamp is relevant here — no argocd_ metrics from redis itself.
	// Emit redis_connected_clients (representative redis_exporter metric).
	forEach(redisMaps, "argocd-metrics", nil, func(lbls map[string]string) {
		c.st.Set("redis_connected_clients", lbls, 5)
		c.st.Set("redis_uptime_in_seconds", lbls, float64(now.Unix()%86400))
	})

	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return err
	}

	// ── Loki log streams (svc-group-b.md §2.C) ───────────────────────────────
	// Guard: w.Logs is nil when the runner has not wired the log sink (e.g. in
	// metric-only tests that pass nil for the log capture).
	if w.Logs != nil {
		streams := c.buildLogStreams(now, cluster)
		if len(streams) > 0 {
			return w.Logs.Write(ctx, streams)
		}
	}
	return nil
}

// buildLogStreams constructs the per-component Loki streams for one tick.
// All 5 argocd components write to stderr every tick (dense per-reconcile volume
// per svc-group-b.md §2.C). Stream labels use OTel/Alloy convention; high-card
// identifiers (app names, sync IDs) ride in body only (I15).
func (c *Construct) buildLogStreams(now time.Time, cluster string) []loki.Stream {
	ts := now.UTC().Format(time.RFC3339)
	// tickIdx rotates 0–59 each minute so log body fields vary across ticks
	// (mirrors karpenter.go per-tick shape rotation).
	tickIdx := now.Minute()

	// podName looks up the first pod name for a workload in SubstrateWorkloads.
	// Falls back to the canonical synthetic name for the component.
	podName := func(workloadName, fallback string) string {
		if c.clust.SubstrateWorkloads == nil {
			return fallback
		}
		for _, wl := range c.clust.SubstrateWorkloads {
			if wl.Name == workloadName && len(wl.PodNames) > 0 {
				return wl.PodNames[0]
			}
		}
		return fallback
	}

	// baseLabels returns the common OTel/Alloy stream labels for all components.
	// Never aliased — each call returns a fresh map.
	baseLabels := func() map[string]string {
		return map[string]string{
			"cluster":            cluster,
			"k8s_cluster_name":   cluster,
			"k8s_namespace_name": "argocd",
			"service_namespace":  "argocd",
			"log_iostream":       "stderr",
		}
	}

	// logfmtLine returns a logfmt-formatted log line with the given msg and optional
	// additional key=value pairs. Format: time="..." level=info msg="..." [extra k=v ...]
	logfmtLine := func(level, msg string, extra ...string) string {
		s := fmt.Sprintf("time=%q level=%s msg=%q", ts, level, msg)
		for i := 0; i+1 < len(extra); i += 2 {
			s += fmt.Sprintf(" %s=%s", extra[i], extra[i+1])
		}
		return s
	}

	// jsonLine returns a JSON-formatted log line.
	jsonLine := func(level, msg string, extra ...string) string {
		fields := fmt.Sprintf(`"level":%q,"msg":%q,"time":%q`, level, msg, ts)
		for i := 0; i+1 < len(extra); i += 2 {
			fields += fmt.Sprintf(`,%q:%q`, extra[i], extra[i+1])
		}
		return "{" + fields + "}"
	}

	detectedLevel := func(level string) string {
		if level == "warning" {
			return "warn"
		}
		return level
	}

	var streams []loki.Stream

	// ── application-controller (StatefulSet, logfmt) ──────────────────────────
	// Real pod name ends with -0 (StatefulSet ordinal). Uses k8s_statefulset_name
	// per svc-group-b.md §2.C cross-cutting facts.
	{
		appCtrlPod := podName("argocd-application-controller", "argocd-application-controller-0")
		lbls := baseLabels()
		lbls["k8s_container_name"] = "application-controller"
		lbls["k8s_pod_name"] = appCtrlPod
		lbls["k8s_statefulset_name"] = "argocd-application-controller"
		lbls["service_name"] = "argocd-application-controller"
		lbls["service_instance_id"] = fmt.Sprintf("argocd.%s.application-controller", appCtrlPod)
		lbls["detected_level"] = detectedLevel("info")
		// Rotate the reconciled app name and timing per tick so bodies aren't frozen.
		appNames := []string{"otel-demo", "external-dns", "cert-manager", "karpenter", "grafana-agent"}
		reconcileTimes := []string{"3", "5", "8", "2", "11", "4", "7", "6", "9", "12"}
		appName := appNames[tickIdx%len(appNames)]
		timeMs := reconcileTimes[tickIdx%len(reconcileTimes)]
		streams = append(streams, loki.Stream{
			Labels: lbls,
			Lines: []loki.Line{
				{T: now, Body: logfmtLine("info", "Reconciliation completed",
					"app-namespace", "argocd",
					"comparison-level", "0",
					"dest-namespace", "kube-system",
					"dest-server", `"https://kubernetes.default.svc"`,
					"patch_ms", "0",
					"project", "platform",
					"time_ms", timeMs,
				)},
				{T: now, Body: logfmtLine("info", "Skipping auto-sync: application status is Synced",
					"app-namespace", "argocd",
					"application", appName,
					"project", "platform",
				)},
			},
		})
	}

	// ── server (Deployment, logfmt) ───────────────────────────────────────────
	{
		serverPod := podName("argocd-server", "argocd-server-0")
		lbls := baseLabels()
		lbls["k8s_container_name"] = "server"
		lbls["k8s_pod_name"] = serverPod
		lbls["k8s_deployment_name"] = "argocd-server"
		lbls["service_name"] = "argocd-server"
		lbls["service_instance_id"] = fmt.Sprintf("argocd.%s.server", serverPod)
		lbls["detected_level"] = detectedLevel("info")
		// Rotate gRPC method per tick so bodies vary.
		grpcMethods := []string{"VersionString", "List", "Get", "Watch", "Update", "ListApps"}
		grpcServices := []string{"version.VersionService", "application.ApplicationService", "cluster.ClusterService"}
		grpcTimings := []string{"0", "1", "2", "3", "0", "1"}
		grpcMethod := grpcMethods[tickIdx%len(grpcMethods)]
		grpcService := grpcServices[tickIdx%len(grpcServices)]
		grpcTime := grpcTimings[tickIdx%len(grpcTimings)]
		streams = append(streams, loki.Stream{
			Labels: lbls,
			Lines: []loki.Line{
				{T: now, Body: logfmtLine("info", "received unary call",
					"grpc.method", grpcMethod,
					"grpc.service", grpcService,
					"grpc.code", "OK",
					"grpc.time_ms", grpcTime,
				)},
			},
		})
	}

	// ── repo-server (Deployment, logfmt) ──────────────────────────────────────
	{
		repoPod := podName("argocd-repo-server", "argocd-repo-server-0")
		lbls := baseLabels()
		lbls["k8s_container_name"] = "repo-server"
		lbls["k8s_pod_name"] = repoPod
		lbls["k8s_deployment_name"] = "argocd-repo-server"
		lbls["service_name"] = "argocd-repo-server"
		lbls["service_instance_id"] = fmt.Sprintf("argocd.%s.repo-server", repoPod)
		lbls["detected_level"] = detectedLevel("info")
		// Rotate repo-server rpc timing and app name per tick.
		repoMethods := []string{"GenerateManifest", "GetAppDetails", "GetRevisionMetadata", "ListApps", "GetHelmCharts"}
		repoTimings := []string{"120", "85", "210", "63", "145", "97", "178", "52", "230", "110"}
		repoMethod := repoMethods[tickIdx%len(repoMethods)]
		repoTime := repoTimings[tickIdx%len(repoTimings)]
		streams = append(streams, loki.Stream{
			Labels: lbls,
			Lines: []loki.Line{
				{T: now, Body: logfmtLine("info", "git fetch",
					"grpc.method", repoMethod,
					"grpc.code", "OK",
					"grpc.time_ms", repoTime,
					"peer.address", "127.0.0.1:56789",
					"protocol", "grpc",
				)},
			},
		})
	}

	// ── applicationset-controller (Deployment, logfmt) ────────────────────────
	// Real cadence: ~10 min heartbeat (not every-tick). Gate on tickIdx%10==0
	// so it fires roughly once per 10 minutes of simulated time.
	if tickIdx%10 == 0 {
		appSetPod := podName("argocd-applicationset-controller", "argocd-applicationset-controller-0")
		lbls := baseLabels()
		lbls["k8s_container_name"] = "applicationset-controller"
		lbls["k8s_pod_name"] = appSetPod
		lbls["k8s_deployment_name"] = "argocd-applicationset-controller"
		lbls["service_name"] = "argocd-applicationset-controller"
		lbls["service_instance_id"] = fmt.Sprintf("argocd.%s.applicationset-controller", appSetPod)
		lbls["detected_level"] = detectedLevel("info")
		// Rotate heap/goroutine values so heartbeat body isn't frozen.
		allocKBs := []string{"12345", "13210", "11870", "14500", "12980", "13750"}
		goroutines := []string{"160", "162", "158", "165", "157", "163"}
		alloc := allocKBs[tickIdx%len(allocKBs)]
		goroutine := goroutines[tickIdx%len(goroutines)]
		streams = append(streams, loki.Stream{
			Labels: lbls,
			Lines: []loki.Line{
				{T: now, Body: logfmtLine("info", fmt.Sprintf("Alloc=%s NumGC=42 Goroutines=%s", alloc, goroutine))},
			},
		})
	}

	// ── notifications-controller (Deployment, JSON) ───────────────────────────
	{
		notifPod := podName("argocd-notifications-controller", "argocd-notifications-controller-0")
		lbls := baseLabels()
		lbls["k8s_container_name"] = "notifications-controller"
		lbls["k8s_pod_name"] = notifPod
		lbls["k8s_deployment_name"] = "argocd-notifications-controller"
		lbls["service_name"] = "argocd-notifications-controller"
		lbls["service_instance_id"] = fmt.Sprintf("argocd.%s.notifications-controller", notifPod)
		lbls["detected_level"] = detectedLevel("info")
		// Rotate notification resource per tick.
		notifResources := []string{"argocd/external-dns", "argocd/otel-demo", "argocd/cert-manager", "argocd/karpenter"}
		notifResource := notifResources[tickIdx%len(notifResources)]
		streams = append(streams, loki.Stream{
			Labels: lbls,
			Lines: []loki.Line{
				{T: now, Body: jsonLine("info", "Start processing",
					"resource", notifResource,
				)},
			},
		})
	}

	return streams
}

// cloneMap returns a shallow copy of m.
func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
