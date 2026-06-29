// SPDX-License-Identifier: AGPL-3.0-only

// controlplane.go — kube-apiserver, kube-scheduler, and kube-controller-manager metric
// families for the k8scluster construct. These are DOC-SOURCED: managed EKS does not
// expose these endpoints directly; values are representative.
//
// Substrate scope: labels cluster, k8s_cluster_name, job, instance only — NO blueprint label.
// Each function emits one instance (one apiserver/scheduler/controller-manager per cluster).
package k8scluster

import (
	statelib "github.com/rknightion/synthkit/internal/state"
)

// cpHistoBounds are the default seconds histogram bounds for all control-plane histograms.
var cpHistoBounds = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// ── apiserver ────────────────────────────────────────────────────────────────────────

const (
	jobApiServer  = "integrations/kubernetes/kube-apiserver"
	instApiServer = "kubernetes.default.svc:443"
)

// emitApiServer emits kube-apiserver metrics under job="integrations/kubernetes/kube-apiserver",
// instance="kubernetes.default.svc:443".
func emitApiServer(st *statelib.State, cluster string, tickSec, scale float64) {
	base := merge(k8sBase(cluster), map[string]string{
		"job":      jobApiServer,
		"instance": instApiServer,
	})

	// apiserver_request_total — counter. Live reference 2026-06-15 (apiServer scrape enabled on
	// a reference EKS cluster): labels verb/code/component/group/version/resource/scope (+ ambient).
	type reqCombo struct{ verb, resource, scope string }
	reqCombos := []reqCombo{
		{"GET", "pods", "resource"},
		{"LIST", "pods", "namespace"},
		{"GET", "configmaps", "resource"},
		{"POST", "configmaps", "resource"},
		{"GET", "secrets", "resource"},
		{"LIST", "secrets", "cluster"},
		{"POST", "events", "resource"},
		{"DELETE", "pods", "resource"},
	}
	for _, c := range reqCombos {
		st.Add("apiserver_request_total", merge(base, map[string]string{
			"verb":      c.verb,
			"resource":  c.resource,
			"scope":     c.scope, // live reference: cluster|namespace|resource
			"code":      "200",
			"component": "apiserver",
			"version":   "v1", // core API; `group` label is empty for core (omitted per I13)
		}), scale*(2+float64(len(c.verb))))
	}

	// apiserver_request_duration_seconds — histogram, labels verb/resource/scope
	durCombos := []reqCombo{
		{"GET", "pods", "resource"},
		{"LIST", "pods", "namespace"},
		{"POST", "events", "resource"},
	}
	for _, c := range durCombos {
		lbls := merge(base, map[string]string{
			"verb":     c.verb,
			"resource": c.resource,
			"scope":    c.scope,
		})
		// representative latency: GETs ~20ms, LIST ~80ms, POSTs ~40ms
		latency := 0.02
		switch c.verb {
		case "LIST":
			latency = 0.08
		case "POST":
			latency = 0.04
		}
		st.Observe("apiserver_request_duration_seconds", lbls, cpHistoBounds, statelib.LEBare, latency)
	}

	// apiserver_current_inflight_requests — gauge, label request_kind
	for _, rk := range []string{"mutating", "readOnly"} {
		val := 5.0
		if rk == "readOnly" {
			val = 12.0
		}
		st.Set("apiserver_current_inflight_requests", merge(base, map[string]string{
			"request_kind": rk,
		}), val)
	}

	// workqueue_* families — label name=admission_quota_controller
	wqName := "admission_quota_controller"
	wqLbls := merge(base, map[string]string{"name": wqName})
	st.Add("workqueue_adds_total", wqLbls, scale*3)
	st.Set("workqueue_depth", wqLbls, 0)
	st.Observe("workqueue_queue_duration_seconds", wqLbls, cpHistoBounds, statelib.LEBare, 0.001)
	st.Observe("workqueue_work_duration_seconds", wqLbls, cpHistoBounds, statelib.LEBare, 0.005)

	// rest_client_requests_total — counter, labels code/method/host
	st.Add("rest_client_requests_total", merge(base, map[string]string{
		"code":   "200",
		"method": "GET",
		"host":   "kubernetes.default.svc:443",
	}), scale*10)

	// etcd_request_duration_seconds — histogram. Live reference 2026-06-15: labels operation/resource
	// (NOT `type`) + group (empty for core → omitted per I13).
	type etcdCombo struct{ op, resource string }
	etcdCombos := []etcdCombo{
		{"get", "pods"},
		{"list", "pods"},
	}
	for _, c := range etcdCombos {
		lbls := merge(base, map[string]string{
			"operation": c.op,
			"resource":  c.resource,
		})
		latency := 0.002
		if c.op == "list" {
			latency = 0.01
		}
		st.Observe("etcd_request_duration_seconds", lbls, cpHistoBounds, statelib.LEBare, latency)
	}
}

