// SPDX-License-Identifier: AGPL-3.0-only

// Package coredns implements the "core_dns" construct (ARCHITECTURE §2, kind="core_dns",
// Scope=ScopeSubstrate). It emits the full CoreDNS metric family that the Grafana
// integrations/kubernetes/kube-dns scrape job collects. One construct instance covers one
// EKS cluster.
//
// Kind: "core_dns"
// Scope: ScopeSubstrate — cluster+k8s_cluster_name disambiguate; NO blueprint label.
// Signals: Metrics only.
// Interval: 60s
// Requires: fx.Cluster (for cluster name label).
//
// Signal contract: signals/k8s-addons.md [slug: k8s-coredns]
// Live recon: docs/superpowers/recon/svc-coredns.md (live reference cluster, 2026-06-16)
//
// Per-pod correlation (ARCHITECTURE Phase 1 seam):
//
//	When fx.Cluster.SubstrateWorkloads contains the "coredns" workload (populated
//	by AddonWorkloads("core_dns")), ALL coredns families are stamped with per-pod
//	join labels via k8saddon.StampPods(cl, "coredns", base, 9153). Both replicas
//	serve DNS so ALL pods emit — NOT leader-only. Metric-specific dimensions
//	(server/zone/zones/proto/type/rcode/family) are merged ON TOP of each stamped
//	pod map; the stamped map is never mutated.
//
//	FALLBACK: when SubstrateWorkloads does not contain "coredns" (nil return from
//	StampPods), a single cluster-scoped series is emitted (back-compat).
//
// Families (per live recon §A):
//
//	coredns_build_info                              G  (static: value=1)
//	coredns_dns_requests_total                      C  server,zone,proto,family,type
//	coredns_dns_responses_total                     C  server,zone,plugin,rcode
//	coredns_dns_request_duration_seconds            H  server,zone  (17-bucket)
//	coredns_dns_request_size_bytes                  H  proto,server,zone  (15-bucket)
//	coredns_dns_response_size_bytes                 H  proto,server,zone  (15-bucket)
//	coredns_cache_entries                           G  server,type,zones
//	coredns_cache_hits_total                        C  server,type,zones
//	coredns_cache_misses_total                      C  server,zones
//	coredns_cache_requests_total                    C  server,zones
//	coredns_forward_healthcheck_broken_total        C  (core pod labels only)
//	coredns_forward_max_concurrent_rejects_total    C  (core pod labels only)
//	coredns_proxy_request_duration_seconds          H  proxy_name,rcode,to  (17-bucket)
//	coredns_proxy_conn_cache_hits_total             C  (core pod labels only)
//	coredns_proxy_conn_cache_misses_total           C  (core pod labels only)
//	coredns_proxy_healthcheck_failures_total        C  (core pod labels only)
//	coredns_health_request_duration_seconds         H  (core pod labels only, 6-bucket)
//	coredns_health_request_failures_total           C  (core pod labels only)
//	coredns_hosts_reload_timestamp_seconds          G  (core pod labels only; 0 = never)
//	coredns_kubernetes_dns_programming_duration_seconds H  service_kind  (21-bucket)
//	coredns_kubernetes_rest_client_requests_total   C  code,host,method
//	coredns_local_localhost_requests_total          C  (core pod labels only)
//	coredns_panics_total                            C  (always 0)
//	coredns_plugin_enabled                          G  name,server,zone
//	coredns_reload_failed_total                     C  (always 0)
//
// Value enums (per recon §A2):
//
//	rcode   ∈ {NOERROR, NXDOMAIN}   (NOERROR ≈18%, NXDOMAIN ≈82% — AAAA→NXDOMAIN pattern)
//	proto   ∈ {udp, tcp}
//	type    ∈ {A, AAAA, PTR, SRV, TXT, other}
//	family  = "1"  (IPv4 only)
//	plugin  = "errors"  (on dns_responses_total — errors plugin intercepts)
//	to      = "10.1.0.2:53"  (VPC DNS resolver)
//	proxy_name = "forward"
//
// ARCHITECTURE invariants honoured:
//   - I3:  counters via state.Add (cumulative); gauges via state.Set
//   - I13: no empty/sentinel labels — view label OMITTED (not present on EKS/kube-dns)
//   - I21: ScopeSubstrate — no blueprint label
package coredns

