// SPDX-License-Identifier: AGPL-3.0-only

// kubelet_test.go — unit tests for the 4 new kubelet metric families added in
// the live-reference audit: kubernetes_build_info, volume_manager_total_volumes,
// storage_operation_duration_seconds_count, prober_probe_total +
// prober_probe_duration_seconds_{bucket,count,sum}.
package k8scluster_test

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
)

// TestKubernetesBuildInfo asserts that kubernetes_build_info is emitted per node with the
// required label keys and that git_version matches the cluster platform's KubeletVersion.
func TestKubernetesBuildInfo(t *testing.T) {
	cl := coretest.Cluster()
	// coretest.Cluster() leaves Platform zero — platformOr fills KubeletVersion → "v1.31.2-eks-f69f56f"
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	series := findSeries(mc, "kubernetes_build_info")
	if len(series) == 0 {
		t.Fatal("kubernetes_build_info: no series emitted")
	}

	// Expect one series per node (3 in coretest fixture)
	if len(series) != len(cl.Nodes) {
		t.Errorf("kubernetes_build_info: want %d series (one per node), got %d", len(cl.Nodes), len(series))
	}

	// Required label keys (audit §kubernetes_build_info)
	requiredKeys := []string{
		"build_date", "compiler", "git_commit", "git_tree_state",
		"git_version", "go_version", "major", "minor", "platform",
	}
	for _, s := range series {
		for _, k := range requiredKeys {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("kubernetes_build_info: missing label key %q (labels: %v)", k, s.Labels)
			}
		}
		// compiler must be "gc"
		if s.Labels["compiler"] != "gc" {
			t.Errorf("kubernetes_build_info: compiler=%q, want gc", s.Labels["compiler"])
		}
		// git_tree_state must be "clean"
		if s.Labels["git_tree_state"] != "clean" {
			t.Errorf("kubernetes_build_info: git_tree_state=%q, want clean", s.Labels["git_tree_state"])
		}
		// platform must start with "linux/"
		if !strings.HasPrefix(s.Labels["platform"], "linux/") {
			t.Errorf("kubernetes_build_info: platform=%q, want linux/<arch>", s.Labels["platform"])
		}
		// value must be 1
		if s.Value != 1 {
			t.Errorf("kubernetes_build_info: value=%v, want 1", s.Value)
		}
	}

	// git_version must match the resolved KubeletVersion (platformOr default)
	gitVersion := labelVal(mc, "kubernetes_build_info", "git_version")
	// platformOr default is "v1.31.2-eks-f69f56f"
	if gitVersion == "" {
		t.Error("kubernetes_build_info: git_version is empty")
	}
	if !strings.HasPrefix(gitVersion, "v") {
		t.Errorf("kubernetes_build_info: git_version=%q, want leading v", gitVersion)
	}

	// major/minor must be non-empty
	major := labelVal(mc, "kubernetes_build_info", "major")
	minor := labelVal(mc, "kubernetes_build_info", "minor")
	if major == "" {
		t.Error("kubernetes_build_info: major is empty")
	}
	if minor == "" {
		t.Error("kubernetes_build_info: minor is empty")
	}
}

// TestKubernetesBuildInfoGitVersionMatchesPlatform asserts that when the cluster has an explicit
// KubeletVersion set, git_version on kubernetes_build_info matches it exactly.
func TestKubernetesBuildInfoGitVersionMatchesPlatform(t *testing.T) {
	cl := coretest.Cluster()
	wantVersion := "v1.31.2-eks-f69f56f"
	cl.Platform.KubeletVersion = wantVersion

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	got := labelVal(mc, "kubernetes_build_info", "git_version")
	if got != wantVersion {
		t.Errorf("kubernetes_build_info: git_version=%q, want %q (platform KubeletVersion)", got, wantVersion)
	}
}

