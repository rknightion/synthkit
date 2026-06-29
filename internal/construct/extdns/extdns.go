// SPDX-License-Identifier: AGPL-3.0-only

// Package extdns implements the "external_dns" construct.
//
// Kind:     "external_dns"
// Scope:    core.ScopeSubstrate (no blueprint label — cluster disambiguates)
// Signals:  []{core.Metrics, core.Logs}
// Interval: 60 s
// Config:   empty struct (all identity comes from fixtures)
// Requires: fx.Cluster (error if nil)
//
// Emits (signals/k8s-addons.md [slug: k8s-externaldns] / svc-external-dns.md):
//
//	Metrics:
//	  external_dns_controller_*               controller status gauges + no-op counter
//	  external_dns_registry_*                 registry record gauges + error counter
//	  external_dns_source_*                   source record gauges + error counter + deduplicated endpoints
//	  external_dns_provider_cache_*           provider cache apply-changes counter
//	  external_dns_webhook_provider_*         webhook provider counters (all zero — cloudflare native)
//	  external_dns_build_info                 discovery/version gauge
//	  external_dns_http_request_duration_seconds (summary: quantile 0.5/0.9/0.99 + _count + _sum)
//
//	Logs: JSON-structured lines to Loki (OTel/Alloy stream labels:
//	      cluster / k8s_cluster_name / k8s_namespace_name / k8s_container_name /
//	      k8s_pod_name / k8s_deployment_name / service_name / service_namespace /
//	      detected_level / log_iostream)
//
// NOTE: ExternalDNS does NOT use controller-runtime — no controller_runtime_* or
// rest_client_* (signals/k8s-addons.md [slug: k8s-externaldns] closing note). This is contractual.
//
// Per-pod correlation (ARCHITECTURE Phase 1):
//
//	When fx.Cluster.SubstrateWorkloads contains the workload "external-dns" (1 pod,
//	namespace external-dns, container external-dns), ALL metric families are stamped
//	with pod/namespace/container/instance labels via k8saddon.StampPods.
//	Absent workload → fallback to cluster-scoped series (no pod labels).
//
// Every series and log stream carries cluster + k8s_cluster_name. No blueprint label.
package extdns

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

// ── Registration ──────────────────────────────────────────────────────────────

// Registration returns the core.ConstructReg for the "external_dns" kind.
// Call this from the composition root's catalog wiring file; no init() self-registration.
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "external_dns",
		Doc:       "ExternalDNS — external_dns_controller_* / registry_* / source_* / build_info + JSON logs",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build: func(cfg any, fx *fixture.Set) (core.Construct, error) {
			if fx.Cluster == nil {
				return nil, errors.New("external_dns: fx.Cluster is required")
			}
			return &construct{clust: fx.Cluster, st: state.NewState()}, nil
		},
	}
}

// ── Config ────────────────────────────────────────────────────────────────────

// Config is the YAML config struct for the external_dns construct.
// It is intentionally empty: all identity comes from the resolved cluster fixture.
// Unknown fields are rejected by strict yaml.v3 decoding at blueprint load.
type Config struct{}

// ── Construct ─────────────────────────────────────────────────────────────────

const (
	kind            = "external_dns"
	interval        = 60 * time.Second
	extDNSJob       = "external-dns"      // recon: job label = "external-dns" (autodiscovery, NOT integrations/)
	extDNSVersion   = "v20260406-v0.21.0" // recon: build_info version label (svc-external-dns.md §A.2)
	extDNSGoVersion = "go1.26.1"          // recon: go_version label (svc-external-dns.md §A.2)
	extDNSPort      = 7979                // recon: metrics port (svc-external-dns.md §B.3)
)

// registryRecordTypes are the record_type values for external_dns_registry_records
// (5 types observed: svc-external-dns.md §A.2). Lowercase per recon.
var registryRecordTypes = []string{"a", "aaaa", "cname", "mx", "txt"}

// sourceRecordTypes are the record_type values for external_dns_source_records
// (2 types observed: svc-external-dns.md §A.2). Lowercase per recon.
var sourceRecordTypes = []string{"a", "cname"}

// verifiedRecordTypes are the record_type values for external_dns_controller_verified_records
// (1 type observed in recon; a is the only one present). Lowercase per recon.
var verifiedRecordTypes = []string{"a"}

