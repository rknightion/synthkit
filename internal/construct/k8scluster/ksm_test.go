// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// bottlerocketCluster returns a scalable test cluster whose Platform is Bottlerocket (so the
// extended node-condition set lights up) and whose single node group is Karpenter-provisioned.
func bottlerocketCluster(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	cl.Seed = cl.Name
	cl.Region = cl.Cloud.Region
	cl.Platform = fixture.Platform{
		OSImage:           "Bottlerocket OS 1.62.0 (aws-k8s-1.35)",
		OSID:              "bottlerocket",
		ContainerRuntime:  "containerd://2.1.7+bottlerocket",
		KubeletVersion:    "v1.35.2-eks-f69f56f",
		KubernetesVersion: "1.35",
		KernelVersion:     "6.12.88",
	}
	cl.NodeGroups = []fixture.NodeGroupSpec{{Name: "default", InstanceType: "m6g.large", Provisioner: "karpenter"}}
	// Re-derive nodes so they pick up the Karpenter provisioner + node group.
	total := 0
	for _, wl := range cl.Workloads {
		total += wl.Replicas
	}
	cl.Nodes = fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, total)
	for i := range cl.Workloads {
		wl := &cl.Workloads[i]
		wl.PodNames, wl.NodeIdx = nil, nil
		for p := 0; p < wl.Replicas; p++ {
			wl.PodNames = append(wl.PodNames, fixture.PodName(cl.Seed, wl.Name, p))
			wl.NodeIdx = append(wl.NodeIdx, p%len(cl.Nodes))
		}
	}
	return cl
}

func captureTick(t *testing.T, cl *fixture.Cluster) *coretest.MetricCapture {
	t.Helper()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)
	return mc
}

// hasKey reports whether the first series named nm carries label key k.
func hasKey(mc *coretest.MetricCapture, nm, k string) bool {
	for _, s := range mc.Find(nm) {
		if _, ok := s.Labels[k]; ok {
			return true
		}
	}
	return false
}

// ── Task 1: kube_node_labels enrichment ─────────────────────────────────────────────

func TestNodeLabelsEnriched(t *testing.T) {
	cl := bottlerocketCluster(t)
	mc := captureTick(t, cl)

	wantKeys := []string{
		"label_kubernetes_io_os",
		"label_topology_kubernetes_io_region",
		"label_kubernetes_io_hostname",
		"label_beta_kubernetes_io_instance_type",
		"label_k8s_io_cloud_provider_aws",
		"label_node_kubernetes_io_instance_type",
		"label_kubernetes_io_arch",
		"label_topology_kubernetes_io_zone",
	}
	for _, k := range wantKeys {
		if !hasKey(mc, "kube_node_labels", k) {
			t.Errorf("kube_node_labels missing key %q", k)
		}
	}
	if got := distinctVals(mc, "kube_node_labels", "label_kubernetes_io_os"); !got["linux"] {
		t.Errorf("label_kubernetes_io_os = %v, want linux", got)
	}
	if got := distinctVals(mc, "kube_node_labels", "label_topology_kubernetes_io_region"); !got[cl.Region] {
		t.Errorf("label_topology_kubernetes_io_region = %v, want %q", got, cl.Region)
	}
	// 16-hex cloud-provider value.
	for _, s := range mc.Find("kube_node_labels") {
		v := s.Labels["label_k8s_io_cloud_provider_aws"]
		if len(v) != 16 {
			t.Errorf("label_k8s_io_cloud_provider_aws = %q, want 16-hex", v)
		}
	}
	// Karpenter provisioner key present; managed key absent.
	if !hasKey(mc, "kube_node_labels", "label_karpenter_sh_nodepool") {
		t.Error("karpenter cluster: kube_node_labels missing label_karpenter_sh_nodepool")
	}
	if hasKey(mc, "kube_node_labels", "label_eks_amazonaws_com_nodegroup") {
		t.Error("karpenter cluster: kube_node_labels must NOT carry label_eks_amazonaws_com_nodegroup")
	}
}

func TestNodeLabelsManagedProvisioner(t *testing.T) {
	// coretest.Cluster() nodes have empty Provisioner → managed nodegroup label.
	cl := coretest.Cluster()
	mc := captureTick(t, cl)
	if !hasKey(mc, "kube_node_labels", "label_eks_amazonaws_com_nodegroup") {
		t.Error("managed cluster: kube_node_labels missing label_eks_amazonaws_com_nodegroup")
	}
	if hasKey(mc, "kube_node_labels", "label_karpenter_sh_nodepool") {
		t.Error("managed cluster: kube_node_labels must NOT carry label_karpenter_sh_nodepool")
	}
}

// ── Task 2: kube_node_status_condition — PIDPressure + Bottlerocket extras ───────────

func TestNodeConditionsExtended(t *testing.T) {
	cl := bottlerocketCluster(t)
	mc := captureTick(t, cl)

	conds := distinctVals(mc, "kube_node_status_condition", "condition")
	want := []string{
		"Ready", "MemoryPressure", "DiskPressure", "PIDPressure",
		"ContainerRuntimeReady", "KernelReady", "NetworkingReady", "StorageReady",
	}
	for _, c := range want {
		if !conds[c] {
			t.Errorf("kube_node_status_condition missing condition %q (got %v)", c, conds)
		}
	}
	// status enum true/false present.
	st := distinctVals(mc, "kube_node_status_condition", "status")
	if !st["true"] || !st["false"] {
		t.Errorf("kube_node_status_condition status = %v, want true+false", st)
	}
}