// TestVolumeManagerTotalVolumes asserts that volume_manager_total_volumes is emitted per node
// with plugin_name and state labels.
func TestVolumeManagerTotalVolumes(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	series := findSeries(mc, "volume_manager_total_volumes")
	if len(series) == 0 {
		t.Fatal("volume_manager_total_volumes: no series emitted")
	}

	// Each node emits 2 state values: actual_state_of_world, desired_state_of_world
	wantCount := len(cl.Nodes) * 2
	if len(series) != wantCount {
		t.Errorf("volume_manager_total_volumes: want %d series (%d nodes × 2 states), got %d",
			wantCount, len(cl.Nodes), len(series))
	}

	// Required label keys
	for _, s := range series {
		if _, ok := s.Labels["plugin_name"]; !ok {
			t.Errorf("volume_manager_total_volumes: missing label key plugin_name (labels: %v)", s.Labels)
		}
		if _, ok := s.Labels["state"]; !ok {
			t.Errorf("volume_manager_total_volumes: missing label key state (labels: %v)", s.Labels)
		}
	}

	// Distinct state values must be exactly {actual_state_of_world, desired_state_of_world}
	states := distinctVals(mc, "volume_manager_total_volumes", "state")
	if !states["actual_state_of_world"] {
		t.Error("volume_manager_total_volumes: state=actual_state_of_world not found")
	}
	if !states["desired_state_of_world"] {
		t.Error("volume_manager_total_volumes: state=desired_state_of_world not found")
	}

	// plugin_name must be "kubernetes.io/csi"
	pluginNames := distinctVals(mc, "volume_manager_total_volumes", "plugin_name")
	if !pluginNames["kubernetes.io/csi"] {
		t.Errorf("volume_manager_total_volumes: want plugin_name=kubernetes.io/csi, got %v", pluginNames)
	}
}

// TestStorageOperationDurationCount asserts that storage_operation_duration_seconds_count is
// emitted per node with the required operation_name, status, volume_plugin, migrated labels.
func TestStorageOperationDurationCount(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	series := findSeries(mc, "storage_operation_duration_seconds_count")
	if len(series) == 0 {
		t.Fatal("storage_operation_duration_seconds_count: no series emitted")
	}

	// Required label keys
	requiredKeys := []string{"operation_name", "status", "volume_plugin", "migrated"}
	for _, s := range series {
		for _, k := range requiredKeys {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("storage_operation_duration_seconds_count: missing label key %q (labels: %v)", k, s.Labels)
			}
		}
		// status must be "success"
		if s.Labels["status"] != "success" {
			t.Errorf("storage_operation_duration_seconds_count: status=%q, want success", s.Labels["status"])
		}
		// volume_plugin must be "kubernetes.io/csi"
		if s.Labels["volume_plugin"] != "kubernetes.io/csi" {
			t.Errorf("storage_operation_duration_seconds_count: volume_plugin=%q, want kubernetes.io/csi", s.Labels["volume_plugin"])
		}
		// migrated must be "false"
		if s.Labels["migrated"] != "false" {
			t.Errorf("storage_operation_duration_seconds_count: migrated=%q, want false", s.Labels["migrated"])
		}
	}

	// Distinct operation_name values must match the live EBS-CSI op set (live reference recon 2026-06-16);
	// volume_attach is a legacy in-tree op and must NOT appear on the CSI path.
	ops := distinctVals(mc, "storage_operation_duration_seconds_count", "operation_name")
	for _, want := range []string{"volume_mount", "volume_unmount", "unmount_device", "verify_controller_attached_volume", "volume_apply_access_control"} {
		if !ops[want] {
			t.Errorf("storage_operation_duration_seconds_count: operation_name=%s not found", want)
		}
	}
	if ops["volume_attach"] {
		t.Error("storage_operation_duration_seconds_count: legacy operation_name=volume_attach must not be emitted (absent from real CSI path)")
	}
}

// TestStorageOperationDurationCountMonotone asserts that storage_operation_duration_seconds_count
// is cumulative (does not decrease across ticks).
func TestStorageOperationDurationCountMonotone(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	lc := &coretest.LogCapture{}

	mc1 := &coretest.MetricCapture{}
	tick(t, c, mc1, lc)
	vals1 := map[string]float64{}
	for _, s := range findSeries(mc1, "storage_operation_duration_seconds_count") {
		vals1[labelSig(s.Labels)] = s.Value
	}

	mc2 := &coretest.MetricCapture{}
	tick(t, c, mc2, lc)
	for _, s := range findSeries(mc2, "storage_operation_duration_seconds_count") {
		key := labelSig(s.Labels)
		if v1, ok := vals1[key]; ok {
			if s.Value < v1 {
				t.Errorf("storage_operation_duration_seconds_count decreased: tick1=%.3f tick2=%.3f labels=%v",
					v1, s.Value, s.Labels)
			}
		}
	}
}