// httpPaths are the Kubernetes API path segments observed in the HTTP summary
// (svc-external-dns.md §A.2 — 7 paths from --source=service + --source=gateway-httproute).
var httpPaths = []string{
	"endpointslices",
	"gateways",
	"httproutes",
	"namespaces",
	"nodes",
	"pods",
	"services",
}

// summaryQuantiles are the quantile label values for external_dns_http_request_duration_seconds
// (svc-external-dns.md §A.2: Prometheus SUMMARY, quantile 0.5/0.9/0.99; no _bucket).
var summaryQuantiles = []string{"0.5", "0.9", "0.99"}

// webhookCounters are the webhook provider metric names (all zero on cloudflare native;
// present as metric families for structural completeness — svc-external-dns.md §A.2).
var webhookCounters = []string{
	"external_dns_webhook_provider_records_requests_total",
	"external_dns_webhook_provider_records_errors_total",
	"external_dns_webhook_provider_adjustendpoints_requests_total",
	"external_dns_webhook_provider_adjustendpoints_errors_total",
	"external_dns_webhook_provider_applychanges_requests_total",
	"external_dns_webhook_provider_applychanges_errors_total",
}

type construct struct {
	clust *fixture.Cluster
	st    *state.State
}

// Kind implements core.Construct.
func (c *construct) Kind() string { return kind }

// Signals implements core.Construct — metrics + logs.
func (c *construct) Signals() []core.SignalClass {
	return []core.SignalClass{core.Metrics, core.Logs}
}

// Interval implements core.Construct.
func (c *construct) Interval() time.Duration { return interval }

// Tick renders one metric + log batch for the cluster.
func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	factor := w.Shape.Factor(now, c.clust.Env.Weight, c.clust.Env.NonProd)
	tickSec := interval.Seconds()
	scale := tickSec / 30.0

	streams := c.emitForCluster(cluster, factor, scale, now, w)

	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return err
	}
	if len(streams) > 0 {
		if err := w.Logs.Write(ctx, streams); err != nil {
			return err
		}
	}
	return nil
}