func TestNodeConditionsNonBottlerocketHasPIDOnly(t *testing.T) {
	cl := coretest.Cluster() // empty Platform → Amazon Linux default, no bottlerocket extras
	mc := captureTick(t, cl)
	conds := distinctVals(mc, "kube_node_status_condition", "condition")
	if !conds["PIDPressure"] {
		t.Error("non-bottlerocket cluster: PIDPressure must still be present")
	}
	for _, extra := range []string{"ContainerRuntimeReady", "KernelReady", "NetworkingReady", "StorageReady"} {
		if conds[extra] {
			t.Errorf("non-bottlerocket cluster: must NOT emit %q", extra)
		}
	}
}

// ── Task 3: kube_node_status_capacity / _allocatable ─────────────────────────────────

func TestNodeCapacityPodsAndHugepages(t *testing.T) {
	cl := bottlerocketCluster(t) // m6g.large
	mc := captureTick(t, cl)

	wantPods := float64(fixture.LookupInstanceSpec("m6g.large").MaxPods())
	if wantPods == 110 {
		t.Fatal("test premise: m6g.large should have a catalogue MaxPods != 110 fallback")
	}
	gotPods := false
	for _, s := range mc.Find("kube_node_status_capacity") {
		if s.Labels["resource"] == "pods" {
			gotPods = true
			if s.Value != wantPods {
				t.Errorf("kube_node_status_capacity pods = %v, want MaxPods %v", s.Value, wantPods)
			}
		}
	}
	if !gotPods {
		t.Error("no kube_node_status_capacity{resource=pods} series")
	}

	// hugepages variants present on both capacity and allocatable, value 0, unit byte.
	hp := []string{"hugepages_1Gi", "hugepages_2Mi", "hugepages_32Mi", "hugepages_64Ki"}
	for _, metric := range []string{"kube_node_status_capacity", "kube_node_status_allocatable"} {
		got := map[string]bool{}
		for _, s := range mc.Find(metric) {
			r := s.Labels["resource"]
			for _, h := range hp {
				if r == h {
					got[h] = true
					if s.Value != 0 {
						t.Errorf("%s{resource=%s} = %v, want 0", metric, h, s.Value)
					}
					if s.Labels["unit"] != "byte" {
						t.Errorf("%s{resource=%s} unit = %q, want byte", metric, h, s.Labels["unit"])
					}
				}
			}
		}
		for _, h := range hp {
			if !got[h] {
				t.Errorf("%s missing hugepages resource %q", metric, h)
			}
		}
	}
}

// ── Task 4: kube_node_info Platform values ───────────────────────────────────────────

func TestNodeInfoPlatform(t *testing.T) {
	cl := bottlerocketCluster(t)
	mc := captureTick(t, cl)

	checks := map[string]string{
		"os_image":                  cl.Platform.OSImage,
		"container_runtime_version": cl.Platform.ContainerRuntime,
		"kubelet_version":           cl.Platform.KubeletVersion,
		"kernel_version":            cl.Platform.KernelVersion,
		"kubeproxy_version":         cl.Platform.KubeletVersion,
	}
	for k, want := range checks {
		if got := labelVal(mc, "kube_node_info", k); got != want {
			t.Errorf("kube_node_info.%s = %q, want %q", k, got, want)
		}
	}
}

func TestNodeInfoPlatformDefaults(t *testing.T) {
	cl := coretest.Cluster() // empty Platform
	mc := captureTick(t, cl)
	if got := labelVal(mc, "kube_node_info", "os_image"); got != "Amazon Linux 2023" {
		t.Errorf("default os_image = %q, want Amazon Linux 2023 (aligned with resolvePlatform default)", got)
	}
}

// ── Task 5: kube_pod_status_phase has all 5 values ───────────────────────────────────

func TestPodStatusPhaseFive(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)
	phases := distinctVals(mc, "kube_pod_status_phase", "phase")
	for _, p := range []string{"Running", "Pending", "Failed", "Succeeded", "Unknown"} {
		if !phases[p] {
			t.Errorf("kube_pod_status_phase missing phase %q (got %v)", p, phases)
		}
	}
}

// ── Task 6: kube_pod_status_reason — 5 reasons + uid ─────────────────────────────────

func TestPodStatusReasonFiveWithUID(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)
	reasons := distinctVals(mc, "kube_pod_status_reason", "reason")
	for _, r := range []string{"Evicted", "NodeAffinity", "NodeLost", "Shutdown", "UnexpectedAdmissionError"} {
		if !reasons[r] {
			t.Errorf("kube_pod_status_reason missing reason %q (got %v)", r, reasons)
		}
	}
	if !hasKey(mc, "kube_pod_status_reason", "uid") {
		t.Error("kube_pod_status_reason must carry uid label")
	}
	for _, s := range mc.Find("kube_pod_status_reason") {
		if s.Value != 0 {
			t.Errorf("kube_pod_status_reason baseline value = %v, want 0", s.Value)
		}
	}
}

// ── namespace_workload_pod: raw input emitted, recording-rule output NOT double-pushed ──

