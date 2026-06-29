// SPDX-License-Identifier: AGPL-3.0-only

// Package lbc implements the "load_balancer_controller" construct.
//
// Kind:     "load_balancer_controller"
// Scope:    core.ScopeSubstrate (no blueprint label — cluster disambiguates)
// Signals:  []{core.Metrics, core.Logs}
// Interval: 60 s
// Config:   empty struct (all identity comes from fixtures)
// Requires: fx.Cluster (error if nil)
//
// Emits five metric families (signals/k8s-addons.md [slug: k8s-lbc] / live recon svc-lbc.md):
//   - awslbc_*             custom LBC metrics
//   - aws_api_*            AWS SDK metrics
//   - controller_runtime_* controller-runtime framework metrics
//   - workqueue_*          workqueue metrics
//   - rest_client_*        client-go kube-apiserver client metrics
//
// Per-pod metric-correlation seam (ARCHITECTURE Phase 1 Task 1.A):
//
//	When fx.Cluster.SubstrateWorkloads contains the "aws-load-balancer-controller"
//	workload (2 pods, kube-system, container "aws-load-balancer-controller"), families
//	are stamped with per-pod join labels via k8saddon helpers, port 8080:
//
//	  - awslbc_*  (cache_object_total, reconcile_errors_total,
//	               reconcile_stage_duration, top_talkers)
//	    → StampLeader("aws-load-balancer-controller") — leader-elected, one pod
//	  - aws_api_* (calls_total, requests_total, durations, retries)
//	    → StampLeader — same leader-only pattern
//	  - controller_runtime_* / rest_client_* / workqueue_*
//	    → StampPods("aws-load-balancer-controller") — both replicas
//
//	If SubstrateWorkloads is absent (nil workload lookup → nil stamp), the fallback
//	emits the original cluster-scoped series (no pod/namespace/container/instance labels).
//	This preserves back-compat for tests and blueprints that don't populate addon pods.
//
// Label notes (live recon svc-lbc.md §E):
//
//   - aws_api_* use exported_service (not service) — Prometheus relabelling renames the
//     LBC-native "service" dimension because Alloy's identity stamp already claims "service".
//   - awslbc_controller_top_talkers uses exported_namespace (optional, gateway controllers only).
//   - retriesBuckets: integer buckets 0..10 (not the default second-range set).
//   - controller_runtime_reconcile_time_seconds uses 40-bucket fine set.
//   - No _build_info gauge (version in startup log + kube_pod_container_info image tag).
//
// Every series carries cluster + k8s_cluster_name (same value). No blueprint label
// (ScopeSubstrate invariant, ARCHITECTURE I21).
package lbc

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

// Registration returns the core.ConstructReg for the "load_balancer_controller" kind.
// Call this from the composition root's catalog wiring file; no init() self-registration.
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "load_balancer_controller",
		Doc:       "AWS Load Balancer Controller — awslbc_* / aws_api_* / controller_runtime_* / workqueue_* / rest_client_*",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build: func(cfg any, fx *fixture.Set) (core.Construct, error) {
			if fx.Cluster == nil {
				return nil, errors.New("load_balancer_controller: fx.Cluster is required")
			}
			return &construct{clust: fx.Cluster, st: state.NewState()}, nil
		},
	}
}

// ── Config ────────────────────────────────────────────────────────────────────

// Config is the YAML config struct for the load_balancer_controller construct.
// It is intentionally empty: all identity comes from the resolved cluster fixture.
// Unknown fields are rejected by strict yaml.v3 decoding at blueprint load.
type Config struct{}

// ── Construct ─────────────────────────────────────────────────────────────────

const (
	kind     = "load_balancer_controller"
	interval = 60 * time.Second
	lbcJob   = "aws-load-balancer-controller"
	lbcPort  = 8080
)

// lbcControllersAWSLBC is the representative set used for awslbc_* per-controller loops
// (reconcile_errors_total, reconcile_stage_duration). Live (live reference recon 2026-06-16):
// reconcile_stage_duration carries ingress+targetGroupBinding and reconcile_errors_total carries
// ingress only — "service" appears in neither, so it is excluded.
var lbcControllersAWSLBC = []string{"ingress", "targetGroupBinding"}

