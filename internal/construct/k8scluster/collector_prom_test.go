// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCollectorPromEnvelopeAndExclusivity(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Alloy = true
	if _, err := k8scluster.New(&k8scluster.Config{OTelCollectorProm: true}, &fixture.Set{Cluster: cl}); err == nil {
		t.Fatal("P3 plus explicit Alloy emission must fail")
	}
	cl.K8sMonitoring.Alloy = false
	if cl.K8sMonitoring.Features == nil {
		cl.K8sMonitoring.Features = map[string]bool{}
	}
	cl.K8sMonitoring.Features["pod_logs"] = true
	cl.K8sMonitoring.PodLogsMethod = "loki"
	if _, err := k8scluster.New(&k8scluster.Config{OTelCollectorProm: true}, &fixture.Set{Cluster: cl}); err == nil {
		t.Fatal("P3 must reject Loki pod logs")
	}
	cl.K8sMonitoring.PodLogsMethod = "opentelemetry"
	c := buildConstructWithConfig(t, &k8scluster.Config{OTelCollectorProm: true}, cl)
	mc, lc, oc := &coretest.MetricCapture{}, &coretest.LogCapture{}, &otlpLogCapture{}
	w := coretest.World(mc, lc, nil)
	w.OTLPLogs = oc
	if err := c.Tick(context.Background(), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatal(err)
	}
	if len(mc.All()) == 0 || len(oc.Resources) == 0 {
		t.Fatal("P3 did not emit populated metric/log envelopes")
	}
	var corpus struct {
		Inventory struct {
			Metrics []struct {
				Name      string `json:"name"`
				Producers []struct {
					Name string `json:"name"`
				} `json:"producers"`
				Labels []struct {
					Key string `json:"key"`
				} `json:"labels"`
			} `json:"metrics"`
		} `json:"inventory"`
	}
	b, err := os.ReadFile("../../../reality-corpus/k8s/k3d-lab-otel-collector-prom.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &corpus); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]map[string]bool{}
	producerJobs := map[string]map[string]bool{}
	for _, metric := range corpus.Inventory.Metrics {
		allowed[metric.Name] = map[string]bool{}
		producerJobs[metric.Name] = map[string]bool{}
		for _, producer := range metric.Producers {
			if strings.HasPrefix(producer.Name, "promrw/") {
				producerJobs[metric.Name][strings.TrimPrefix(producer.Name, "promrw/")] = true
			}
		}
		for _, l := range metric.Labels {
			allowed[metric.Name][l.Key] = true
		}
	}
	seen := map[string]bool{}
	for _, s := range mc.All() {
		family := s.Name
		for _, suffix := range []string{"_bucket", "_count", "_sum"} {
			candidate := strings.TrimSuffix(s.Name, suffix)
			if candidate != s.Name && allowed[candidate] != nil {
				family = candidate
				break
			}
		}
		for key := range s.Labels {
			if key == "job" && producerJobs[family][s.Labels[key]] {
				continue
			}
			if !allowed[family][key] {
				t.Fatalf("uncaptured %s label %s", s.Name, key)
			}
		}
		identity := fmt.Sprintf("%s%v", s.Name, s.Labels)
		if seen[identity] {
			t.Fatalf("duplicate P3 series %s", identity)
		}
		seen[identity] = true
		if _, ok := s.Labels["source"]; ok {
			t.Fatalf("P3 source leaked on %s", s.Name)
		}
		if s.Name != "target_info" && (s.Labels["otel_scope_name"] == "" || s.Labels["otel_scope_version"] != "0.158.0") {
			t.Fatalf("P3 scope missing on %s", s.Name)
		}
	}
	event, pod := false, false
	for _, r := range oc.Resources {
		if r.Attrs["service.name"] == "integrations/kubernetes/eventhandler" {
			event = true
			if _, ok := r.Records[0].Attrs["event.name"]; !ok {
				t.Fatal("event metadata missing")
			}
		} else {
			pod = true
			if _, ok := r.Attrs["k8s.pod.uid"]; !ok {
				t.Fatal("pod UID missing")
			}
			if _, ok := r.Records[0].Attrs["log.file.path"]; !ok {
				t.Fatal("filelog path missing")
			}
		}
	}
	if !event || !pod {
		t.Fatalf("P3 events=%v podlogs=%v", event, pod)
	}
}

