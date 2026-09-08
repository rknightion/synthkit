// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

const (
	p3KubeletJob      = "integrations/kubernetes/kubelet"
	p3NodeExporterJob = "integrations/node_exporter"
)

func captureP3Envelope(t *testing.T) *coretest.MetricCapture {
	t.Helper()
	cl := coretest.Cluster()
	cl.K8sMonitoring.Alloy = false
	c := buildConstructWithConfig(t, &k8scluster.Config{OTelCollectorProm: true}, cl)
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatal(err)
	}
	return mc
}

func captureNonP3Envelope(t *testing.T) *coretest.MetricCapture {
	t.Helper()
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	tick(t, c, mc, &coretest.LogCapture{})
	return mc
}

func cloneP3Series(series []promrw.Series) []promrw.Series {
	out := make([]promrw.Series, len(series))
	for i, s := range series {
		out[i] = s
		out[i].Labels = make(map[string]string, len(s.Labels))
		for k, v := range s.Labels {
			out[i].Labels[k] = v
		}
	}
	return out
}

func seriesForJob(series []promrw.Series, name, job string) []promrw.Series {
	var out []promrw.Series
	for _, s := range series {
		if s.Name == name && s.Labels["job"] == job {
			out = append(out, s)
		}
	}
	return out
}

func firstSeries(series []promrw.Series, name string) *promrw.Series {
	for i := range series {
		if series[i].Name == name {
			return &series[i]
		}
	}
	return nil
}

func requireEveryLabel(series []promrw.Series, name, key string) error {
	seen := false
	for _, s := range series {
		if s.Name != name {
			continue
		}
		seen = true
		if s.Labels[key] == "" {
			return fmt.Errorf("%s missing non-empty %s", name, key)
		}
	}
	if !seen {
		return fmt.Errorf("%s has no series", name)
	}
	return nil
}

func requireAnyLabel(series []promrw.Series, name, key string) error {
	seen := false
	for _, s := range series {
		if s.Name == name && s.Labels[key] != "" {
			seen = true
		}
	}
	if !seen {
		return fmt.Errorf("%s missing non-empty %s", name, key)
	}
	return nil
}

func rejectLabel(series []promrw.Series, names []string, key string) error {
	for _, name := range names {
		for _, s := range series {
			if s.Name == name {
				if _, ok := s.Labels[key]; ok {
					return fmt.Errorf("%s unexpectedly has %s", name, key)
				}
			}
		}
	}
	return nil
}

func validateP3Envelope(series []promrw.Series) error {
	for _, name := range []string{"process_cpu_seconds_total", "process_resident_memory_bytes"} {
		if len(seriesForJob(series, name, p3KubeletJob)) == 0 || len(seriesForJob(series, name, p3NodeExporterJob)) == 0 {
			return fmt.Errorf("%s does not carry both kubelet and node-exporter envelopes", name)
		}
		for _, kubelet := range seriesForJob(series, name, p3KubeletJob) {
			if _, ok := kubelet.Labels["namespace"]; ok {
				return fmt.Errorf("%s kubelet envelope inherited namespace", name)
			}
		}
	}
	for _, check := range []struct{ name, key string }{
		{"kube_node_info", "pod_cidr"},
		{"machine_memory_bytes", "boot_id"},
	} {
		if err := requireEveryLabel(series, check.name, check.key); err != nil {
			return err
		}
	}
	for _, name := range []string{"node_filesystem_device_error", "node_filesystem_readonly"} {
		if err := requireAnyLabel(series, name, "device_error"); err != nil {
			return err
		}
	}
	failedMounts := map[string]map[string]bool{}
	for _, s := range series {
		if s.Name != "node_filesystem_device_error" && s.Name != "node_filesystem_readonly" {
			continue
		}
		if s.Labels["device_error"] == "" {
			continue
		}
		mount := s.Labels["device"] + "|" + s.Labels["fstype"] + "|" + s.Labels["mountpoint"]
		if failedMounts[mount] == nil {
			failedMounts[mount] = map[string]bool{}
		}
		failedMounts[mount][s.Name] = true
		if s.Name == "node_filesystem_device_error" && s.Value != 1 {
			return fmt.Errorf("failed filesystem device_error=%v, want 1", s.Value)
		}
		if s.Name == "node_filesystem_readonly" && s.Value != 0 {
			return fmt.Errorf("failed filesystem readonly=%v, want rw status 0", s.Value)
		}
	}
	for mount, statuses := range failedMounts {
		if !statuses["node_filesystem_device_error"] || !statuses["node_filesystem_readonly"] {
			return fmt.Errorf("failed filesystem mount %s lacks one status family", mount)
		}
		for _, capacity := range []string{
			"node_filesystem_avail_bytes", "node_filesystem_files", "node_filesystem_files_free",
			"node_filesystem_free_bytes", "node_filesystem_purgeable_bytes", "node_filesystem_size_bytes",
		} {
			for _, s := range series {
				if s.Name == capacity && s.Labels["device"]+"|"+s.Labels["fstype"]+"|"+s.Labels["mountpoint"] == mount {
					return fmt.Errorf("failed filesystem mount %s incorrectly has %s", mount, capacity)
				}
			}
		}
	}
	if len(failedMounts) == 0 {
		return fmt.Errorf("no failed filesystem status mount carries device_error")
	}
	if err := rejectLabel(series, []string{
		"node_filesystem_avail_bytes", "node_filesystem_files", "node_filesystem_files_free",
		"node_filesystem_free_bytes", "node_filesystem_purgeable_bytes", "node_filesystem_size_bytes",
	}, "device_error"); err != nil {
		return err
	}
	if err := rejectLabel(series, []string{
		"container_cpu_usage_seconds_total", "container_memory_working_set_bytes", "container_memory_rss",
	}, "boot_id"); err != nil {
		return err
	}
	if err := rejectLabel(series, []string{"target_info"}, "otel_scope_name"); err != nil {
		return err
	}
	return rejectLabel(series, []string{"target_info"}, "otel_scope_version")
}