// lbcControllersRuntime is the full live-confirmed controller set for controller_runtime_*
// metrics (live recon svc-lbc.md §A.5b, a live reference cluster 2026-06-16). controller_runtime_* labels the
// gateway controllers with the LONG-FORM gateway.k8s.aws/{alb,nlb}; the SHORT-FORM
// albgateway/nlbgateway are the real names on the awslbc_controller_top_talkers metric (a different
// family that labels the same controllers differently).
var lbcControllersRuntime = []string{
	"ingress",
	"service",
	"targetGroupBinding",
	"gateway.k8s.aws/alb",
	"gateway.k8s.aws/nlb",
	"aws-lbc-gateway-class-controller",
	"aws-lbc-loadbalancerconfiguration-controller",
	"aws-lbc-listenerruleconfiguration-controller",
	"aws-lbc-targetgroupconfiguration-controller",
}

// lbcAWSServices are the AWS SDK service/operation pairs LBC calls.
// exported_service values: "EC2", "Elastic Load Balancing v2", "Shield" — use
// representative values matching the real recon (svc-lbc.md §A.6).
var lbcAWSServices = []struct{ service, operation string }{
	{"Elastic Load Balancing v2", "DescribeTargetGroups"},
	{"Elastic Load Balancing v2", "RegisterTargets"},
	{"Elastic Load Balancing v2", "DeregisterTargets"},
	{"EC2", "DescribeVpcs"},
	{"EC2", "DescribeSubnets"},
}

// lbcWorkqueues are the workqueue names (one per controller reconciler queue).
// Matches recon svc-lbc.md §A.5b workqueue_* label values.
var lbcWorkqueues = []string{
	"ingress",
	"targetGroupBinding",
	"gateway.k8s.aws/alb",
	"gateway.k8s.aws/nlb",
}

// lbcWebhooks are the webhook paths LBC registers (recon svc-lbc.md §A.5b).
var lbcWebhooks = []string{
	"/mutate-elbv2-k8s-aws-v1beta1-targetgroupbinding",
	"/validate-networking-v1-ingress",
}

// promDefaultSecondsBuckets matches the standard 11-bucket Prometheus default histogram
// bounds (svc-lbc.md §A.5: awslbc/aws_api duration families).
var promDefaultSecondsBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// webhookLatencyBuckets is the controller-runtime webhook latency very-fine le set
// (svc-lbc.md §A.5b + svc-cert-manager.md §3.4: 1e-08 … 1000, powers of ten). Distinct
// from the default buckets used by awslbc/aws_api duration.
var webhookLatencyBuckets = []float64{1e-08, 1e-07, 1e-06, 1e-05, 1e-04, 0.001, 0.01, 0.1, 1.0, 10.0, 100.0, 1000.0}

// retriesBuckets are the bounds for aws_api_call_retries (integer retry counts 0..10,
// per live recon svc-lbc.md §A.6 — NOT the default second-range set).
var retriesBuckets = []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

// fineTimeBuckets is the 40-bucket fine set used by controller_runtime_reconcile_time_seconds
// (svc-lbc.md §A.5b: "fine-grained 40-bucket default").
var fineTimeBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.15, 0.2, 0.25, 0.3, 0.35,
	0.4, 0.45, 0.5, 0.6, 0.7, 0.8, 0.9, 1, 1.25, 1.5,
	1.75, 2, 2.5, 3, 3.5, 4, 4.5, 5, 6, 7,
	8, 9, 10, 15, 20, 25, 30, 40, 50, 60,
}

type construct struct {
	clust *fixture.Cluster
	st    *state.State
}

// Kind implements core.Construct.
func (c *construct) Kind() string { return kind }

// Signals implements core.Construct — metrics + logs.
func (c *construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }

// Interval implements core.Construct.
func (c *construct) Interval() time.Duration { return interval }