func TestNamespaceWorkloadPodRawOnlyNoRecordingRule(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)
	// Raw KSM-style input series must still be emitted (the recording rule consumes it).
	if len(mc.Find("namespace_workload_pod")) == 0 {
		t.Error("namespace_workload_pod (raw input) must still be emitted")
	}
	// The recording-rule output series must NOT be emitted by the construct — the deployed
	// Grafana k8s-monitoring recording rule is its sole producer (a second copy with our
	// scrape labels breaks 1:1 vector matching in panels).
	if got := mc.Find("namespace_workload_pod:kube_pod_owner:relabel"); len(got) != 0 {
		t.Errorf("construct must NOT push recording-rule series namespace_workload_pod:kube_pod_owner:relabel, got %d series", len(got))
	}
}

// ── Task 7: StatefulSet family ───────────────────────────────────────────────────────

func TestStatefulSetFamily(t *testing.T) {
	cl := coretest.Cluster() // no stateful workload → synthetic <cluster>-state emitted
	mc := captureTick(t, cl)

	families := []string{
		"kube_statefulset_created",
		"kube_statefulset_metadata_generation",
		"kube_statefulset_replicas",
		"kube_statefulset_status_replicas",
		"kube_statefulset_status_replicas_available",
		"kube_statefulset_status_replicas_current",
		"kube_statefulset_status_replicas_ready",
		"kube_statefulset_status_replicas_updated",
		"kube_statefulset_status_observed_generation",
		"kube_statefulset_status_current_revision",
		"kube_statefulset_status_update_revision",
		"kube_statefulset_persistentvolumeclaim_retention_policy",
	}
	for _, f := range families {
		if !hasSeries(mc, f) {
			t.Errorf("statefulset family: %q not emitted", f)
		}
		if !hasKey(mc, f, "statefulset") || !hasKey(mc, f, "namespace") {
			t.Errorf("%q missing statefulset/namespace label keys", f)
		}
	}
	// revision label on the revision metrics.
	for _, f := range []string{"kube_statefulset_status_current_revision", "kube_statefulset_status_update_revision"} {
		if !hasKey(mc, f, "revision") {
			t.Errorf("%q missing revision label", f)
		}
	}
	// retention policy labels.
	for _, k := range []string{"when_deleted", "when_scaled"} {
		if !hasKey(mc, "kube_statefulset_persistentvolumeclaim_retention_policy", k) {
			t.Errorf("retention_policy missing label %q", k)
		}
	}
}

// ── Task 8: Init-containers ──────────────────────────────────────────────────────────

func TestInitContainerFamily(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	families := []string{
		"kube_pod_init_container_info",
		"kube_pod_init_container_status_ready",
		"kube_pod_init_container_status_running",
		"kube_pod_init_container_status_terminated",
		"kube_pod_init_container_status_terminated_reason",
		"kube_pod_init_container_status_restarts_total",
		"kube_pod_init_container_resource_limits",
		"kube_pod_init_container_resource_requests",
		"kube_pod_init_container_status_waiting",
	}
	for _, f := range families {
		if !hasSeries(mc, f) {
			t.Errorf("init-container family: %q not emitted", f)
			continue
		}
		for _, k := range []string{"container", "namespace", "pod", "uid"} {
			if !hasKey(mc, f, k) {
				t.Errorf("%q missing label key %q", f, k)
			}
		}
	}
	// container value is "init".
	if got := distinctVals(mc, "kube_pod_init_container_info", "container"); !got["init"] {
		t.Errorf("init container name = %v, want init", got)
	}
	// _info identity keys.
	for _, k := range []string{"container_id", "image", "image_id", "image_spec"} {
		if !hasKey(mc, "kube_pod_init_container_info", k) {
			t.Errorf("kube_pod_init_container_info missing %q", k)
		}
	}
	// terminated_reason = Completed.
	if got := distinctVals(mc, "kube_pod_init_container_status_terminated_reason", "reason"); !got["Completed"] {
		t.Errorf("init terminated reason = %v, want Completed", got)
	}
	// resource_* carry resource/unit/node.
	for _, k := range []string{"resource", "unit", "node"} {
		if !hasKey(mc, "kube_pod_init_container_resource_requests", k) {
			t.Errorf("init resource_requests missing %q", k)
		}
	}
}

// ── Task 9: Job + CronJob ────────────────────────────────────────────────────────────

func TestJobCronJobFamily(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	if !hasSeries(mc, "kube_cronjob_info") {
		t.Error("kube_cronjob_info not emitted")
	}
	if !hasKey(mc, "kube_cronjob_info", "cronjob") || !hasKey(mc, "kube_cronjob_info", "schedule") {
		t.Error("kube_cronjob_info missing cronjob/schedule keys")
	}
	for _, f := range []string{
		"kube_cronjob_created", "kube_cronjob_status_active", "kube_cronjob_spec_suspend",
		"kube_cronjob_next_schedule_time",
	} {
		if !hasSeries(mc, f) {
			t.Errorf("cronjob family: %q not emitted", f)
		}
	}
	for _, f := range []string{
		"kube_job_info", "kube_job_created", "kube_job_owner", "kube_job_spec_completions",
		"kube_job_status_succeeded", "kube_job_status_failed", "kube_job_complete",
	} {
		if !hasSeries(mc, f) {
			t.Errorf("job family: %q not emitted", f)
		}
		if !hasKey(mc, f, "namespace") || !hasKey(mc, f, "job_name") {
			t.Errorf("%q missing namespace/job_name keys", f)
		}
	}
	// owner joins to the CronJob.
	if got := distinctVals(mc, "kube_job_owner", "owner_kind"); !got["CronJob"] {
		t.Errorf("kube_job_owner owner_kind = %v, want CronJob", got)
	}
}

