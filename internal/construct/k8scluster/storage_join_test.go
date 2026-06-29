// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// findOne returns the first series named nm, failing if none captured.
func findOne(t *testing.T, mc *coretest.MetricCapture, nm string) promrw.Series {
	t.Helper()
	ss := mc.Find(nm)
	if len(ss) == 0 {
		t.Fatalf("%s: not emitted", nm)
	}
	return ss[0]
}

// TestPVCStorageJoin proves the cross-construct PVC/PV identity join: the SAME volume's
// persistentvolumeclaim value is identical on the KSM PVC family, kubelet_volume_stats, and
// OpenCost pod_pvc_allocation; the PV/volumename value is identical across the KSM PV-phase
// series, kube_persistentvolumeclaim_info.volumename, and pv_hourly_cost. All three constructs
// read the single shared volumesForCluster identity source.
func TestPVCStorageJoin(t *testing.T) {
	cl := coretest.Cluster() // OpenCost on; one workload → one PVC
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// ── The persistentvolumeclaim JOIN ───────────────────────────────────────────────
	pvcInfo := findOne(t, mc, "kube_persistentvolumeclaim_info")
	pvcName := pvcInfo.Labels["persistentvolumeclaim"]
	if pvcName == "" {
		t.Fatal("kube_persistentvolumeclaim_info: missing persistentvolumeclaim label")
	}

	kubeletCap := findOne(t, mc, "kubelet_volume_stats_capacity_bytes")
	if got := kubeletCap.Labels["persistentvolumeclaim"]; got != pvcName {
		t.Errorf("PVC join broken: kubelet_volume_stats_capacity_bytes pvc=%q != kube_persistentvolumeclaim_info pvc=%q", got, pvcName)
	}

	podPVC := findOne(t, mc, "pod_pvc_allocation")
	if got := podPVC.Labels["persistentvolumeclaim"]; got != pvcName {
		t.Errorf("PVC join broken: pod_pvc_allocation pvc=%q != kube_persistentvolumeclaim_info pvc=%q", got, pvcName)
	}

	// ── The persistentvolume / volumename JOIN ───────────────────────────────────────
	pvName := pvcInfo.Labels["volumename"]
	if pvName == "" {
		t.Fatal("kube_persistentvolumeclaim_info: missing volumename label")
	}

	pvPhase := findOne(t, mc, "kube_persistentvolume_status_phase")
	if got := pvPhase.Labels["persistentvolume"]; got != pvName {
		t.Errorf("PV join broken: kube_persistentvolume_status_phase pv=%q != info.volumename=%q", got, pvName)
	}

	pvCost := findOne(t, mc, "pv_hourly_cost")
	if got := pvCost.Labels["persistentvolume"]; got != pvName {
		t.Errorf("PV join broken: pv_hourly_cost.persistentvolume=%q != info.volumename=%q", got, pvName)
	}
	if got := pvCost.Labels["volumename"]; got != pvName {
		t.Errorf("PV join broken: pv_hourly_cost.volumename=%q != info.volumename=%q", got, pvName)
	}
	// pod_pvc_allocation also carries the PV name.
	if got := podPVC.Labels["persistentvolume"]; got != pvName {
		t.Errorf("PV join broken: pod_pvc_allocation.persistentvolume=%q != info.volumename=%q", got, pvName)
	}

	// pv_hourly_cost.provider_id must be the backing EBS volume id (vol-…), not the PV name.
	if got := pvCost.Labels["provider_id"]; got == "" || got[:4] != "vol-" {
		t.Errorf("pv_hourly_cost.provider_id=%q, want vol-… EBS id", got)
	}
}

// TestPVCInfoLabelSet asserts kube_persistentvolumeclaim_info carries the audit-sourced labels.
func TestPVCInfoLabelSet(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	info := findOne(t, mc, "kube_persistentvolumeclaim_info")
	if got := info.Labels["storageclass"]; got != "gp3" {
		t.Errorf("storageclass=%q, want gp3", got)
	}
	if got := info.Labels["volumemode"]; got != "Filesystem" {
		t.Errorf("volumemode=%q, want Filesystem", got)
	}

	am := findOne(t, mc, "kube_persistentvolumeclaim_access_mode")
	if got := am.Labels["access_mode"]; got != "ReadWriteOnce" {
		t.Errorf("access_mode=%q, want ReadWriteOnce", got)
	}

	// requests_storage_bytes must equal the kubelet capacity (lanes agree on size).
	req := findOne(t, mc, "kube_persistentvolumeclaim_resource_requests_storage_bytes")
	kubeletCap := findOne(t, mc, "kubelet_volume_stats_capacity_bytes")
	if req.Value != kubeletCap.Value {
		t.Errorf("requests_storage_bytes=%.0f != kubelet capacity=%.0f", req.Value, kubeletCap.Value)
	}
}

// TestPVCPhaseFanout asserts the PVC + PV status-phase fan-outs are present with value 1 on Bound.
func TestPVCPhaseFanout(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	wantPVC := map[string]float64{"Bound": 1, "Pending": 0, "Lost": 0}
	gotPVC := map[string]float64{}
	for _, s := range mc.Find("kube_persistentvolumeclaim_status_phase") {
		gotPVC[s.Labels["phase"]] = s.Value
	}
	for phase, want := range wantPVC {
		if gotPVC[phase] != want {
			t.Errorf("kube_persistentvolumeclaim_status_phase{phase=%s}=%.0f, want %.0f", phase, gotPVC[phase], want)
		}
	}

	wantPV := map[string]float64{"Available": 0, "Bound": 1, "Released": 0, "Failed": 0, "Pending": 0}
	gotPV := map[string]float64{}
	for _, s := range mc.Find("kube_persistentvolume_status_phase") {
		gotPV[s.Labels["phase"]] = s.Value
	}
	for phase, want := range wantPV {
		if _, ok := gotPV[phase]; !ok {
			t.Errorf("kube_persistentvolume_status_phase{phase=%s} missing", phase)
		}
		if gotPV[phase] != want {
			t.Errorf("kube_persistentvolume_status_phase{phase=%s}=%.0f, want %.0f", phase, gotPV[phase], want)
		}
	}
}

// TestPodVolumesPVCInfoUID asserts kube_pod_spec_volumes_persistentvolumeclaims_info carries the
// POD uid (same derivation as the other KSM pod series), not the PVC uid.
func TestPodVolumesPVCInfoUID(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	spec := findOne(t, mc, "kube_pod_spec_volumes_persistentvolumeclaims_info")
	pod := spec.Labels["pod"]
	if pod == "" {
		t.Fatal("kube_pod_spec_volumes_persistentvolumeclaims_info: missing pod label")
	}
	// The pod must be a real KSM pod (kube_pod_info exists for it with the same uid).
	var podUID string
	for _, s := range mc.Find("kube_pod_info") {
		if s.Labels["pod"] == pod {
			podUID = s.Labels["uid"]
			break
		}
	}
	if podUID == "" {
		t.Fatalf("no kube_pod_info for pod %q referenced by volumes_pvc_info", pod)
	}
	if got := spec.Labels["uid"]; got != podUID {
		t.Errorf("kube_pod_spec_volumes_persistentvolumeclaims_info.uid=%q, want POD uid %q", got, podUID)
	}
}