func validateNonP3Envelope(series []promrw.Series) error {
	for _, check := range []struct{ name, key string }{
		{"kube_node_info", "pod_cidr"},
		{"machine_memory_bytes", "boot_id"},
		{"node_filesystem_device_error", "device_error"},
		{"node_filesystem_readonly", "device_error"},
	} {
		if err := rejectLabel(series, []string{check.name}, check.key); err != nil {
			return err
		}
	}
	return nil
}

func rejectKubeletProcessNamespace(series []promrw.Series) error {
	for _, name := range []string{"process_cpu_seconds_total", "process_resident_memory_bytes"} {
		for _, s := range seriesForJob(series, name, p3KubeletJob) {
			if _, ok := s.Labels["namespace"]; ok {
				return fmt.Errorf("%s kubelet envelope inherited namespace", name)
			}
		}
	}
	return nil
}

func TestCollectorPromP3ClosesScopedEnvelopeOmissions(t *testing.T) {
	if err := validateP3Envelope(captureP3Envelope(t).All()); err != nil {
		t.Fatal(err)
	}
	if err := validateNonP3Envelope(captureNonP3Envelope(t).All()); err != nil {
		t.Fatal(err)
	}
}

func TestCollectorPromP3EnvelopeGuardsRejectLeakage(t *testing.T) {
	series := captureP3Envelope(t).All()
	mutations := []struct {
		name   string
		metric string
		key    string
		guard  func([]promrw.Series) error
	}{
		{"filesystem sibling", "node_filesystem_avail_bytes", "device_error", func(series []promrw.Series) error {
			return rejectLabel(series, []string{"node_filesystem_avail_bytes"}, "device_error")
		}},
		{"container sibling", "container_memory_working_set_bytes", "boot_id", func(series []promrw.Series) error {
			return rejectLabel(series, []string{"container_memory_working_set_bytes"}, "boot_id")
		}},
		{"scope-free target_info", "target_info", "otel_scope_name", func(series []promrw.Series) error {
			return rejectLabel(series, []string{"target_info"}, "otel_scope_name")
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			broken := cloneP3Series(series)
			candidate := firstSeries(broken, mutation.metric)
			if mutation.metric == "process_cpu_seconds_total" {
				candidate = firstSeries(seriesForJob(broken, mutation.metric, p3KubeletJob), mutation.metric)
			}
			if candidate == nil {
				t.Fatalf("missing mutation target %s", mutation.metric)
			}
			candidate.Labels[mutation.key] = "deliberately-broken"
			if err := mutation.guard(broken); err == nil || !strings.Contains(err.Error(), mutation.key) {
				t.Fatalf("guard accepted %s mutation: %v", mutation.name, err)
			} else {
				t.Logf("guard rejected deliberate %s mutation: %v", mutation.name, err)
			}
		})
	}
	brokenProcess := cloneP3Series(series)
	process := firstSeries(brokenProcess, "process_cpu_seconds_total")
	if process == nil {
		t.Fatal("missing node-exporter process_cpu_seconds_total mutation target")
	}
	process.Labels["job"] = p3KubeletJob
	process.Labels["namespace"] = "deliberately-broken"
	if err := rejectKubeletProcessNamespace(brokenProcess); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("kubelet envelope guard accepted namespace mutation: %v", err)
	} else {
		t.Logf("guard rejected deliberate kubelet process namespace mutation: %v", err)
	}

	brokenNonP3 := cloneP3Series(captureNonP3Envelope(t).All())
	candidate := firstSeries(brokenNonP3, "kube_node_info")
	if candidate == nil {
		t.Fatal("missing non-P3 kube_node_info mutation target")
	}
	candidate.Labels["pod_cidr"] = "10.244.0.0/24"
	if err := validateNonP3Envelope(brokenNonP3); err == nil || !strings.Contains(err.Error(), "pod_cidr") {
		t.Fatalf("non-P3 guard accepted pod_cidr mutation: %v", err)
	} else {
		t.Logf("guard rejected deliberate non-P3 pod_cidr mutation: %v", err)
	}
}