// TestCronJobScheduleIndexStable verifies the CronJob-spawned Job NAME is <cron>-<scheduleIndex>
// derived from the STABLE clusterCreatedUnix cadence (not wall-clock), so the job-series name does
// not churn every tick. Two ticks at different wall-clock times must yield identical job_name sets.
func TestCronJobScheduleIndexStable(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)

	names := func(now time.Time) map[string]bool {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, &coretest.LogCapture{}, nil)
		if err := c.Tick(context.Background(), now, w); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		return distinctVals(mc, "kube_job_info", "job_name")
	}
	a := names(time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC))
	b := names(time.Date(2026, 6, 13, 10, 5, 0, 0, time.UTC)) // 5 minutes later
	if len(a) == 0 {
		t.Fatal("no kube_job_info job_name series")
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("job_name set churned across ticks: %v vs %v (must be stable, not wall-clock)", a, b)
	}
	// Each job name must be <cron>-<scheduleIndex>: prefixed by the cluster CronJob name.
	cron := cl.Name + "-backup"
	for n := range a {
		if !strings.HasPrefix(n, cron+"-") {
			t.Errorf("job_name %q not <cron>-<scheduleIndex> form (cron=%q)", n, cron)
		}
	}
}

// TestJobPodSeries verifies the CronJob's Jobs spawn pod-level series: a pod named <job>-<5char>
// identified as Job-owned via kube_pod_owner.owner_kind=Job + kube_pod_info.created_by_kind=Job
// (verified via live reference 2026-06-16). The batch.kubernetes.io/* pod labels live on kube_pod_labels in
// real KSM (not emitted by this construct), so they are intentionally absent from kube_pod_info.
func TestJobPodSeries(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	// Find a Job-owned pod.
	var jobPod string
	for _, s := range mc.Find("kube_pod_owner") {
		if s.Labels["owner_kind"] == "Job" {
			jobPod = s.Labels["pod"]
			break
		}
	}
	if jobPod == "" {
		t.Fatal("no kube_pod_owner with owner_kind=Job (Job pods not emitted)")
	}
	// kube_pod_info for that pod carries created_by_kind=Job (NOT batch labels).
	var info map[string]string
	for _, s := range mc.Find("kube_pod_info") {
		if s.Labels["pod"] == jobPod {
			info = s.Labels
			break
		}
	}
	if info == nil {
		t.Fatalf("no kube_pod_info for job pod %q", jobPod)
	}
	if info["created_by_kind"] != "Job" {
		t.Errorf("job pod %q created_by_kind=%q want Job", jobPod, info["created_by_kind"])
	}
	if _, ok := info["label_batch_kubernetes_io_job_name"]; ok {
		t.Errorf("job pod %q must NOT carry batch labels on kube_pod_info (they live on kube_pod_labels): %v", jobPod, info)
	}
}

// ── Task 10: Pod lifecycle extras (init-container terminated already covers waiting) ──
//
// last_terminated_reason{reason=OOMKilled} is exercised by the failure-mode tests in
// k8scluster_test.go (TestOOMKill); at baseline it is correctly ABSENT, so no positive
// baseline assertion is made here.

// ── Task 11: kube_node_status_addresses ──────────────────────────────────────────────

func TestNodeStatusAddresses(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	series := mc.Find("kube_node_status_addresses")
	if len(series) == 0 {
		t.Fatal("kube_node_status_addresses: no series emitted")
	}
	// Verify all 3 type values are present.
	types := distinctVals(mc, "kube_node_status_addresses", "type")
	for _, want := range []string{"Hostname", "InternalDNS", "InternalIP"} {
		if !types[want] {
			t.Errorf("kube_node_status_addresses missing type=%q (got %v)", want, types)
		}
	}
	// All series must carry node, type, and address keys with non-empty values.
	for _, s := range series {
		for _, k := range []string{"node", "type", "address"} {
			if s.Labels[k] == "" {
				t.Errorf("kube_node_status_addresses series missing non-empty label %q", k)
			}
		}
		if s.Value != 1 {
			t.Errorf("kube_node_status_addresses value=%v, want 1", s.Value)
		}
	}
	// InternalIP series must carry a non-empty address (private IP).
	for _, s := range series {
		if s.Labels["type"] == "InternalIP" && s.Labels["address"] == "" {
			t.Error("kube_node_status_addresses{type=InternalIP} has empty address")
		}
	}
	// 3 series per node (one per type).
	nodeCount := len(distinctVals(mc, "kube_node_status_addresses", "node"))
	if nodeCount == 0 {
		t.Error("kube_node_status_addresses: no node label values")
	}
	if got := len(series); got != nodeCount*3 {
		t.Errorf("kube_node_status_addresses: got %d series, want %d (nodeCount=%d × 3)", got, nodeCount*3, nodeCount)
	}
}

// ── Task 12: kube_replicaset additional gauges ───────────────────────────────────────

func TestReplicaSetAdditionalGauges(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	for _, nm := range []string{
		"kube_replicaset_metadata_generation",
		"kube_replicaset_status_observed_generation",
		"kube_replicaset_status_fully_labeled_replicas",
		"kube_replicaset_status_terminating_replicas",
	} {
		if !hasSeries(mc, nm) {
			t.Errorf("%q not emitted", nm)
			continue
		}
		if !hasKey(mc, nm, "namespace") || !hasKey(mc, nm, "replicaset") {
			t.Errorf("%q missing namespace/replicaset label keys", nm)
		}
	}
	// terminating_replicas must be 0.
	for _, s := range mc.Find("kube_replicaset_status_terminating_replicas") {
		if s.Value != 0 {
			t.Errorf("kube_replicaset_status_terminating_replicas = %v, want 0", s.Value)
		}
	}
}