// TestProberProbeTotal asserts that prober_probe_total is emitted with the required label keys
// including pod_uid, probe_type, and result.
func TestProberProbeTotal(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane.KubeletProbes = true // gate enabled for this assertion
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	series := findSeries(mc, "prober_probe_total")
	if len(series) == 0 {
		t.Fatal("prober_probe_total: no series emitted")
	}

	// Required label keys per the live-reference audit §prober_probe_total
	requiredKeys := []string{"container", "namespace", "pod", "pod_uid", "probe_type", "result"}
	for _, s := range series {
		for _, k := range requiredKeys {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("prober_probe_total: missing label key %q (labels: %v)", k, s.Labels)
			}
		}
		// result must be "successful"
		if s.Labels["result"] != "successful" {
			t.Errorf("prober_probe_total: result=%q, want successful", s.Labels["result"])
		}
		// pod_uid must be non-empty
		if s.Labels["pod_uid"] == "" {
			t.Errorf("prober_probe_total: pod_uid is empty (labels: %v)", s.Labels)
		}
	}

	// Distinct probe_type values must include readiness, liveness, startup
	probeTypes := distinctVals(mc, "prober_probe_total", "probe_type")
	for _, pt := range []string{"readiness", "liveness", "startup"} {
		if !probeTypes[pt] {
			t.Errorf("prober_probe_total: probe_type=%q not found (got %v)", pt, probeTypes)
		}
	}

	// Job must be "integrations/kubernetes/probes"
	for _, s := range series {
		if s.Labels["job"] != "integrations/kubernetes/probes" {
			t.Errorf("prober_probe_total: job=%q, want integrations/kubernetes/probes", s.Labels["job"])
			break
		}
	}
}

// TestProberProbeDurationHistogram asserts that prober_probe_duration_seconds histogram families
// are emitted with le (bucket), count, and sum, and carry container/namespace/pod/probe_type.
func TestProberProbeDurationHistogram(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane.KubeletProbes = true // gate enabled for this assertion
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// Bucket series
	buckets := findSeries(mc, "prober_probe_duration_seconds_bucket")
	if len(buckets) == 0 {
		t.Fatal("prober_probe_duration_seconds_bucket: no series emitted")
	}
	for _, s := range buckets {
		if _, ok := s.Labels["le"]; !ok {
			t.Errorf("prober_probe_duration_seconds_bucket: missing le label (labels: %v)", s.Labels)
		}
		for _, k := range []string{"container", "namespace", "pod", "probe_type"} {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("prober_probe_duration_seconds_bucket: missing label %q (labels: %v)", k, s.Labels)
			}
		}
	}

	// Count series
	if !hasSeries(mc, "prober_probe_duration_seconds_count") {
		t.Error("prober_probe_duration_seconds_count: not emitted")
	}

	// Sum series
	if !hasSeries(mc, "prober_probe_duration_seconds_sum") {
		t.Error("prober_probe_duration_seconds_sum: not emitted")
	}
}

// TestProberProbeSourceLabel asserts prober series carry source="kubernetes" (I32: all k8s-monitoring
// series must carry source).
func TestProberProbeSourceLabel(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane.KubeletProbes = true // gate enabled for this assertion
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, nm := range []string{"prober_probe_total", "prober_probe_duration_seconds_bucket"} {
		for _, s := range findSeries(mc, nm) {
			if s.Labels["source"] != "kubernetes" {
				t.Errorf("%s: source=%q, want kubernetes", nm, s.Labels["source"])
				break
			}
		}
	}
}

// TestNewFamiliesCarryKubeletJob asserts that the three node-scoped new families carry
// job="integrations/kubernetes/kubelet" (not the probes job).
func TestNewFamiliesCarryKubeletJob(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, nm := range []string{
		"kubernetes_build_info",
		"volume_manager_total_volumes",
		"storage_operation_duration_seconds_count",
	} {
		series := findSeries(mc, nm)
		if len(series) == 0 {
			t.Errorf("%s: no series emitted", nm)
			continue
		}
		for _, s := range series {
			if s.Labels["job"] != "integrations/kubernetes/kubelet" {
				t.Errorf("%s: job=%q, want integrations/kubernetes/kubelet", nm, s.Labels["job"])
				break
			}
		}
	}
}
