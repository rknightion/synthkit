// SPDX-License-Identifier: AGPL-3.0-only

// Package certmanager implements the "cert_manager" construct.
//
// Kind:     "cert_manager"
// Scope:    core.ScopeSubstrate (cluster disambiguates; no blueprint label)
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   Config{JobMode}
//
// Build requires fx.Cluster (non-nil).
//
// Signal contract (signals/k8s-addons.md [slug: k8s-cert-manager]):
//
//	Families: certmanager_*
//	Job:      "cert-manager" (autodiscovery, default) | "integrations/cert-manager" (integration mode)
//	Labels:   cluster + k8s_cluster_name on every series; NO blueprint label
//
// Synthetic certs (namespace="cert-manager", exported_namespace=cert's real ns):
//
//	"demo-tls"    (exported_namespace=default, ClusterIssuer letsencrypt-prod)
//	"api-tls"     (exported_namespace=api, ClusterIssuer letsencrypt-prod)
//	"internal-tls" (exported_namespace=internal, Issuer internal-ca)
//
// Expiry timestamps are deterministic from fx.Seed + cert name (stable across ticks).
//
// condition ∈ {True, False, Unknown} — ALL three series emitted per cert/issuer;
// exactly one =1 (True).
//
// Per-pod correlation (ARCHITECTURE Phase 1 Task 1.A):
//
//	When fx.Cluster.SubstrateWorkloads contains the cert_manager addon workloads,
//	certmanager_* families are stamped with per-pod join labels via k8saddon helpers:
//
//	 - certmanager_certificate_*/issuer_*/clusterissuer_*/http_acme_*/controller_sync_*
//	   → StampLeader("cert-manager") — only the leader-elected pod emits these
//	 - certmanager_clock_time_seconds* → StampPods("cert-manager") — all replicas emit
//	 - controller_runtime_* → StampPods("cert-manager-webhook") + StampPods("cert-manager-cainjector")
//	 - workqueue_* + rest_client_requests_total → StampPods("cert-manager-cainjector")
//
//	If SubstrateWorkloads is absent (nil workload lookup → nil stamp), the fallback
//	emits the original cluster-scoped series (no pod/namespace/container/instance labels).
//	This preserves back-compat for tests and blueprints that don't declare cert_manager.
//
// Container-name note: the real Prometheus scrape target for the cert-manager controller
// pod uses container="cert-manager-controller" (not "cert-manager" which is the deploy
// name). StampPodsContainer is used to override the container label correctly.
//
// ARCHITECTURE invariants honoured:
//   - I3:  counters via state.Add (cumulative); gauges via state.Set
//   - I13: no empty/sentinel labels — absent dims are omitted
//   - I21: ScopeSubstrate — no blueprint label
package certmanager

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
	kind     = "cert_manager"
	interval = 60 * time.Second
)

// Metrics ports (from recon svc-cert-manager.md §1, §8).
const (
	portController = 9402 // controller, cainjector, webhook all expose metrics on 9402
	portWebhook    = 9402
	portCainjector = 9402
)

// Config controls optional cert-manager emission behaviour.
// All identity comes from fx.Cluster.
type Config struct {
	// JobMode selects the job label value.
	//   "" or "autodiscovery" → "cert-manager"  (annotation-autodiscovery path, default)
	//   "integration"         → "integrations/cert-manager"  (chart feature-integrations path)
	JobMode string `yaml:"job_mode"`
}

// certEntry is one synthetic managed certificate.
type certEntry struct {
	name       string
	namespace  string
	issuer     string
	issuerKind string
}

// syntheticCerts is the per-cluster certificate inventory.
var syntheticCerts = []certEntry{
	{"demo-tls", "default", "letsencrypt-prod", "ClusterIssuer"},
	{"api-tls", "api", "letsencrypt-prod", "ClusterIssuer"},
	{"internal-tls", "internal", "internal-ca", "Issuer"},
}

