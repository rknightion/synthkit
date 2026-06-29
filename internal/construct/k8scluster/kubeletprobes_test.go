// SPDX-License-Identifier: AGPL-3.0-only

// kubeletprobes_test.go — TDD tests for KubeletProbes gating in emitKubelet.
// prober_probe_total / prober_probe_duration_seconds_* must be ABSENT by default
// and present when KubeletProbes=true.
package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
)

// proberNames are the metric families emitted by emitProberMetrics.
var proberNames = []string{
	"prober_probe_total",
	"prober_probe_duration_seconds_bucket",
	"prober_probe_duration_seconds_count",
	"prober_probe_duration_seconds_sum",
}

func TestKubeletProbesDefaultOff(t *testing.T) {
	cl := coretest.Cluster()
	// coretest.Cluster() does not set KubeletProbes — default false.
	if cl.K8sMonitoring.ControlPlane.KubeletProbes {
		t.Skip("coretest.Cluster() has KubeletProbes=true — skip default-off test")
	}

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, nm := range proberNames {
		if hasSeries(mc, nm) {
			t.Errorf("%s: must be ABSENT when KubeletProbes=false (default off), but was emitted", nm)
		}
	}
}

func TestKubeletProbesOn(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane.KubeletProbes = true

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, nm := range proberNames {
		if !hasSeries(mc, nm) {
			t.Errorf("%s: must be PRESENT when KubeletProbes=true, but was not emitted", nm)
		}
	}
}

func TestKubeletProbesOffDoesNotAffectOtherKubeletMetrics(t *testing.T) {
	// Ensure gating prober metrics does not accidentally suppress other kubelet metrics.
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane.KubeletProbes = false

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// Core kubelet metrics must still be present.
	for _, nm := range []string{
		"kubelet_running_pods",
		"kubelet_running_containers",
		"kubernetes_build_info",
	} {
		if !hasSeries(mc, nm) {
			t.Errorf("%s: must be PRESENT even when KubeletProbes=false", nm)
		}
	}
}