// Tick renders one metric + log batch for the cluster.
func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	factor := w.Shape.Factor(now, c.clust.Env.Weight, c.clust.Env.NonProd)
	tickSec := interval.Seconds()
	scale := tickSec / 30.0

	// base label map — cluster scope, no pod labels.
	// endpoint="metrics-server" is the ServiceMonitor port name (svc-lbc.md §A.7).
	// NOTE: asserts_env is Asserts read-side injection — NOT emitted here.
	base := map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"job":              lbcJob,
		"endpoint":         "metrics-server",
	}

	// ── Per-pod stamp helpers ─────────────────────────────────────────────────
	//
	// leaderMaps: ONE label-set (leader pod) for awslbc_* and aws_api_* families.
	// allPodMaps: label-set per replica for controller_runtime_*/rest_client_*/workqueue_*.
	//
	// Both return nil when SubstrateWorkloads is absent → fallback to cluster-scoped base.
	leaderMaps := k8saddon.StampLeader(c.clust, lbcJob, base, lbcPort)
	allPodMaps := k8saddon.StampPods(c.clust, lbcJob, base, lbcPort)

	// withLeader emits a metric for each leader pod label-set (or base fallback).
	// extra labels are merged on top without mutating the stamped map.
	withLeader := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(leaderMaps) > 0 {
			for _, pm := range leaderMaps {
				emit(mergeLabels(pm, extra))
			}
		} else {
			emit(mergeLabels(base, extra))
		}
	}

	// withAllPods emits for all replica pods (or base fallback).
	withAllPods := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(allPodMaps) > 0 {
			for _, pm := range allPodMaps {
				emit(mergeLabels(pm, extra))
			}
		} else {
			emit(mergeLabels(base, extra))
		}
	}

	hb := promDefaultSecondsBuckets

	// ── awslbc_* custom metrics (leader-only) ────────────────────────────────

	// awslbc_readiness_gate_ready_seconds (H) — leader-only
	for _, ctrl := range lbcControllersAWSLBC {
		for i := 0; i < 2+w.Shape.IntN(3); i++ {
			name := ctrl + "-obj"
			withLeader(map[string]string{"namespace": "kube-system", "name": name, "controller": ctrl},
				func(lbls map[string]string) {
					c.st.Observe("awslbc_readiness_gate_ready_seconds", lbls, hb, state.LEBare,
						0.05+factor*0.3*w.Shape.Noise(0.4))
				})
		}
	}

	// awslbc_controller_reconcile_errors_total (C) — leader-only
	for _, ctrl := range lbcControllersAWSLBC {
		for _, errCat := range []string{"build_load_balancer_error", "build_model_error"} {
			delta := 0.0
			if w.Shape.Float64() < 0.05*factor {
				delta = 1 * scale
			}
			withLeader(map[string]string{"controller": ctrl, "error_category": errCat},
				func(lbls map[string]string) {
					c.st.Add("awslbc_controller_reconcile_errors_total", lbls, delta)
				})
		}
	}

	// awslbc_controller_reconcile_stage_duration (H) — leader-only
	for _, ctrl := range lbcControllersAWSLBC {
		for _, stage := range []string{"fetch_ingress", "build_model", "deploy_model"} {
			samples := 1 + w.Shape.IntN(4)
			for s := 0; s < samples; s++ {
				withLeader(map[string]string{"controller": ctrl, "reconcile_stage": stage},
					func(lbls map[string]string) {
						c.st.Observe("awslbc_controller_reconcile_stage_duration", lbls, hb,
							state.LEBare, 0.01+factor*0.08*w.Shape.Noise(0.3))
					})
			}
		}
	}

	// awslbc_controller_cache_object_total (G) — leader-only
	for _, res := range []string{"ingress", "service", "targetgroupbinding"} {
		count := 3.0 + factor*10*w.Shape.Noise(0.1)
		withLeader(map[string]string{"resource": res},
			func(lbls map[string]string) {
				c.st.Set("awslbc_controller_cache_object_total", lbls, count)
			})
	}

	// awslbc_controller_top_talkers (G) — leader-only
	// controller uses the SHORT-FORM names live (live reference recon 2026-06-16): ingress, albgateway,
	// nlbgateway, targetgroupbinding — distinct from the long-form gateway.k8s.aws/{alb,nlb} on
	// controller_runtime_reconcile_total. exported_namespace is OPTIONAL (gateway controllers only).
	for _, tt := range []struct {
		controller, name, exportedNamespace string
	}{
		{"ingress", "synthkit-recon", ""},
		{"albgateway", "tailscale", "envoy-gateway-system"},
		{"nlbgateway", "tailscale", "envoy-gateway-system"},
		{"targetgroupbinding", "synthkit-recon", ""},
	} {
		extra := map[string]string{"controller": tt.controller, "name": tt.name}
		if tt.exportedNamespace != "" {
			extra["exported_namespace"] = tt.exportedNamespace
		}
		withLeader(extra, func(lbls map[string]string) {
			c.st.Set("awslbc_controller_top_talkers", lbls, 1+factor*20)
		})
	}

	// awslbc_webhook_* (C) — leader-only (webhook hits the leader's admission webhook)
	for _, wh := range lbcWebhooks {
		delta := 0.0
		if w.Shape.Float64() < 0.02 {
			delta = 1 * scale
		}
		withLeader(map[string]string{"webhook_name": wh, "error_category": "validation"},
			func(lbls map[string]string) {
				c.st.Add("awslbc_webhook_validation_failure_total", lbls, delta)
				c.st.Add("awslbc_webhook_mutation_failure_total", lbls, 0)
			})
	}

	// awslbc_quic_target_missing_server_id (C) — leader-only
	withLeader(nil, func(lbls map[string]string) {
		c.st.Add("awslbc_quic_target_missing_server_id", lbls, 0)
	})

	// ── aws_api_* (AWS SDK metrics) — leader-only ────────────────────────────
	//
	// IMPORTANT: the label key is "exported_service" (not "service").
	// Prometheus relabelling renames the LBC-native "service" dimension because Alloy's
	// identity stamp already claims "service" as a stream label (svc-lbc.md §E.3).

	for _, svc := range lbcAWSServices {
		withLeader(map[string]string{"exported_service": svc.service, "operation": svc.operation, "status_code": "200"},
			func(lbls map[string]string) {
				c.st.Add("aws_api_calls_total", lbls, (2+factor*8)*scale)
			})

		withLeader(map[string]string{"exported_service": svc.service, "operation": svc.operation},
			func(lbls map[string]string) {
				c.st.Observe("aws_api_call_duration_seconds", lbls, hb, state.LEBare,
					0.05+factor*0.15*w.Shape.Noise(0.3))
				c.st.Observe("aws_api_call_retries", lbls, retriesBuckets, state.LEBare, 0)
				c.st.Add("aws_api_requests_total", lbls, (2+factor*8)*scale)
				c.st.Observe("aws_api_request_duration_seconds", lbls, hb, state.LEBare,
					0.05+factor*0.1*w.Shape.Noise(0.2))
				c.st.Add("aws_api_call_permission_errors_total", lbls, 0)
				c.st.Add("aws_api_call_service_limit_exceeded_errors_total", lbls, 0)
				c.st.Add("aws_api_call_throttled_errors_total", lbls, 0)
				c.st.Add("aws_api_call_validation_errors_total", lbls, 0)
			})
	}

	// aws_target_group_info (G) — leader-only (one TG per ingress)
	for _, ns := range []string{"default", "production"} {
		tgARN := "arn:aws:elasticloadbalancing:us-east-1:" + cluster + ":targetgroup/k8s-" + ns + "-http/abcdef01"
		withLeader(map[string]string{"namespace": ns, "service": "frontend", "target_group": tgARN},
			func(lbls map[string]string) {
				c.st.Set("aws_target_group_info", lbls, 1)
			})
	}

	// ── controller_runtime_* — all pods ──────────────────────────────────────

	for _, ctrl := range lbcControllersRuntime {
		for _, result := range []string{"success", "error", "requeue", "requeue_after"} {
			var delta float64
			switch result {
			case "success":
				delta = (5 + factor*20) * scale
			case "error":
				if w.Shape.Float64() < 0.03*factor {
					delta = 1 * scale
				}
			case "requeue":
				delta = (1 + factor*3) * scale
			case "requeue_after":
				delta = (1 + factor*3) * scale
			}
			withAllPods(map[string]string{"controller": ctrl, "result": result},
				func(lbls map[string]string) {
					c.st.Add("controller_runtime_reconcile_total", lbls, delta)
				})
		}
		withAllPods(map[string]string{"controller": ctrl},
			func(lbls map[string]string) {
				c.st.Add("controller_runtime_reconcile_errors_total", lbls, 0)
				c.st.Observe("controller_runtime_reconcile_time_seconds", lbls, fineTimeBuckets,
					state.LEBare, 0.02+factor*0.1*w.Shape.Noise(0.3))
				c.st.Set("controller_runtime_active_workers", lbls, 1+factor*2)
				c.st.Set("controller_runtime_max_concurrent_reconciles", lbls, 3)
			})
	}

	// controller_runtime webhook metrics — all pods
	for _, wh := range lbcWebhooks {
		for _, code := range []string{"200", "500"} {
			var delta float64
			switch code {
			case "200":
				delta = (3 + factor*10) * scale
			default:
				if w.Shape.Float64() < 0.01 {
					delta = 1 * scale
				}
			}
			withAllPods(map[string]string{"webhook": wh, "code": code},
				func(lbls map[string]string) {
					c.st.Add("controller_runtime_webhook_requests_total", lbls, delta)
				})
		}
		withAllPods(map[string]string{"webhook": wh},
			func(lbls map[string]string) {
				c.st.Observe("controller_runtime_webhook_latency_seconds", lbls, webhookLatencyBuckets,
					state.LEBare, 0.005+factor*0.02*w.Shape.Noise(0.3))
			})
	}

	// ── rest_client_* — all pods ──────────────────────────────────────────────
	// LBC is a controller-runtime/sigs.k8s.io based controller and uses client-go
	// for kube-apiserver access (recon svc-lbc.md §A, §E.2).

	for _, meth := range []string{"GET", "PUT", "POST"} {
		withAllPods(map[string]string{"code": "200", "method": meth, "host": "172.20.0.1:443"},
			func(lbls map[string]string) {
				c.st.Add("rest_client_requests_total", lbls, scale)
			})
	}

	// ── workqueue_* — all pods ────────────────────────────────────────────────

	for _, wq := range lbcWorkqueues {
		withAllPods(map[string]string{"name": wq, "controller": wq},
			func(lbls map[string]string) {
				c.st.Set("workqueue_depth", lbls, factor*3)
				c.st.Add("workqueue_adds_total", lbls, (3+factor*15)*scale)
				c.st.Observe("workqueue_queue_duration_seconds", lbls, hb, state.LEBare,
					0.005+factor*0.05*w.Shape.Noise(0.3))
				c.st.Observe("workqueue_work_duration_seconds", lbls, hb, state.LEBare,
					0.01+factor*0.1*w.Shape.Noise(0.3))
				c.st.Add("workqueue_retries_total", lbls, factor*0.5*scale)
			})
	}

	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return err
	}

	// ── JSON-structured logs (OTel/Alloy Loki stream labels) ─────────────────
	//
	// NOTE: kube-system pod logs are absent from Loki on the reference cluster (scrape gap), but a
	// real prod cluster ships them — synthesize anyway (svc-lbc.md §C.1).
	//
	// Guard: skip silently when w.Logs is nil (tests that only probe metrics).
	if w.Logs != nil {
		streams := buildLBCLogStreams(cluster, c.clust, now, allPodMaps)
		if len(streams) > 0 {
			if err := w.Logs.Write(ctx, streams); err != nil {
				return err
			}
		}
	}
	return nil
}