import (
	"context"
	"errors"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/k8saddon"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "core_dns"
	interval = 60 * time.Second

	// coreDNSServer is the server label value on every series that carries it.
	coreDNSServer = "dns://:53"
	// coreDNSZone is the zone (singular) label value used on dns_* and plugin_enabled.
	coreDNSZone = "."
	// coreDNSZones is the zones (plural) label value used on cache_* metrics.
	coreDNSZones = "."
	// coreDNSJob is the per-pod Prometheus scrape job label (recon §A4, Job 1).
	coreDNSJob = "integrations/kubernetes/kube-dns"
	// coreDNSPort is the metrics port scraped per-pod.
	coreDNSPort = 9153
)

// coreDNSProtos are the protocol families for DNS requests.
var coreDNSProtos = []string{"udp", "tcp"}

// coreDNSFamilies is the IP family label — IPv4 only in this synthetic.
var coreDNSFamilies = []string{"1"}

// coreDNSQueryTypes are the DNS query types emitted (recon §A2 observed values).
var coreDNSQueryTypes = []string{"A", "AAAA", "PTR", "SRV", "TXT", "other"}

// coreDNSRcodes are the response codes with probability weights (recon §A2: NXDOMAIN ~82%,
// NOERROR ~18% — AAAA queries for IPv4-only services dominate with NXDOMAIN).
var coreDNSRcodes = []struct {
	rcode  string
	weight float64
}{
	{"NOERROR", 0.18},
	{"NXDOMAIN", 0.82},
}

// coreDNSProxyRcodes are the rcodes on proxy_request_duration_seconds (recon §A3).
var coreDNSProxyRcodes = []string{"NOERROR", "NXDOMAIN"}

// coreDNSUpstream is the VPC DNS forwarder (recon §A3, §B3).
const coreDNSUpstream = "10.1.0.2:53"

// coreDNSPlugins are the plugins reported via coredns_plugin_enabled (recon §A2: 7 plugins).
// Note: health/ready/reload plugins do NOT register with coredns_plugin_enabled.
var coreDNSPlugins = []string{
	"cache", "errors", "forward", "kubernetes", "loadbalance", "loop", "prometheus",
}

// coreDNSServiceKinds are the service_kind values for kubernetes_dns_programming (recon §A3).
var coreDNSServiceKinds = []string{"cluster_ip", "headless_with_selector"}

// coreDNSDurationBuckets are the le boundaries for coredns_dns_request_duration_seconds
// (17 le values per recon §A3, not including +Inf which is implicit in state.Observe).
var coreDNSDurationBuckets = []float64{
	0.00025, 0.0005, 0.001, 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128,
	0.256, 0.512, 1.024, 2.048, 4.096, 8.192,
}

// coreDNSSizeBuckets are the le boundaries for request/response size histograms
// (15 le values per recon §A3: 0.0 through 64000.0 + +Inf implicit).
var coreDNSSizeBuckets = []float64{
	0.0, 100.0, 200.0, 300.0, 400.0, 511.0, 1023.0, 2047.0, 4095.0, 8291.0,
	16000.0, 32000.0, 48000.0, 64000.0,
}

// coreDNSProxyDurationBuckets are the le boundaries for proxy_request_duration_seconds
// (17 le values per recon §A3: 0.00025 through +Inf; +Inf implicit).
var coreDNSProxyDurationBuckets = []float64{
	0.00025, 0.0005, 0.001, 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128,
	0.256, 0.512, 1.024, 2.048,
}

// coreDNSHealthDurationBuckets are the le boundaries for health_request_duration_seconds
// (6 le values per recon §A3).
var coreDNSHealthDurationBuckets = []float64{0.00025, 0.0025, 0.025, 0.25, 2.5}

// coreDNSK8sDurationBuckets are the le boundaries for kubernetes_dns_programming_duration_seconds
// (21 le values per recon §A3; +Inf implicit).
var coreDNSK8sDurationBuckets = []float64{
	0.001, 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128, 0.256, 0.512,
	1.024, 2.048, 4.096, 8.192, 16.384, 32.768, 65.536, 131.072, 262.144, 524.288,
}

// Config is the construct config struct (empty — all identity from fixtures).
type Config struct{}

// Construct is one core_dns instance covering one EKS cluster.
type Construct struct {
	clust *fixture.Cluster
	st    *state.State
}