// ── Task 13: kube_daemonset additional gauges ────────────────────────────────────────

func TestDaemonSetAdditionalGauges(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	for _, nm := range []string{
		"kube_daemonset_status_number_unavailable",
		"kube_daemonset_status_observed_generation",
		"kube_daemonset_status_updated_number_scheduled",
	} {
		if !hasSeries(mc, nm) {
			t.Errorf("%q not emitted", nm)
			continue
		}
		if !hasKey(mc, nm, "daemonset") || !hasKey(mc, nm, "namespace") {
			t.Errorf("%q missing daemonset/namespace label keys", nm)
		}
	}
	// number_unavailable must be 0 at steady state.
	for _, s := range mc.Find("kube_daemonset_status_number_unavailable") {
		if s.Value != 0 {
			t.Errorf("kube_daemonset_status_number_unavailable = %v, want 0", s.Value)
		}
	}
	// observed_generation must be 1.
	for _, s := range mc.Find("kube_daemonset_status_observed_generation") {
		if s.Value != 1 {
			t.Errorf("kube_daemonset_status_observed_generation = %v, want 1", s.Value)
		}
	}
}

// ── Task 14: kube_pod_status_phase churn realism ─────────────────────────────────────
// At baseline (no incidents) the vast majority of pods are Running, but a small fraction
// (~1%) are Pending (mid-startup churn). We assert the ratio without being precise.

// ksmPhaseSum returns the sum of kube_pod_status_phase values for a given phase label.
func ksmPhaseSum(mc *coretest.MetricCapture, phase string) float64 {
	var s float64
	for _, series := range mc.Find("kube_pod_status_phase") {
		if series.Labels["phase"] == phase {
			s += series.Value
		}
	}
	return s
}

func TestPodPhaseChurnBaseline(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	runningSum := ksmPhaseSum(mc, "Running")
	pendingSum := ksmPhaseSum(mc, "Pending")
	total := runningSum + pendingSum

	if total == 0 {
		t.Fatal("no Running or Pending pods found")
	}
	// >97% must be Running (churn is tiny).
	runningPct := runningSum / total * 100
	if runningPct < 97 {
		t.Errorf("Running pod fraction = %.1f%%, want >97%% (churn model too aggressive)", runningPct)
	}
	// At least one Pending pod must exist (churn is non-zero).
	// NOTE: coretest.Cluster() has a small population; we cannot guarantee the churn lands here.
	// We only require it for the larger synthetic population tested in churn_test.go. So we
	// log but do not fail if pendingSum==0 for the tiny coretest cluster.
	t.Logf("baseline: Running=%.0f Pending=%.0f (%.1f%% running)", runningSum, pendingSum, runningPct)
}

// ── Task 15: kube_pod_status_ready ───────────────────────────────────────────────────

func TestPodStatusReadyEmitted(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	// Must be emitted.
	if len(mc.Find("kube_pod_status_ready")) == 0 {
		t.Fatal("kube_pod_status_ready: no series emitted")
	}
	// Must carry namespace, pod, uid, condition labels.
	for _, k := range []string{"namespace", "pod", "uid", "condition"} {
		if !hasKey(mc, "kube_pod_status_ready", k) {
			t.Errorf("kube_pod_status_ready: missing label key %q", k)
		}
	}
	// condition ∈ {true, false, unknown} all present.
	conds := distinctVals(mc, "kube_pod_status_ready", "condition")
	for _, c := range []string{"true", "false", "unknown"} {
		if !conds[c] {
			t.Errorf("kube_pod_status_ready: missing condition=%q (got %v)", c, conds)
		}
	}
}

func TestPodStatusReadyMostlyReady(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	// Count pods where condition=true is 1 (ready) vs condition=false is 1 (not ready).
	var readyCount, notReadyCount int
	// Group by pod: find if the {condition=true} series for that pod has value 1.
	podReady := map[string]bool{}
	for _, s := range mc.Find("kube_pod_status_ready") {
		pod := s.Labels["pod"]
		if pod == "" {
			continue
		}
		if s.Labels["condition"] == "true" {
			if s.Value == 1 {
				podReady[pod] = true
			} else {
				if _, exists := podReady[pod]; !exists {
					podReady[pod] = false
				}
			}
		}
	}
	for _, ready := range podReady {
		if ready {
			readyCount++
		} else {
			notReadyCount++
		}
	}
	total := readyCount + notReadyCount
	if total == 0 {
		t.Fatal("no pods found in kube_pod_status_ready")
	}
	readyPct := float64(readyCount) / float64(total) * 100
	if readyPct < 97 {
		t.Errorf("ready pods = %.1f%%, want >97%%", readyPct)
	}
	t.Logf("kube_pod_status_ready: %d/%d ready (%.1f%%)", readyCount, total, readyPct)
}

// ── Task 16: kube_pod_container_status_waiting + waiting_reason ──────────────────────

func TestPodContainerStatusWaitingEmitted(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	// Must be emitted.
	if len(mc.Find("kube_pod_container_status_waiting")) == 0 {
		t.Fatal("kube_pod_container_status_waiting: no series emitted")
	}
	// Must carry namespace, pod, container, uid.
	for _, k := range []string{"namespace", "pod", "container", "uid"} {
		if !hasKey(mc, "kube_pod_container_status_waiting", k) {
			t.Errorf("kube_pod_container_status_waiting: missing label key %q", k)
		}
	}
}