// certManagerControllers are the cert-manager sync controllers (live recon §2.6).
// Full list from a live reference cluster capture 2026-06-16.
var certManagerControllers = []string{
	"certificaterequests-approver",
	"certificaterequests-issuer-acme",
	"certificaterequests-issuer-ca",
	"certificaterequests-issuer-selfsigned",
	"certificaterequests-issuer-vault",
	"certificaterequests-issuer-venafi",
	"certificates-issuing",
	"certificates-key-manager",
	"certificates-metrics",
	"certificates-readiness",
	"certificates-request-manager",
	"certificates-revision-manager",
	"certificates-trigger",
	"certificatesigningrequests-issuer-acme",
	"certificatesigningrequests-issuer-ca",
	"certificatesigningrequests-issuer-selfsigned",
	"certificatesigningrequests-issuer-vault",
	"certificatesigningrequests-issuer-venafi",
	"clusterissuers",
	"gateway-shim",
	"ingress-shim",
	"issuers",
	"orders",
}

// certManagerConditions are the possible condition label values.
var certManagerConditions = []string{"True", "False", "Unknown"}

// cainjectorControllers are the controller names for the cainjector component (recon §3.2).
var cainjectorControllers = []string{
	"apiservice",
	"customresourcedefinition",
	"mutatingwebhookconfiguration",
	"validatingwebhookconfiguration",
}

// cainjectorReconcileResults are the result label values for controller_runtime_reconcile_total.
var cainjectorReconcileResults = []string{"error", "requeue", "requeue_after", "success"}

// workqueueNames are the queue names for workqueue_* metrics on cainjector (recon §4.2).
var workqueueNames = []string{
	"apiservice",
	"customresourcedefinition",
	"mutatingwebhookconfiguration",
	"validatingwebhookconfiguration",
}

// summaryQuantiles are the quantile label values for the ACME client duration summary
// (recon §2.9 — type: SUMMARY, not histogram; quantile 0.5/0.9/0.99).
var summaryQuantiles = []string{"0.5", "0.9", "0.99"}