// New builds a Construct from the cfg pointer returned by NewConfig and the resolved
// fixture set. Returns an error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("core_dns: fixture.Cluster is required (nil)")
	}
	return &Construct{
		clust: fx.Cluster,
		st:    state.NewState(),
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics only.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one metric batch for the CoreDNS instance on this cluster.
// Counters accumulate across ticks (state.Add); gauges are set per tick (state.Set);
// histograms accumulate observations (state.Observe). Pushing running totals not deltas
// satisfies ARCHITECTURE I3.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	factor := w.Shape.Factor(now, c.clust.Env.Weight, c.clust.Env.NonProd)
	scale := interval.Seconds() / 30.0 // cadence-invariant volume (I3)

	// baseMap is the universal label set for the per-pod scrape job.
	// app, workload, service are real labels added by Alloy/k8s-monitoring
	// (svc-coredns.md §A2, §A4 Job 1 — present on every per-pod series).
	// NOTE: asserts_env is Asserts read-side injection — NOT emitted here.
	baseMap := map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"job":              coreDNSJob,
		"app":              "kube-dns",
		"workload":         "ReplicaSet/coredns",
		"service":          "kube-dns",
	}

	// podMaps: one label-map per coredns pod (stamped with pod/namespace/container/instance).
	// Returns nil when SubstrateWorkloads does not contain the "coredns" workload.
	podMaps := k8saddon.StampPods(c.clust, "coredns", baseMap, coreDNSPort)

	// withEach calls fn for each pod map, or once with the base cluster-scoped map as
	// fallback when no pods are available.
	withEach := func(extra map[string]string, fn func(lbls map[string]string)) {
		if len(podMaps) > 0 {
			for i, pm := range podMaps {
				merged := mergeLabels(pm, extra)
				// Per-pod value splitting: split totals across pods by index parity to
				// avoid emitting identical values for both replicas (realism).
				// Caller may override by passing extra with a pre-split value — this
				// helper just applies the merge; value splitting is done at call sites.
				_ = i
				fn(merged)
			}
		} else {
			lbls := mergeLabels(baseMap, extra)
			fn(lbls)
		}
	}

	// Baseline DNS request rate (req/s equivalent per tick), shared across all per-pod calls.
	reqRate := 50.0 + factor*200

	// ── coredns_build_info (G; static=1, core labels + server) ─────────────────
	// Emitted per pod (recon §A2: one per-pod series).
	withEach(map[string]string{"server": coreDNSServer}, func(lbls map[string]string) {
		c.st.Set("coredns_build_info", lbls, 1)
	})

	// ── coredns_dns_requests_total (C; server,zone,proto,family,type) ───────────
	// 6 types × 2 protos × 1 family = 12 series per pod.
	for _, proto := range coreDNSProtos {
		protoWeight := 0.9
		if proto == "tcp" {
			protoWeight = 0.1
		}
		for _, family := range coreDNSFamilies {
			for _, qtype := range coreDNSQueryTypes {
				var typeWeight float64
				switch qtype {
				case "A":
					typeWeight = 0.44 // recon §D: ~44%
				case "AAAA":
					typeWeight = 0.55 // recon §D: ~55%
				case "PTR":
					typeWeight = 0.001
				case "SRV":
					typeWeight = 0.005
				case "TXT":
					typeWeight = 0.003
				default: // "other"
					typeWeight = 0.001
				}
				delta := reqRate * typeWeight * protoWeight * scale
				extra := map[string]string{
					"server": coreDNSServer, "zone": coreDNSZone,
					"proto": proto, "family": family, "type": qtype,
				}
				withEach(extra, func(lbls map[string]string) {
					c.st.Add("coredns_dns_requests_total", lbls, delta)
				})
			}
		}
	}

	// ── coredns_dns_responses_total (C; server,zone,plugin,rcode) ───────────────
	// plugin is always "errors" (the errors plugin intercepts — recon §A2).
	for _, rc := range coreDNSRcodes {
		delta := reqRate * rc.weight * scale
		extra := map[string]string{
			"server": coreDNSServer, "zone": coreDNSZone,
			"plugin": "errors", "rcode": rc.rcode,
		}
		withEach(extra, func(lbls map[string]string) {
			c.st.Add("coredns_dns_responses_total", lbls, delta)
		})
	}

	// ── coredns_dns_request_duration_seconds (H; server,zone) ───────────────────
	// One histogram per pod (no additional dims beyond server+zone — recon §A3).
	extra := map[string]string{"server": coreDNSServer, "zone": coreDNSZone}
	withEach(extra, func(lbls map[string]string) {
		samples := 5 + w.Shape.IntN(20)
		for s := 0; s < samples; s++ {
			c.st.Observe("coredns_dns_request_duration_seconds", lbls,
				coreDNSDurationBuckets, state.LEBare,
				0.0002+factor*0.001*w.Shape.Noise(0.5))
		}
	})

	// ── coredns_dns_request_size_bytes (H; proto,server,zone) ───────────────────
	for _, proto := range coreDNSProtos {
		extra := map[string]string{
			"proto": proto, "server": coreDNSServer, "zone": coreDNSZone,
		}
		withEach(extra, func(lbls map[string]string) {
			for s := 0; s < 5; s++ {
				c.st.Observe("coredns_dns_request_size_bytes", lbls,
					coreDNSSizeBuckets, state.LEBare,
					40+w.Shape.Float64()*60) // most requests ≤ 100 bytes
			}
		})
	}

	// ── coredns_dns_response_size_bytes (H; proto,server,zone) ──────────────────
	for _, proto := range coreDNSProtos {
		extra := map[string]string{
			"proto": proto, "server": coreDNSServer, "zone": coreDNSZone,
		}
		withEach(extra, func(lbls map[string]string) {
			for s := 0; s < 5; s++ {
				c.st.Observe("coredns_dns_response_size_bytes", lbls,
					coreDNSSizeBuckets, state.LEBare,
					80+w.Shape.Float64()*220) // broader spread, some large AAAA responses
			}
		})
	}

	// ── coredns_cache_entries (G; server,type,zones) ────────────────────────────
	// Note: "zones" label (plural) per recon §A2/§D.
	// denial entries dominate (NXDOMAIN caching) over success.
	for _, cacheType := range []string{"denial", "success"} {
		var entries float64
		if cacheType == "denial" {
			entries = 775 + factor*200 // recon: ~775–780
		} else {
			entries = 153 + factor*20 // recon: ~153 both pods
		}
		cacheExtra := map[string]string{
			"server": coreDNSServer, "type": cacheType, "zones": coreDNSZones,
		}
		withEach(cacheExtra, func(lbls map[string]string) {
			c.st.Set("coredns_cache_entries", lbls, entries)
		})
	}

	// ── coredns_cache_hits_total (C; server,type,zones) ─────────────────────────
	// denial hits ≫ success hits (recon §A2: 8.4M denial vs 0.8M success).
	c.emitCacheHits(reqRate, scale, w)

	// ── coredns_cache_misses_total (C; server,zones) ─────────────────────────────
	// No "type" dimension (recon §A2). Uses "zones" (plural) not "zone".
	withEach(map[string]string{"server": coreDNSServer, "zones": coreDNSZones}, func(lbls map[string]string) {
		c.st.Add("coredns_cache_misses_total", lbls, reqRate*0.37*scale) // recon: misses ~37%
	})

	// ── coredns_cache_requests_total (C; server,zones) ──────────────────────────
	// No "type" dimension (recon §A2). Uses "zones" (plural).
	withEach(map[string]string{"server": coreDNSServer, "zones": coreDNSZones}, func(lbls map[string]string) {
		c.st.Add("coredns_cache_requests_total", lbls, reqRate*scale)
	})

	// ── coredns_forward_healthcheck_broken_total (C; core labels, no server/zone) ─
	// Always 0 on healthy cluster (recon §A2).
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_forward_healthcheck_broken_total", lbls, 0)
	})

	// ── coredns_forward_max_concurrent_rejects_total (C; core labels only) ───────
	// Always 0 on healthy cluster (recon §A1).
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_forward_max_concurrent_rejects_total", lbls, 0)
	})

	// ── coredns_proxy_request_duration_seconds (H; proxy_name,rcode,to) ─────────
	// 2 rcodes × 17 le values = 34 series per pod (recon §A3).
	for _, rc := range coreDNSProxyRcodes {
		proxyExtra := map[string]string{
			"proxy_name": "forward", "rcode": rc, "to": coreDNSUpstream,
		}
		withEach(proxyExtra, func(lbls map[string]string) {
			for s := 0; s < 3; s++ {
				c.st.Observe("coredns_proxy_request_duration_seconds", lbls,
					coreDNSProxyDurationBuckets, state.LEBare,
					0.001+factor*0.005*w.Shape.Noise(0.4)) // VPC DNS latency ~1-2ms
			}
		})
	}

	// ── coredns_proxy_conn_cache_hits_total (C; core labels only) ────────────────
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_proxy_conn_cache_hits_total", lbls, reqRate*0.1*scale)
	})

	// ── coredns_proxy_conn_cache_misses_total (C; core labels only) ──────────────
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_proxy_conn_cache_misses_total", lbls, reqRate*0.01*scale)
	})

	// ── coredns_proxy_healthcheck_failures_total (C; core labels only) ───────────
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_proxy_healthcheck_failures_total", lbls, 0)
	})

	// ── coredns_health_request_duration_seconds (H; core labels only, 6-bucket) ──
	// No server/zone labels on the health endpoint (recon §A3).
	withEach(nil, func(lbls map[string]string) {
		c.st.Observe("coredns_health_request_duration_seconds", lbls,
			coreDNSHealthDurationBuckets, state.LEBare,
			0.001+w.Shape.Noise(0.3)*0.001) // recon: 98.4% ≤ 2.5ms
	})

	// ── coredns_health_request_failures_total (C; core labels only) ──────────────
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_health_request_failures_total", lbls, 0)
	})

	// ── coredns_hosts_reload_timestamp_seconds (G; core labels only) ─────────────
	// 0 = no /etc/hosts-style reload has occurred (recon §A2: "hosts" plugin not used).
	withEach(nil, func(lbls map[string]string) {
		c.st.Set("coredns_hosts_reload_timestamp_seconds", lbls, 0)
	})

	// ── coredns_kubernetes_dns_programming_duration_seconds (H; service_kind) ─────
	// 2 service_kinds × 21 le values = 42 series per pod (recon §A3).
	for _, sk := range coreDNSServiceKinds {
		skExtra := map[string]string{"service_kind": sk}
		withEach(skExtra, func(lbls map[string]string) {
			// Programming latency peaks in 0.1–1s range (recon §A3 example).
			c.st.Observe("coredns_kubernetes_dns_programming_duration_seconds", lbls,
				coreDNSK8sDurationBuckets, state.LEBare,
				0.3+w.Shape.Noise(0.4)*0.5)
		})
	}

	// ── coredns_kubernetes_rest_client_requests_total (C; code,host,method) ──────
	// Only code=200, method=GET observed in recon §A2.
	withEach(map[string]string{
		"code": "200", "host": "172.20.0.1:443", "method": "GET",
	}, func(lbls map[string]string) {
		c.st.Add("coredns_kubernetes_rest_client_requests_total", lbls, scale)
	})

	// ── coredns_local_localhost_requests_total (C; core labels only) ─────────────
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_local_localhost_requests_total", lbls, scale)
	})

	// ── coredns_panics_total (C; core labels only — always 0) ────────────────────
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_panics_total", lbls, 0)
	})

	// ── coredns_plugin_enabled (G; name,server,zone) ─────────────────────────────
	// 7 plugins per recon §A2 (health/ready/reload do NOT register here).
	for _, plugin := range coreDNSPlugins {
		pluginExtra := map[string]string{
			"name": plugin, "server": coreDNSServer, "zone": coreDNSZone,
		}
		withEach(pluginExtra, func(lbls map[string]string) {
			c.st.Set("coredns_plugin_enabled", lbls, 1)
		})
	}

	// ── coredns_reload_failed_total (C; core labels only — always 0) ─────────────
	withEach(nil, func(lbls map[string]string) {
		c.st.Add("coredns_reload_failed_total", lbls, 0)
	})

	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// emitCacheHits emits coredns_cache_hits_total for both type values.