func TestPodContainerStatusWaitingReasonEmitted(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	// Must be emitted.
	if len(mc.Find("kube_pod_container_status_waiting_reason")) == 0 {
		t.Fatal("kube_pod_container_status_waiting_reason: no series emitted")
	}
	// Must carry reason label.
	if !hasKey(mc, "kube_pod_container_status_waiting_reason", "reason") {
		t.Error("kube_pod_container_status_waiting_reason: missing reason label")
	}
	// reason ONLY ∈ {ContainerCreating, PodInitializing} — no error reasons.
	allowedReasons := map[string]bool{
		"ContainerCreating": true,
		"PodInitializing":   true,
	}
	reasons := distinctVals(mc, "kube_pod_container_status_waiting_reason", "reason")
	for r := range reasons {
		if !allowedReasons[r] {
			t.Errorf("kube_pod_container_status_waiting_reason: unexpected reason %q (must be ContainerCreating or PodInitializing only)", r)
		}
	}
}

func TestPodContainerStatusWaitingNoErrorReasons(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	// Error reasons must NEVER appear at baseline.
	errorReasons := []string{"ImagePullBackOff", "CrashLoopBackOff", "ErrImagePull"}
	for _, s := range mc.Find("kube_pod_container_status_waiting_reason") {
		r := s.Labels["reason"]
		for _, bad := range errorReasons {
			if r == bad {
				t.Errorf("kube_pod_container_status_waiting_reason emitted error reason %q at baseline — must be incident-only", r)
			}
		}
	}
}

func TestPodContainerStatusWaitingReasonCardinality(t *testing.T) {
	cl := coretest.Cluster()
	mc := captureTick(t, cl)

	// Only ContainerCreating and PodInitializing reasons should be emitted (bounded cardinality).
	reasons := distinctVals(mc, "kube_pod_container_status_waiting_reason", "reason")
	// At most 2 distinct reason values.
	if len(reasons) > 2 {
		t.Errorf("kube_pod_container_status_waiting_reason: %d distinct reasons, want ≤2 (ContainerCreating,PodInitializing)", len(reasons))
	}
}

// ── Per-workload resource variation (size-class defaults + blueprint override) ─────────

// mixWorkloadCluster returns a cluster with several app workloads whose deploy names land in
// DIFFERENT CPU size classes (verified against the FNV mapping), so the request/limit values are a
// realistic MIX rather than a uniform 0.25/1.0. One workload ("svc-a") carries a blueprint override.
func mixWorkloadCluster(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	// test-api → class 0 (small 0.1/0.25); coredns/metrics-server substrate add classes 1/2; add an
	// xlarge (svc-a → class 3, 1.0/2.0) with a pinned override to prove both paths.
	add := func(name string, res *fixture.WorkloadResources) {
		wl := fixture.Workload{Name: name, Namespace: name, Replicas: 1, Resources: res}
		wl.PodNames = []string{fixture.PodName("test", name, 0)}
		wl.NodeIdx = []int{0}
		cl.Workloads = append(cl.Workloads, wl)
	}
	add("coredns-app", nil) // distinct from kube-system coredns; lands its own class
	add("metrics-server-app", nil)
	add("svc-a", &fixture.WorkloadResources{CPURequest: 0.42, CPULimit: 0.84})
	return cl
}

// cpuReqByContainer returns container→cpu-request value for kube_pod_container_resource_requests.
func cpuReqByContainer(mc *coretest.MetricCapture, metric string) map[string]float64 {
	out := map[string]float64{}
	for _, s := range mc.Find(metric) {
		if s.Labels["resource"] != "cpu" {
			continue
		}
		out[s.Labels["container"]] = s.Value
	}
	return out
}

func TestResourceRequestsAreAMix(t *testing.T) {
	cl := mixWorkloadCluster(t)
	mc := captureTick(t, cl)

	reqs := cpuReqByContainer(mc, "kube_pod_container_resource_requests")
	if len(reqs) == 0 {
		t.Fatal("no kube_pod_container_resource_requests{resource=cpu} emitted")
	}
	t.Logf("cpu requests by container (mix): %v", reqs)
	// The CPU requests must NOT all be the old uniform 0.25.
	distinct := map[float64]bool{}
	for _, v := range reqs {
		distinct[v] = true
	}
	if len(distinct) < 2 {
		t.Errorf("cpu requests are uniform (%v) — expected a mix across workloads", distinct)
	}
	// The blueprint override on svc-a wins (0.42), not a size-class value.
	if got := reqs["svc-a"]; got != 0.42 {
		t.Errorf("svc-a cpu request = %g, want override 0.42", got)
	}
	if got := reqs["test-api"]; got != 0.1 { // class 0 small
		t.Errorf("test-api cpu request = %g, want size-class 0.1", got)
	}
	// Memory requests/limits still emitted (resource=memory present).
	memReq := false
	for _, s := range mc.Find("kube_pod_container_resource_requests") {
		if s.Labels["resource"] == "memory" {
			memReq = true
		}
	}
	if !memReq {
		t.Error("kube_pod_container_resource_requests{resource=memory} not emitted")
	}
}