// lbcLogMessages is the pool of realistic log lines for LBC (svc-lbc.md §C.3).
// Indexed deterministically by tick to produce varied-but-stable output.
// High-card fields (reconcileID, ARNs, error text) are always in the body, never stream labels.
var lbcLogMessages = []struct {
	level  string
	logger string
	msg    string
	extra  string // additional JSON key-value pairs (comma-prefixed)
}{
	{"info", "controller-runtime.metrics", "Serving metrics server", `,"bindAddress":":8080","secure":false`},
	{"info", "controllers.ingress", "successfully built model", `,"controller":"ingress"`},
	{"info", "controllers.ingress", "reconciling ingress", `,"controller":"ingress","reconcileID":"d8188620-2415-43ab-bcd5-753c8f31461d"`},
	{"info", "controllers.ingress", "successfully deployed model", `,"ingressGroup":"synthkit-recon"`},
	{"info", "controller-runtime.certwatcher", "Updated current TLS certificate", ``},
	{"info", "", "Starting workers", `,"controller":"ingress","worker count":3`},
	{"error", "controllers.ingress", "Reconciler error", `,"controller":"ingress","reconcileID":"7b6e3f1d-ac9b-44cc-8bd1-a32a55cdb078","error":"couldn't auto-discover subnets: subnets count less than minimal required count: 1 < 2"`},
	{"info", "controllers.ingress", "creating securityGroup", `,"resourceID":"ManagedLBSecurityGroup"`},
	{"info", "", "Successfully acquired lease", `,"lock":"kube-system/aws-load-balancer-controller-leader"`},
	{"error", "controllers.ingress", "Reconciler error", `,"controller":"ingress","reconcileID":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","error":"conflicting scheme: map[internal:{} internet-facing:{}]"`},
}