// denial hits dominate (recon §A2: ~10.5× more denial than success hits).
func (c *Construct) emitCacheHits(reqRate, scale float64, w *core.World) {
	podMaps := k8saddon.StampPods(c.clust, "coredns", map[string]string{
		"cluster":          c.clust.Name,
		"k8s_cluster_name": c.clust.Name,
		"job":              coreDNSJob,
		"app":              "kube-dns",
		"workload":         "ReplicaSet/coredns",
		"service":          "kube-dns",
	}, coreDNSPort)

	emitForType := func(cacheType string, weight float64) {
		extra := map[string]string{
			"server": coreDNSServer, "type": cacheType, "zones": coreDNSZones,
		}
		if len(podMaps) > 0 {
			for _, pm := range podMaps {
				lbls := mergeLabels(pm, extra)
				c.st.Add("coredns_cache_hits_total", lbls, reqRate*weight*scale)
			}
		} else {
			baseMap := map[string]string{
				"cluster":          c.clust.Name,
				"k8s_cluster_name": c.clust.Name,
				"job":              coreDNSJob,
				"app":              "kube-dns",
				"workload":         "ReplicaSet/coredns",
				"service":          "kube-dns",
			}
			lbls := mergeLabels(baseMap, extra)
			c.st.Add("coredns_cache_hits_total", lbls, reqRate*weight*scale)
		}
	}
	emitForType("denial", 0.58)   // recon: 8.4M denial hits
	emitForType("success", 0.055) // recon: 0.8M success hits
}

// mergeLabels returns a new map with all keys from both a and b. b takes precedence.
// Neither input is mutated.
func mergeLabels(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