func TestResourceLimitsOverrideWins(t *testing.T) {
	cl := mixWorkloadCluster(t)
	mc := captureTick(t, cl)
	lims := cpuReqByContainer(mc, "kube_pod_container_resource_limits")
	if got := lims["svc-a"]; got != 0.84 {
		t.Errorf("svc-a cpu limit = %g, want override 0.84", got)
	}
	// test-api default limit is size-class small = 0.25 (NOT the old uniform 1.0).
	if got := lims["test-api"]; got != 0.25 {
		t.Errorf("test-api cpu limit = %g, want size-class 0.25", got)
	}
}

// ── SubstrateWorkloads: addon/baseline pods join kube_pod_* via resolver-minted identity ──
//
// cl.SubstrateWorkloads carry addon/baseline pods (coredns, metrics-server, cert-manager, karpenter,
// …) with resolver-minted PodNames + real Container. The pod-shape emitters must treat them as REAL
// workloads — using PodNames as the `pod` label and Container as the `container` label — so an addon
// construct stamping the SAME PodNames joins the kube_pod_* series. They live in a SEPARATE slice so
// they stay invisible to node-count summers.

// substrateTestCluster returns the coretest cluster with cl.SubstrateWorkloads populated from the
// addon catalog (baseline + cert_manager + karpenter), PodNames minted via WorkloadPodNames.
func substrateTestCluster(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	if cl.Seed == "" {
		cl.Seed = cl.Name
	}
	subs := append(fixture.BaselineWorkloads(), fixture.AddonWorkloads("cert_manager")...)
	subs = append(subs, fixture.AddonWorkloads("karpenter")...)
	for i := range subs {
		subs[i].PodNames = fixture.WorkloadPodNames(cl.Seed, subs[i], nil)
	}
	cl.SubstrateWorkloads = subs
	return cl
}

// TestSubstrateWorkloadsPodNamesAndContainer asserts the migrated addon/baseline pods surface on
// kube_pod_info with the resolver-minted PodNames (not the synthPodName form) and the catalog's real
// container name, and that adding SubstrateWorkloads does NOT change the node count.
func TestSubstrateWorkloadsPodNamesAndContainer(t *testing.T) {
	cl := substrateTestCluster(t)
	mc := captureTick(t, cl)

	// cert-manager controller workload: its kube_pod_info pods' `pod` label == the minted PodNames.
	var cm fixture.Workload
	for _, w := range cl.SubstrateWorkloads {
		if w.Name == "cert-manager" {
			cm = w
		}
	}
	if len(cm.PodNames) != 2 {
		t.Fatalf("cert-manager catalog Replicas changed: got %d PodNames, want 2", len(cm.PodNames))
	}
	wantPods := map[string]bool{}
	for _, p := range cm.PodNames {
		wantPods[p] = true
	}
	gotPods := map[string]bool{}
	for _, s := range mc.Find("kube_pod_info") {
		if s.Labels["namespace"] == "cert-manager" {
			gotPods[s.Labels["pod"]] = true
		}
	}
	for p := range wantPods {
		if !gotPods[p] {
			t.Errorf("cert-manager kube_pod_info missing minted pod %q (got %v)", p, gotPods)
		}
	}

	// container labels: cert-manager-webhook pod → container="cert-manager-webhook";
	// karpenter pod (deploy "karpenter") → container="controller" (differs from deploy name).
	containerForDeploy := func(deploy string) map[string]bool {
		out := map[string]bool{}
		// resolve the workload's minted pod names
		var w fixture.Workload
		for _, s := range cl.SubstrateWorkloads {
			if s.Name == deploy {
				w = s
			}
		}
		podSet := map[string]bool{}
		for _, p := range w.PodNames {
			podSet[p] = true
		}
		for _, s := range mc.Find("kube_pod_container_info") {
			if podSet[s.Labels["pod"]] {
				out[s.Labels["container"]] = true
			}
		}
		return out
	}
	if c := containerForDeploy("cert-manager-webhook"); !c["cert-manager-webhook"] {
		t.Errorf("cert-manager-webhook container = %v, want cert-manager-webhook", c)
	}
	if c := containerForDeploy("karpenter"); !c["controller"] {
		t.Errorf("karpenter container = %v, want controller", c)
	}

	// The 3 cert-manager kube_deployment_* meta families are still emitted (cert-manager,
	// cert-manager-webhook, cert-manager-cainjector are all Deployments).
	cmDeploys := distinctVals(mc, "kube_deployment_spec_replicas", "deployment")
	for _, d := range []string{"cert-manager", "cert-manager-webhook", "cert-manager-cainjector"} {
		if !cmDeploys[d] {
			t.Errorf("kube_deployment_spec_replicas missing cert-manager deployment %q (got %v)", d, cmDeploys)
		}
	}
	// cert-manager controller deployment carries 2 replicas (catalog value, matches real).
	for _, s := range mc.Find("kube_deployment_spec_replicas") {
		if s.Labels["deployment"] == "cert-manager" && s.Value != 2 {
			t.Errorf("cert-manager kube_deployment_spec_replicas = %v, want 2 (catalog)", s.Value)
		}
	}

	// NODE COUNT is unchanged by adding SubstrateWorkloads (they are not node-count summed).
	withoutSubs := captureTick(t, coretest.Cluster())
	wantNodes := len(distinctVals(withoutSubs, "kube_node_info", "node"))
	gotNodes := len(distinctVals(mc, "kube_node_info", "node"))
	if gotNodes != wantNodes {
		t.Errorf("node count changed by SubstrateWorkloads: got %d, want %d", gotNodes, wantNodes)
	}
}