// ── scheduler ────────────────────────────────────────────────────────────────────────

const (
	jobScheduler  = "kube-scheduler"
	instScheduler = "kube-scheduler:10259"
)

// emitScheduler emits kube-scheduler metrics under job="kube-scheduler",
// instance="kube-scheduler:10259".
func emitScheduler(st *statelib.State, cluster string, tickSec, scale float64) {
	base := merge(k8sBase(cluster), map[string]string{
		"job":      jobScheduler,
		"instance": instScheduler,
	})

	// scheduler_scheduling_attempt_duration_seconds — histogram, labels profile/result
	type schedCombo struct {
		result  string
		latency float64
	}
	schedCombos := []schedCombo{
		{"scheduled", 0.003},
		{"unschedulable", 0.001},
		{"error", 0.0005},
	}
	for _, c := range schedCombos {
		lbls := merge(base, map[string]string{
			"profile": "default-scheduler",
			"result":  c.result,
		})
		st.Observe("scheduler_scheduling_attempt_duration_seconds", lbls, cpHistoBounds, statelib.LEBare, c.latency)
	}

	// scheduler_pending_pods — gauge, label queue
	queueVals := map[string]float64{
		"active":        3,
		"backoff":       0,
		"unschedulable": 0,
		"gated":         0,
	}
	for q, v := range queueVals {
		st.Set("scheduler_pending_pods", merge(base, map[string]string{
			"queue": q,
		}), v)
	}

	// scheduler_schedule_attempts_total — counter, labels profile/result
	for _, c := range schedCombos {
		st.Add("scheduler_schedule_attempts_total", merge(base, map[string]string{
			"profile": "default-scheduler",
			"result":  c.result,
		}), scale)
	}

	// workqueue_* — label name=DynamicConfigMap
	wqLbls := merge(base, map[string]string{"name": "DynamicConfigMap"})
	st.Set("workqueue_depth", wqLbls, 0)
	st.Add("workqueue_adds_total", wqLbls, scale)

	// rest_client_requests_total — counter, labels code/method/host
	st.Add("rest_client_requests_total", merge(base, map[string]string{
		"code":   "200",
		"method": "GET",
		"host":   "kubernetes.default.svc:443",
	}), scale*5)
}

// ── controller-manager ───────────────────────────────────────────────────────────────

const (
	jobControllerManager  = "kube-controller-manager"
	instControllerManager = "kube-controller-manager:10257"
)

// emitControllerManager emits kube-controller-manager metrics under
// job="kube-controller-manager", instance="kube-controller-manager:10257".
func emitControllerManager(st *statelib.State, cluster string, tickSec, scale float64) {
	base := merge(k8sBase(cluster), map[string]string{
		"job":      jobControllerManager,
		"instance": instControllerManager,
	})

	// workqueue_* families — label name ∈ {node,replicaset,daemonset,deployment,disruption}
	wqNames := []string{"node", "replicaset", "daemonset", "deployment", "disruption"}
	for _, name := range wqNames {
		wqLbls := merge(base, map[string]string{"name": name})
		st.Add("workqueue_adds_total", wqLbls, scale*(1+float64(len(name)%3)))
		st.Set("workqueue_depth", wqLbls, 0)
		st.Observe("workqueue_queue_duration_seconds", wqLbls, cpHistoBounds, statelib.LEBare, 0.002)
		st.Observe("workqueue_work_duration_seconds", wqLbls, cpHistoBounds, statelib.LEBare, 0.01)
		st.Add("workqueue_retries_total", wqLbls, 0)
	}

	// rest_client_requests_total — counter, labels code/method/host
	st.Add("rest_client_requests_total", merge(base, map[string]string{
		"code":   "200",
		"method": "GET",
		"host":   "kubernetes.default.svc:443",
	}), scale*8)
}
