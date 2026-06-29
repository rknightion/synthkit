// SPDX-License-Identifier: AGPL-3.0-only

package beyla

import "testing"

func TestModeLabelKeys(t *testing.T) {
	k8s := ResourceLabelKeys(ModeKubernetes)
	// Full live-confirmed k8s envelope (reference cluster Beyla 3.20.0): every k8s_* key is present
	// (absent dims emit "" — the ABSENT-DIM TRAP), not just the headline few.
	for _, w := range []string{
		"service_name", "service_namespace",
		"k8s_cluster_name", "k8s_namespace_name", "k8s_node_name",
		"k8s_pod_name", "k8s_pod_uid", "k8s_pod_start_time",
		"k8s_container_name", "k8s_kind", "k8s_owner_name",
		"k8s_deployment_name", "k8s_replicaset_name", "k8s_daemonset_name",
		"k8s_statefulset_name", "k8s_job_name", "k8s_cronjob_name",
	} {
		if !contains(k8s, w) {
			t.Fatalf("k8s missing %q: %v", w, k8s)
		}
	}
	std := ResourceLabelKeys(ModeStandalone)
	if contains(std, "k8s_namespace_name") {
		t.Fatalf("standalone must not carry k8s_*: %v", std)
	}
	for _, w := range []string{"service_name", "host_name", "host_id"} {
		if !contains(std, w) {
			t.Fatalf("standalone missing %q: %v", w, std)
		}
	}
}

func TestClientMetricForHopKind(t *testing.T) {
	for kind, want := range map[string]string{
		"service": "http_client_request_duration_seconds",
		"db":      "db_client_operation_duration_seconds",
		"cache":   "db_client_operation_duration_seconds",
	} {
		if got := ClientDurationMetric(kind); got != want {
			t.Fatalf("%q: got %q want %q", kind, got, want)
		}
	}
}

func TestClientDurationMetricDefault(t *testing.T) {
	// Unknown / empty hop kinds fall back to the HTTP client instrument.
	for _, kind := range []string{"", "unknown", "queue"} {
		if got := ClientDurationMetric(kind); got != MetricHTTPClientDuration {
			t.Fatalf("default %q: got %q want %q", kind, got, MetricHTTPClientDuration)
		}
	}
}

func TestServerDurationMetric(t *testing.T) {
	if got := ServerDurationMetric(); got != MetricHTTPServerDuration {
		t.Fatalf("got %q want %q", got, MetricHTTPServerDuration)
	}
}

func TestContextEmissionMatrix(t *testing.T) {
	// ebpf_only: Beyla is the sole source → RED + span + network all on.
	e := Emission(ContextEBPFOnly)
	if !e.RED || !e.SpanMetrics || !e.ServiceGraph || !e.Traces || !e.Network {
		t.Fatalf("ebpf_only must emit all: %+v", e)
	}
	if e.AvoidedService {
		t.Fatalf("ebpf_only must NOT set avoided_services")
	}
	if !e.TargetInfo {
		t.Fatalf("ebpf_only must emit target_info: %+v", e)
	}
	// coexist_sdk: SDK covers RED/span/traces → only network + target_info + avoided.
	c := Emission(ContextCoexistSDK)
	if c.RED || c.SpanMetrics || c.ServiceGraph || c.Traces {
		t.Fatalf("coexist must suppress SDK-covered signals: %+v", c)
	}
	if !c.Network || !c.TargetInfo || !c.AvoidedService {
		t.Fatalf("coexist must emit network+target_info+avoided: %+v", c)
	}
}

func TestEmissionUnknownContext(t *testing.T) {
	// An unrecognised context is treated as the safe default (ebpf_only sole source).
	if got := Emission(Context("bogus")); got != Emission(ContextEBPFOnly) {
		t.Fatalf("unknown context should default to ebpf_only: %+v", got)
	}
}

func TestDefaultFeatures(t *testing.T) {
	k8s := DefaultFeatures(ModeKubernetes)
	for _, w := range []string{FeatureApplication, FeatureNetwork, FeatureServiceGraph, FeatureAppSpan, FeatureAppHost} {
		if !contains(k8s, w) {
			t.Fatalf("k8s default features missing %q: %v", w, k8s)
		}
	}
	std := DefaultFeatures(ModeStandalone)
	if contains(std, FeatureAppHost) {
		t.Fatalf("standalone default features must drop %q: %v", FeatureAppHost, std)
	}
}

func TestDistroMarker(t *testing.T) {
	if DistroName != "opentelemetry-ebpf-instrumentation" {
		t.Fatalf("distro name wrong: %q", DistroName)
	}
	if AttrDistroName != "telemetry.distro.name" {
		t.Fatalf("distro attr key wrong: %q", AttrDistroName)
	}
}

func TestSourceValue(t *testing.T) {
	if SourceValue() != "beyla" {
		t.Fatalf("source value wrong: %q", SourceValue())
	}
}

// TestTargetInfoOriginValues pins the live-confirmed target_info origin values: Beyla's
// telemetry_sdk_name is "beyla" (NOT "opentelemetry"), distro version is "unset".
func TestTargetInfoOriginValues(t *testing.T) {
	if SDKNameBeyla != "beyla" {
		t.Fatalf("telemetry_sdk_name must be \"beyla\", got %q", SDKNameBeyla)
	}
	if SDKName != "beyla" {
		t.Fatalf("SDKName back-compat alias must be \"beyla\", got %q", SDKName)
	}
	if DistroVersionUnset != "unset" {
		t.Fatalf("telemetry_distro_version must be \"unset\", got %q", DistroVersionUnset)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