// buildLBCLogStreams constructs Loki streams for one LBC tick (pure, no I/O).
// allPodMaps is the per-pod label-sets from StampPods; may be nil for fallback.
func buildLBCLogStreams(
	cluster string,
	cl *fixture.Cluster,
	now time.Time,
	allPodMaps []map[string]string,
) []loki.Stream {
	// Determine pod name(s) for the stream label — use BOTH pods (LEADER emits reconcile lines).
	// If SubstrateWorkloads is absent, fall back to an empty string (label omitted).
	var podNames []string
	if len(allPodMaps) > 0 {
		seen := map[string]bool{}
		for _, pm := range allPodMaps {
			if pn := pm["pod"]; pn != "" && !seen[pn] {
				podNames = append(podNames, pn)
				seen[pn] = true
			}
		}
	}

	// Deterministic line selection using tick index (seconds since epoch / interval).
	tick := int(now.Unix()) / int(interval.Seconds())
	idx := tick % len(lbcLogMessages)
	line := lbcLogMessages[idx]

	ts := now.UTC().Format(time.RFC3339)

	// Build body: zap-JSON with "ts" field (NOT "time" — svc-lbc.md §E.4).
	var body string
	if line.logger != "" {
		body = fmt.Sprintf(`{"level":%q,"ts":%q,"logger":%q,"msg":%q%s}`,
			line.level, ts, line.logger, line.msg, line.extra)
	} else {
		body = fmt.Sprintf(`{"level":%q,"ts":%q,"msg":%q%s}`,
			line.level, ts, line.msg, line.extra)
	}

	detectedLevel := line.level // info → info, error → error (no warn in LBC logs per recon)

	// Base stream labels (low-card only — no reconcileID, error text, ARNs).
	// service_name = lbcJob per OTel/Alloy convention (svc-lbc.md §C.1).
	_ = cl // cl reserved for future fixture fields (e.g. service_instance_id)
	streamBase := map[string]string{
		"cluster":                cluster,
		"k8s_cluster_name":       cluster,
		"k8s_namespace_name":     "kube-system",
		"k8s_container_name":     "aws-load-balancer-controller",
		"k8s_deployment_name":    "aws-load-balancer-controller",
		"service_name":           lbcJob,
		"service_namespace":      "kube-system",
		"app_kubernetes_io_name": "aws-load-balancer-controller",
		"log_iostream":           "stderr",
	}

	if len(podNames) == 0 {
		// Fallback: absent SubstrateWorkloads — emit one stream without k8s_pod_name.
		lbls := cloneMap(streamBase)
		lbls["detected_level"] = detectedLevel
		return []loki.Stream{
			{Labels: lbls, Lines: []loki.Line{{T: now, Body: body}}},
		}
	}

	// Emit one stream per pod (each gets its own detected_level-labelled stream).
	out := make([]loki.Stream, 0, len(podNames))
	for _, pn := range podNames {
		lbls := cloneMap(streamBase)
		lbls["k8s_pod_name"] = pn
		lbls["detected_level"] = detectedLevel
		out = append(out, loki.Stream{
			Labels: lbls,
			Lines:  []loki.Line{{T: now, Body: body}},
		})
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

// mergeLabels merges extra on top of base into a new map — never mutates either input.
func mergeLabels(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