// TestSubstrateWorkloadsUseMintedPodNames guards that migrated baseline pods (coredns) surface with
// the resolver-minted seeded PodName form, not name-only synthesis.
func TestSubstrateWorkloadsUseMintedPodNames(t *testing.T) {
	cl := substrateTestCluster(t)
	mc := captureTick(t, cl)

	var coredns fixture.Workload
	for _, w := range cl.SubstrateWorkloads {
		if w.Name == "coredns" {
			coredns = w
		}
	}
	if len(coredns.PodNames) == 0 {
		t.Fatal("coredns has no minted PodNames")
	}
	got := map[string]bool{}
	for _, s := range mc.Find("kube_pod_info") {
		if s.Labels["namespace"] == "kube-system" && strings.HasPrefix(s.Labels["pod"], "coredns-") {
			got[s.Labels["pod"]] = true
		}
	}
	for _, p := range coredns.PodNames {
		if !got[p] {
			t.Errorf("coredns kube_pod_info missing minted pod %q (got %v)", p, got)
		}
	}
}

// TestControllerKindRouting verifies a workload is routed to exactly ONE KSM controller family by
// its Controller field, with the matching kube_pod_owner.owner_kind + namespace_workload_pod
// workload_type, and that HPA/PVC are opt-in (sourced from live reference kubectl data 2026-06-16).
func TestControllerKindRouting(t *testing.T) {
	cl := coretest.Cluster()
	nodeN := len(cl.Nodes)
	dsPods := make([]string, nodeN)
	for i := range dsPods {
		dsPods[i] = fixture.DaemonSetPodName(cl.Seed, "ds-agent", cl.Nodes[i].Hostname)
	}
	cl.Workloads = []fixture.Workload{
		{Name: "dep-app", Namespace: "apps", Replicas: 2, Controller: "deployment", HasHPA: true,
			PodNames: []string{fixture.PodName(cl.Seed, "dep-app", 0), fixture.PodName(cl.Seed, "dep-app", 1)}, NodeIdx: []int{0, 0}},
		{Name: "sts-db", Namespace: "data", Replicas: 2, Controller: "statefulset", VolumeClaims: []string{"datadir"},
			PodNames: []string{fixture.StatefulSetPodName("sts-db", 0), fixture.StatefulSetPodName("sts-db", 1)}, NodeIdx: []int{0, 0}},
		{Name: "ds-agent", Namespace: "obs", Replicas: nodeN, Controller: "daemonset",
			PodNames: dsPods, NodeIdx: make([]int, nodeN)},
	}
	mc := captureTick(t, cl)

	labelHas := func(metric, key, val string) bool {
		for _, s := range mc.Find(metric) {
			if s.Labels[key] == val {
				return true
			}
		}
		return false
	}
	ownerKindFor := func(podPrefix string) string {
		for _, s := range mc.Find("kube_pod_owner") {
			if p := s.Labels["pod"]; len(p) >= len(podPrefix) && p[:len(podPrefix)] == podPrefix {
				return s.Labels["owner_kind"]
			}
		}
		return ""
	}

	// Each workload in exactly one controller meta family.
	if !labelHas("kube_deployment_spec_replicas", "deployment", "dep-app") {
		t.Error("dep-app missing kube_deployment_spec_replicas")
	}
	if labelHas("kube_deployment_spec_replicas", "deployment", "sts-db") || labelHas("kube_deployment_spec_replicas", "deployment", "ds-agent") {
		t.Error("sts-db/ds-agent must NOT emit kube_deployment_* (double-emit)")
	}
	if !labelHas("kube_statefulset_replicas", "statefulset", "sts-db") {
		t.Error("sts-db missing kube_statefulset_replicas")
	}
	if !labelHas("kube_daemonset_status_desired_number_scheduled", "daemonset", "ds-agent") {
		t.Error("ds-agent missing kube_daemonset_*")
	}
	// Pod owner kinds.
	if got := ownerKindFor("dep-app-"); got != "ReplicaSet" {
		t.Errorf("dep-app pod owner_kind = %q, want ReplicaSet", got)
	}
	if got := ownerKindFor("sts-db-"); got != "StatefulSet" {
		t.Errorf("sts-db pod owner_kind = %q, want StatefulSet", got)
	}
	if got := ownerKindFor("ds-agent-"); got != "DaemonSet" {
		t.Errorf("ds-agent pod owner_kind = %q, want DaemonSet", got)
	}
	// workload_type on namespace_workload_pod.
	if !labelHas("namespace_workload_pod", "workload_type", "statefulset") || !labelHas("namespace_workload_pod", "workload_type", "daemonset") {
		t.Error("namespace_workload_pod missing statefulset/daemonset workload_type")
	}
	// HPA opt-in: dep-app (HasHPA) yes; sts-db/ds-agent no.
	if !labelHas("kube_horizontalpodautoscaler_spec_max_replicas", "horizontalpodautoscaler", "dep-app") {
		t.Error("dep-app (HasHPA) missing HPA family")
	}
	if labelHas("kube_horizontalpodautoscaler_spec_max_replicas", "horizontalpodautoscaler", "sts-db") {
		t.Error("sts-db (no HasHPA) must not emit HPA")
	}
	// PVC: sts-db per-ordinal naming datadir-sts-db-0.
	if !labelHas("kube_persistentvolumeclaim_info", "persistentvolumeclaim", "datadir-sts-db-0") {
		t.Error("sts-db missing per-ordinal PVC datadir-sts-db-0")
	}
}