// Construct renders cert-manager metrics for one cluster.
type Construct struct {
	clust *fixture.Cluster
	seed  string
	st    *state.State
	job   string
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// New builds a Construct from cfg and the resolved fixtures.
// Returns an error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("cert_manager: fixture.Cluster is required (nil)")
	}
	job := "cert-manager"
	if c, ok := cfg.(*Config); ok && c != nil && c.JobMode == "integration" {
		job = "integrations/cert-manager"
	}
	return &Construct{
		clust: fx.Cluster,
		seed:  fx.Seed,
		st:    state.NewState(),
		job:   job,
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics + logs.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one cert-manager snapshot for the cluster.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	tickSec := interval.Seconds()
	scale := tickSec / 30.0

	base := map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"job":              c.job,
	}

	// ── Per-pod stamp helpers ─────────────────────────────────────────────────
	//
	// StampPodsContainer overrides the container label to the real scrape container
	// name "cert-manager-controller" — the deploy is named "cert-manager" but the
	// actual container is "cert-manager-controller" (recon §2.2, §6, §8).
	//
	// leaderMaps: ONE label-set (leader pod) for families the leader-elected controller
	// emits exclusively. nil when SubstrateWorkloads absent → fallback path.
	//
	// allControllerMaps: label-set per replica for clock_time (all pods emit it).
	//
	// webhookMaps / cainjectorMaps: per-pod label-sets for the respective components.
	leaderMaps := k8saddon.StampPodsContainer(c.clust, "cert-manager", "cert-manager-controller", base, portController)
	if len(leaderMaps) > 1 {
		leaderMaps = leaderMaps[:1] // keep leader only
	}
	allControllerMaps := k8saddon.StampPodsContainer(c.clust, "cert-manager", "cert-manager-controller", base, portController)

	// Build cainjector and webhook bases with their own job labels (recon §8).
	baseWebhook := cloneMap(base)
	baseWebhook["job"] = "webhook"
	baseCainjector := cloneMap(base)
	baseCainjector["job"] = "cainjector"

	webhookMaps := k8saddon.StampPods(c.clust, "cert-manager-webhook", baseWebhook, portWebhook)
	cainjectorMaps := k8saddon.StampPods(c.clust, "cert-manager-cainjector", baseCainjector, portCainjector)

	// mergeLabels merges extra labels on top of a stamped pod label-map clone,
	// returning the merged result. Never mutates the input.
	mergeLabels := func(podMap map[string]string, extra map[string]string) map[string]string {
		out := make(map[string]string, len(podMap)+len(extra))
		for k, v := range podMap {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	// withLeader emits a metric for each leader pod label-set (or base if no pods).
	// fn receives the fully-merged label map to pass to st.Set/st.Add.
	withLeader := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(leaderMaps) > 0 {
			for _, pm := range leaderMaps {
				emit(mergeLabels(pm, extra))
			}
		} else {
			// Fallback: cluster-scoped (no pod labels).
			lbls := make(map[string]string, len(base)+len(extra))
			for k, v := range base {
				lbls[k] = v
			}
			for k, v := range extra {
				lbls[k] = v
			}
			emit(lbls)
		}
	}

	// withAllControllers emits for all controller replica pods (or base if no pods).
	withAllControllers := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(allControllerMaps) > 0 {
			for _, pm := range allControllerMaps {
				emit(mergeLabels(pm, extra))
			}
		} else {
			lbls := make(map[string]string, len(base)+len(extra))
			for k, v := range base {
				lbls[k] = v
			}
			for k, v := range extra {
				lbls[k] = v
			}
			emit(lbls)
		}
	}

	// ── Per-certificate metrics ───────────────────────────────────────────────
	for _, cert := range syntheticCerts {
		expirationTS := certExpiryTS(c.seed, cert.name, now)
		renewalTS := expirationTS - 30*86400
		notBeforeTS := expirationTS - 90*86400

		certExtra := map[string]string{
			"name":               cert.name,
			"namespace":          "cert-manager",
			"exported_namespace": cert.namespace,
			"issuer_name":        cert.issuer,
			"issuer_kind":        cert.issuerKind,
			"issuer_group":       "cert-manager.io",
		}

		// certmanager_certificate_ready_status — leader pod, one series per condition.
		for _, cond := range certManagerConditions {
			val := 0.0
			if cond == "True" {
				val = 1.0
			}
			condExtra := make(map[string]string, len(certExtra)+1)
			for k, v := range certExtra {
				condExtra[k] = v
			}
			condExtra["condition"] = cond
			withLeader(condExtra, func(lbls map[string]string) {
				c.st.Set("certmanager_certificate_ready_status", lbls, val)
			})
		}

		withLeader(certExtra, func(lbls map[string]string) {
			c.st.Set("certmanager_certificate_expiration_timestamp_seconds", lbls, expirationTS)
			c.st.Set("certmanager_certificate_renewal_timestamp_seconds", lbls, renewalTS)
			c.st.Set("certmanager_certificate_not_after_timestamp_seconds", lbls, expirationTS)
			c.st.Set("certmanager_certificate_not_before_timestamp_seconds", lbls, notBeforeTS)
		})
	}

	// ── Issuer / ClusterIssuer readiness ─────────────────────────────────────
	for _, cond := range certManagerConditions {
		val := 0.0
		if cond == "True" {
			val = 1.0
		}
		withLeader(map[string]string{
			"name": "internal-ca", "namespace": "cert-manager", "condition": cond,
		}, func(lbls map[string]string) {
			c.st.Set("certmanager_issuer_ready_status", lbls, val)
		})
		withLeader(map[string]string{
			"name": "letsencrypt-prod", "condition": cond,
		}, func(lbls map[string]string) {
			c.st.Set("certmanager_clusterissuer_ready_status", lbls, val)
		})
	}

	// ── ACME HTTP client metrics ──────────────────────────────────────────────
	// From recon §2.8: actions are get_registration / register_account / update_registration.
	// Methods observed: GET, POST, HEAD. Status: 200 (no errors on stable cluster).
	for _, action := range []string{"get_registration", "register_account", "update_registration"} {
		method := "POST"
		if action == "get_registration" {
			method = "GET"
		}
		acmeExtra := map[string]string{
			"scheme": "https", "host": "acme-v02.api.letsencrypt.org",
			"action": action, "method": method, "status": "200",
		}
		withLeader(acmeExtra, func(lbls map[string]string) {
			c.st.Add("certmanager_http_acme_client_request_count", lbls, scale)
			c.st.Add("certmanager_http_acme_client_request_duration_seconds_count", lbls, scale)
			c.st.Add("certmanager_http_acme_client_request_duration_seconds_sum", lbls, 0.1*scale)
		})

		// ACME duration summary — emit quantile series (recon §2.9: type SUMMARY,
		// quantile label values 0.5 / 0.9 / 0.99; no _bucket series).
		for _, q := range summaryQuantiles {
			quantileVal := 0.1 // representative latency in seconds
			switch q {
			case "0.5":
				quantileVal = 0.08
			case "0.9":
				quantileVal = 0.18
			case "0.99":
				quantileVal = 0.45
			}
			qExtra := make(map[string]string, len(acmeExtra)+1)
			for k, v := range acmeExtra {
				qExtra[k] = v
			}
			qExtra["quantile"] = q
			withLeader(qExtra, func(lbls map[string]string) {
				c.st.Set("certmanager_http_acme_client_request_duration_seconds", lbls, quantileVal)
			})
		}
	}

	// ── Controller sync metrics ───────────────────────────────────────────────
	for _, ctrl := range certManagerControllers {
		ctrlExtra := map[string]string{"controller": ctrl}
		withLeader(ctrlExtra, func(lbls map[string]string) {
			c.st.Add("certmanager_controller_sync_call_count", lbls, scale)
			c.st.Add("certmanager_controller_sync_error_count", lbls, 0)
		})
	}

	// ── Clock time ────────────────────────────────────────────────────────────
	// Live reference cluster emits BOTH certmanager_clock_time_seconds and certmanager_clock_time_seconds_gauge.
	// Both replicas emit this metric (recon §2.5, §10.1) — use allControllerMaps.
	withAllControllers(nil, func(lbls map[string]string) {
		c.st.Set("certmanager_clock_time_seconds", lbls, float64(now.Unix()))
		c.st.Set("certmanager_clock_time_seconds_gauge", lbls, float64(now.Unix()))
	})

	// ── controller_runtime_* (webhook + cainjector) ───────────────────────────
	// Only emitted when SubstrateWorkloads provides the respective pods.
	// Webhook: controller_runtime_reconcile_total + webhook-specific metrics.
	if len(webhookMaps) > 0 {
		c.emitWebhookMetrics(webhookMaps, scale)
	}
	// Cainjector: controller_runtime_reconcile_total + workqueue_* + rest_client_*.
	if len(cainjectorMaps) > 0 {
		c.emitCainjectorMetrics(cainjectorMaps, scale)
	}

	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return err
	}

	// ── klog-format log streams (one stream per cert-manager component) ───────
	if w.Logs != nil {
		streams := c.buildLogStreams(cluster, now)
		if len(streams) > 0 {
			if err := w.Logs.Write(ctx, streams); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitWebhookMetrics emits controller_runtime_* for the cert-manager-webhook component.
// Webhook job = "webhook", port 9402, container = "cert-manager-webhook".
func (c *Construct) emitWebhookMetrics(podMaps []map[string]string, scale float64) {
	// controller_runtime_reconcile_total — webhook reconcile loop.
	// Webhook controllers are CRD admission controllers; result values match cainjector.
	for _, pm := range podMaps {
		for _, result := range cainjectorReconcileResults {
			lbls := cloneMap(pm)
			lbls["controller"] = "cert-manager-webhook"
			lbls["result"] = result
			c.st.Add("controller_runtime_reconcile_total", lbls, scale)
		}
		// controller_runtime_active_workers + max_concurrent_reconciles
		lbls := cloneMap(pm)
		lbls["controller"] = "cert-manager-webhook"
		c.st.Set("controller_runtime_active_workers", lbls, 1)
		c.st.Set("controller_runtime_max_concurrent_reconciles", lbls, 1)

		// webhook-specific metrics
		c.st.Add("controller_runtime_webhook_requests_total", cloneMap(pm), scale)
		c.st.Set("controller_runtime_webhook_requests_in_flight", cloneMap(pm), 0)
		c.st.Add("controller_runtime_webhook_panics_total", cloneMap(pm), 0)
	}
}

// emitCainjectorMetrics emits controller_runtime_* + workqueue_* + rest_client_requests_total
// for the cert-manager-cainjector component (job="cainjector", port 9402).
func (c *Construct) emitCainjectorMetrics(podMaps []map[string]string, scale float64) {
	for _, pm := range podMaps {
		// controller_runtime_reconcile_total — one per (controller × result).
		for _, ctrl := range cainjectorControllers {
			for _, result := range cainjectorReconcileResults {
				lbls := cloneMap(pm)
				lbls["controller"] = ctrl
				lbls["result"] = result
				c.st.Add("controller_runtime_reconcile_total", lbls, scale)
			}
			// active_workers + max_concurrent_reconciles
			wlbls := cloneMap(pm)
			wlbls["controller"] = ctrl
			c.st.Set("controller_runtime_active_workers", wlbls, 1)
			c.st.Set("controller_runtime_max_concurrent_reconciles", wlbls, 1)
		}

		// workqueue_* — one per queue name (recon §4).
		for _, qname := range workqueueNames {
			qlbls := cloneMap(pm)
			qlbls["name"] = qname
			qlbls["controller"] = qname
			c.st.Add("workqueue_adds_total", qlbls, scale)
			c.st.Set("workqueue_depth", qlbls, 0)
			c.st.Set("workqueue_longest_running_processor_seconds", qlbls, 0)
			c.st.Add("workqueue_retries_total", qlbls, 0)
			c.st.Set("workqueue_unfinished_work_seconds", qlbls, 0)
		}

		// rest_client_requests_total (recon §5).
		for _, meth := range []string{"GET", "PUT", "POST"} {
			rlbls := cloneMap(pm)
			rlbls["code"] = "200"
			rlbls["method"] = meth
			rlbls["host"] = "172.20.0.1:443"
			c.st.Add("rest_client_requests_total", rlbls, scale)
		}
	}
}

// ── Log lane ──────────────────────────────────────────────────────────────────

// certComponent describes one cert-manager component for log emission.
type certComponent struct {
	workloadName  string // SubstrateWorkloads lookup key (e.g. "cert-manager")
	containerName string // k8s_container_name in stream labels
	deployName    string // k8s_deployment_name / service_name
}

// certManagerComponents lists the three cert-manager components that emit logs.
// Live recon (svc-cert-manager.md §7): only cainjector had recent entries, but controller
// and webhook are present pods and do emit on activity — we emit for all three to match
// the full stream-label topology Alloy would ingest if log volume were higher.
var certManagerComponents = []certComponent{
	{workloadName: "cert-manager", containerName: "cert-manager-controller", deployName: "cert-manager"},
	{workloadName: "cert-manager-webhook", containerName: "cert-manager-webhook", deployName: "cert-manager-webhook"},
	{workloadName: "cert-manager-cainjector", containerName: "cert-manager-cainjector", deployName: "cert-manager-cainjector"},
}

// klogLines are the synthetic klog log bodies emitted per component per tick.
// Indexes are modulated by tick index derived from the timestamp so lines vary
// deterministically. Format: <Level><MMDD> <HH:MM:SS.µs>  <goroutine> <file>:<line>] "msg" key=val
//
// High-card data (object names, reconcileIDs, cert names) goes IN THE BODY only.
// Stream labels must stay low-cardinality.
var klogTemplates = []struct {
	level string // "I", "W", "E"
	body  string // format string: args are (now.Format("0102"), now.Format("15:04:05.000000"))
}{
	{"I", `%s %s       1 controller.go:215] "Syncing certificate" logger="cert-manager" controller="certificates-readiness"`},
	{"I", `%s %s       1 controller.go:167] "Certificate renewal scheduled" logger="cert-manager" controller="certificates-trigger"`},
	{"I", `%s %s       1 reconciler.go:141] "Updated object" logger="cert-manager" kind="mutatingwebhookconfiguration" name="cert-manager-webhook"`},
	{"I", `%s %s       1 acme.go:82] "ACME order finalised" logger="cert-manager" controller="certificaterequests-issuer-acme"`},
	{"W", `%s %s       1 controller.go:308] "Certificate approaching renewal window" logger="cert-manager" controller="certificates-readiness"`},
	{"E", `%s %s       1 indexers.go:61] "unable to fetch certificate that owns the secret" err="Certificate.cert-manager.io not found" logger="cert-manager" kind="apiservice"`},
}

// buildLogStreams builds klog-format Loki streams for all three cert-manager components.
// One stream per component per tick, with pod name from SubstrateWorkloads when available.
// High-card values (cert names, object names) are body-only — never in stream labels.
func (c *Construct) buildLogStreams(cluster string, now time.Time) []loki.Stream {
	// Derive a tick index from the timestamp for deterministic line variation.
	tickIdx := int(now.Unix() / int64(interval.Seconds()))

	var streams []loki.Stream
	for _, comp := range certManagerComponents {
		// Look up the pod name from SubstrateWorkloads — required for k8s_pod_name label.
		wl, ok := k8saddon.LookupSubstrateWorkload(c.clust, comp.workloadName)
		if !ok || len(wl.PodNames) == 0 {
			// No workload found — skip this component.
			continue
		}
		// Use the first pod name (stable across ticks for this component).
		podName := wl.PodNames[0]

		// Rotate through templates deterministically (vary by tick + component index).
		tmplIdx := (tickIdx + len(streams)) % len(klogTemplates)
		tmpl := klogTemplates[tmplIdx]

		datePart := now.Format("0102")
		timePart := now.Format("15:04:05.000000")
		body := fmt.Sprintf(tmpl.level+tmpl.body, datePart, timePart)

		// Map klog level to detected_level (Alloy convention):
		// I → "info", W → "warn", E → "error"; klog "I" lines Alloy marks as "unknown"
		// in practice but our synthetic label matches the body's intent.
		detectedLevel := "info"
		switch tmpl.level {
		case "W":
			detectedLevel = "warn"
		case "E":
			detectedLevel = "error"
		}

		streams = append(streams, loki.Stream{
			Labels: map[string]string{
				"cluster":             cluster,
				"k8s_cluster_name":    cluster,
				"k8s_namespace_name":  "cert-manager",
				"k8s_container_name":  comp.containerName,
				"k8s_deployment_name": comp.deployName,
				"k8s_pod_name":        podName,
				"service_name":        comp.deployName,
				"service_namespace":   "cert-manager",
				"detected_level":      detectedLevel,
				"log_iostream":        "stderr",
			},
			Lines: []loki.Line{{T: now, Body: body}},
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

// certExpiryTS derives a deterministic expiry Unix timestamp for a cert. The offset
// is in the range [60, 90) days in the future, stable across ticks (derived from seed).
func certExpiryTS(seed, certName string, now time.Time) float64 {
	// Use first 8 bytes of the hex hash as a uint64 to derive a stable day offset.
	h := fixture.Sum(seed, "cert_expiry", certName)
	// Convert 4 hex pairs (8 chars) → uint32
	var raw uint32
	for i := 0; i < 4; i++ {
		var b byte
		hi := hexNibble(h[i*2])
		lo := hexNibble(h[i*2+1])
		b = hi<<4 | lo
		raw = raw<<8 | uint32(b)
	}
	// Day offset in [60, 90)
	dayOffset := int64(60) + int64(raw%30)
	return float64(now.Unix() + dayOffset*86400)
}

// hexNibble converts a hex ASCII character to its 4-bit value.
func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return 0
	}
}