func TestCollectorPromP3TargetFamiliesAndHistogramLayouts(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Alloy = false
	c := buildConstructWithConfig(t, &k8scluster.Config{OTelCollectorProm: true}, cl)
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatal(err)
	}

	// These are the target families absent from the retained 125-family baseline. Keep
	// this list explicit so a successful test proves the P3 additions rather than only
	// proving that some family with a shared prefix was emitted.
	for _, name := range []string{
		"go_goroutines",
		"kube_node_role",
		"kube_node_spec_pod_cidrs",
		"kubelet_cgroup_manager_duration_seconds",
		"kubelet_pleg_relist_duration_seconds",
		"kubelet_pod_start_duration_seconds",
		"kubelet_pod_worker_duration_seconds",
		"node_cpu_frequency_max_hertz",
		"node_cpu_frequency_min_hertz",
		"node_cpu_isolated",
		"node_cpu_scaling_frequency_hertz",
		"node_cpu_scaling_frequency_max_hertz",
		"node_cpu_scaling_frequency_min_hertz",
		"node_cpu_scaling_governor",
		"rest_client_requests_total",
		"storage_operation_duration_seconds",
		"target_info",
	} {
		found := false
		for _, s := range mc.All() {
			if s.Name == name || strings.HasPrefix(s.Name, name+"_") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("P3 target family %q was not emitted", name)
		}
	}
	isolated := mc.Find("node_cpu_isolated")
	if len(isolated) != len(cl.Nodes) {
		t.Fatalf("modeled node_cpu_isolated series=%d, want one reserved CPU per node", len(isolated))
	}
	for _, s := range isolated {
		if s.Value != 1 {
			t.Errorf("modeled node_cpu_isolated[%s]=%v, want 1", s.Labels["cpu"], s.Value)
		}
	}

	// Read exact bucket-label strings from the independent capture. Numeric
	// bounds alone would miss Collector "1" versus Alloy "1.0" formatting.
	var corpus struct {
		Inventory struct {
			Metrics []struct {
				Name   string `json:"name"`
				Labels []struct {
					Key          string   `json:"key"`
					Values       []string `json:"values"`
					ValuesElided bool     `json:"values_elided"`
				} `json:"labels"`
			} `json:"metrics"`
		} `json:"inventory"`
	}
	b, err := os.ReadFile("../../../reality-corpus/k8s/k3d-lab-otel-collector-prom.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &corpus); err != nil {
		t.Fatal(err)
	}
	capturedLE := map[string][]string{}
	for _, metric := range corpus.Inventory.Metrics {
		for _, label := range metric.Labels {
			if label.Key == "le" && !label.ValuesElided {
				capturedLE[metric.Name] = label.Values
			}
		}
	}
	for _, name := range []string{
		"kubelet_cgroup_manager_duration_seconds",
		"kubelet_pleg_relist_duration_seconds",
		"kubelet_pod_worker_duration_seconds",
		"kubelet_pod_start_duration_seconds",
		"storage_operation_duration_seconds",
	} {
		want := capturedLE[name]
		if len(want) == 0 {
			t.Fatalf("%s: capture has no retained bucket-label evidence", name)
		}
		seen := map[string]bool{}
		for _, s := range mc.Find(name + "_bucket") {
			seen[s.Labels["le"]] = true
		}
		if len(seen) != len(want) {
			t.Errorf("%s bucket count=%d, want %d (%v)", name, len(seen), len(want), seen)
		}
		for _, le := range want {
			if !seen[le] {
				t.Errorf("%s missing le=%q (got %v)", name, le, seen)
			}
		}
	}
}
