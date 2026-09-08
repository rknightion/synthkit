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
				Name   string `json:"name"`
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
	for _, metric := range corpus.Inventory.Metrics {
		allowed[metric.Name] = map[string]bool{}
		for _, l := range metric.Labels {
			allowed[metric.Name][l.Key] = true
		}
	}
	seen := map[string]bool{}
	for _, s := range mc.All() {
		for key := range s.Labels {
			if !allowed[s.Name][key] {
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
		if s.Labels["otel_scope_name"] == "" || s.Labels["otel_scope_version"] != "0.158.0" {
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