// emitForCluster accumulates metric state and builds log streams for one cluster tick.
func (c *construct) emitForCluster(cluster string, factor, scale float64, now time.Time, w *core.World) []loki.Stream {
	// base builds the cluster-identity label map used before pod stamping.
	// A new map is allocated each call; never aliased.
	base := map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"job":              extDNSJob,
		"service":          "external-dns",
		"endpoint":         "http",
	}

	// ── Per-pod stamp ─────────────────────────────────────────────────────────
	//
	// StampPods looks up workload "external-dns" in cl.SubstrateWorkloads and
	// returns one label-set per pod with pod/namespace/container/instance stamped.
	// Returns nil when the workload is absent → fallback to cluster-scoped emission.
	podMaps := k8saddon.StampPods(c.clust, "external-dns", base, extDNSPort)

	// withPods emits metric(s) for each pod label-set, or falls back to base +
	// extra for cluster-scoped emission. The extra map is merged on top; neither
	// base nor podMaps elements are mutated.
	withPods := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(podMaps) > 0 {
			for _, pm := range podMaps {
				lbls := mergeLabels(pm, extra)
				emit(lbls)
			}
		} else {
			// Fallback: cluster-scoped, no pod/namespace/container/instance labels.
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

	// ── Controller status gauges ──────────────────────────────────────────────

	syncTS := float64(now.Unix()) - 30 - w.Shape.Float64()*30
	withPods(nil, func(lbls map[string]string) {
		c.st.Set("external_dns_controller_last_sync_timestamp_seconds", lbls, syncTS)
		c.st.Set("external_dns_controller_last_reconcile_timestamp_seconds", lbls, syncTS-5)
		c.st.Set("external_dns_controller_consecutive_soft_errors", lbls, 0)
	})

	// ── Controller no-op counter ──────────────────────────────────────────────

	withPods(nil, func(lbls map[string]string) {
		c.st.Add("external_dns_controller_no_op_runs_total", lbls, scale*(1+factor*2))
	})

	// ── Per-record-type gauges: verified (1 type: a) ──────────────────────────

	for _, rt := range verifiedRecordTypes {
		count := 8.0 + factor*4
		withPods(map[string]string{"record_type": rt}, func(lbls map[string]string) {
			c.st.Set("external_dns_controller_verified_records", lbls, count)
		})
	}

	// ── Per-record-type gauges: registry (5 types) ────────────────────────────

	registryCounts := map[string]float64{"a": 11, "aaaa": 2, "cname": 5, "mx": 1, "txt": 4}
	for _, rt := range registryRecordTypes {
		cnt := registryCounts[rt] * (0.8 + factor*0.4)
		withPods(map[string]string{"record_type": rt}, func(lbls map[string]string) {
			c.st.Set("external_dns_registry_records", lbls, cnt)
		})
	}

	// ── Per-record-type gauges: source (2 types) ──────────────────────────────

	sourceCounts := map[string]float64{"a": 8, "cname": 1}
	for _, rt := range sourceRecordTypes {
		cnt := sourceCounts[rt] * (0.8 + factor*0.4)
		withPods(map[string]string{"record_type": rt}, func(lbls map[string]string) {
			c.st.Set("external_dns_source_records", lbls, cnt)
		})
	}

	// ── Endpoint gauges (no record_type) ─────────────────────────────────────

	withPods(nil, func(lbls map[string]string) {
		c.st.Set("external_dns_registry_endpoints_total", lbls, 23*(0.8+factor*0.4))
		c.st.Set("external_dns_source_endpoints_total", lbls, 9*(0.8+factor*0.4))
	})

	// ── Deduplicated endpoints (record_type × source_type) ────────────────────

	for _, rt := range []string{"a", "cname"} {
		for _, srcType := range []string{"service", "ingress"} {
			cnt := 5.0 * (0.8 + factor*0.4)
			withPods(map[string]string{"record_type": rt, "source_type": srcType}, func(lbls map[string]string) {
				c.st.Set("external_dns_source_deduplicated_endpoints", lbls, cnt)
			})
		}
	}

	// ── Error counters ────────────────────────────────────────────────────────

	withPods(nil, func(lbls map[string]string) {
		c.st.Add("external_dns_registry_errors_total", lbls, 0)
		c.st.Add("external_dns_source_errors_total", lbls, 0)
	})

	// ── Provider cache counter ────────────────────────────────────────────────

	withPods(nil, func(lbls map[string]string) {
		c.st.Add("external_dns_provider_cache_apply_changes_calls", lbls, 0)
	})

	// ── Webhook provider counters (all zero — cloudflare native, not webhook) ─

	for _, name := range webhookCounters {
		withPods(nil, func(lbls map[string]string) {
			c.st.Add(name, lbls, 0)
		})
	}

	// ── build_info ────────────────────────────────────────────────────────────

	withPods(map[string]string{
		"version":    extDNSVersion,
		"goversion":  extDNSGoVersion,
		"go_version": extDNSGoVersion,
		"arch":       "arm64",
		"os":         "linux",
		"revision":   "unknown",
	}, func(lbls map[string]string) {
		c.st.Set("external_dns_build_info", lbls, 1)
	})

	// ── HTTP request duration (summary: quantile + _count + _sum) ────────────
	//
	// Recon (svc-external-dns.md §A.2):
	//   handler = "instrumented_http" (not /healthz or /metrics — those are liveness paths)
	//   host    = "172.20.0.1:443"   (kube-apiserver in-cluster address)
	//   scheme  = "https"
	//   method  = "GET"
	//   status  = "200"
	//   path    = one of 7 Kubernetes API paths (endpointslices / gateways / httproutes / ...)
	//   quantile = 0.5 / 0.9 / 0.99 (Prometheus SUMMARY; no _bucket series)

	// Representative latency values per path (p50/p90/p99 in seconds; from recon §A.2).
	type pathLatency struct{ p50, p90, p99 float64 }
	pathLatencies := map[string]pathLatency{
		"endpointslices": {0.001828, 0.003035, 0.005503},
		"gateways":       {0.001890, 0.002396, 0.007538},
		"httproutes":     {0.002036, 0.007490, 0.009105},
		"namespaces":     {0.001996, 0.002247, 0.002459},
		"nodes":          {0.002379, 0.003319, 0.007651},
		"pods":           {0.002001, 0.005394, 0.006267},
		"services":       {0.001991, 0.002833, 0.003049},
	}

	for _, path := range httpPaths {
		lat := pathLatencies[path]
		pathExtra := map[string]string{
			"handler": "instrumented_http",
			"host":    "172.20.0.1:443",
			"scheme":  "https",
			"method":  "GET",
			"status":  "200",
			"path":    path,
		}

		// Quantile series (summary body).
		for _, q := range summaryQuantiles {
			var qval float64
			switch q {
			case "0.5":
				qval = lat.p50
			case "0.9":
				qval = lat.p90
			case "0.99":
				qval = lat.p99
			}
			qExtra := make(map[string]string, len(pathExtra)+1)
			for k, v := range pathExtra {
				qExtra[k] = v
			}
			qExtra["quantile"] = q
			withPods(qExtra, func(lbls map[string]string) {
				c.st.Set("external_dns_http_request_duration_seconds", lbls, qval*(0.8+factor*0.4))
			})
		}

		// _count and _sum (same label set, no quantile key).
		withPods(pathExtra, func(lbls map[string]string) {
			c.st.Add("external_dns_http_request_duration_seconds_count", lbls, (2+factor*3)*scale)
			c.st.Add("external_dns_http_request_duration_seconds_sum", lbls, (lat.p90+factor*lat.p99)*scale)
		})
	}

	// ── JSON-structured logs (OTel/Alloy Loki stream labels) ─────────────────
	//
	// Recon (svc-external-dns.md §C): JSON format (--log-format=json), stderr only.
	// Stream labels use OTel/Alloy convention: k8s_namespace_name (NOT namespace),
	// k8s_pod_name, k8s_container_name, k8s_deployment_name, service_namespace, etc.
	// detected_level maps JSON "warning" → "warn".
	// ~70% of ticks emit a log line (2/min cadence: 1 warning + 1 info per sync).

	if w.Shape.Float64() < 0.7 {
		// Determine the pod name for the stream label — use first pod if available.
		podName := ""
		if len(podMaps) > 0 {
			podName = podMaps[0]["pod"]
		}

		// tickIdx rotates 0–59 each minute so log body fields vary across ticks
		// (mirrors svc-external-dns.md §C.3: real cluster emits same warning every sync
		// but message content references real DNS zones, not cluster names).
		tickIdx := now.Minute()

		// Use a realistic static DNS zone name (sourced from live reference recon, svc-external-dns.md §B.4)
		// rather than interpolating the cluster name.
		// Rotate over a set of plausible subdomain records to vary the body per tick.
		warnDomains := []string{
			"*.k8s.example.com",
			"api.k8s.example.com",
			"*.apps.k8s.example.com",
			"ingress.k8s.example.com",
		}
		infoMessages := []string{
			"All records are already up to date",
			"Endpoints were successfully applied",
			"All records are already up to date",
			"Registry updated with new records",
		}
		warnDomain := warnDomains[tickIdx%len(warnDomains)]
		infoMsg := infoMessages[tickIdx%len(infoMessages)]
		ts := now.UTC().Format(time.RFC3339)

		// Warning line (always accompanies an info line in the real cluster).
		warnBody := fmt.Sprintf(
			`{"level":"warning","msg":"Domain %s. contains conflicting record type candidates; discarding CNAME record","time":"%s"}`,
			warnDomain, ts,
		)
		infoBody := fmt.Sprintf(
			`{"level":"info","msg":"%s","time":"%s"}`,
			infoMsg, ts,
		)

		streamLabels := map[string]string{
			"cluster":                cluster,
			"k8s_cluster_name":       cluster,
			"k8s_namespace_name":     "external-dns",
			"k8s_container_name":     "external-dns",
			"k8s_deployment_name":    "external-dns",
			"service_name":           "external-dns",
			"service_namespace":      "external-dns",
			"app_kubernetes_io_name": "external-dns",
			"log_iostream":           "stderr",
		}
		if podName != "" {
			streamLabels["k8s_pod_name"] = podName
		}

		// Emit warning stream with detected_level=warn.
		warnLabels := cloneMap(streamLabels)
		warnLabels["detected_level"] = "warn"

		// Emit info stream with detected_level=info.
		infoLabels := cloneMap(streamLabels)
		infoLabels["detected_level"] = "info"

		return []loki.Stream{
			{
				Labels: warnLabels,
				Lines:  []loki.Line{{T: now, Body: warnBody}},
			},
			{
				Labels: infoLabels,
				Lines:  []loki.Line{{T: now, Body: infoBody}},
			},
		}
	}
	return nil
}

// mergeLabels merges extra labels on top of a pod label-map clone.
// Never mutates either input.
func mergeLabels(podMap map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(podMap)+len(extra))
	for k, v := range podMap {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// cloneMap returns a shallow copy of m.
func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
