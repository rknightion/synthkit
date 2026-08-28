// SPDX-License-Identifier: AGPL-3.0-only

package k8saddon_test

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/argocd"
	"github.com/rknightion/synthkit/internal/construct/certmanager"
	"github.com/rknightion/synthkit/internal/construct/envoygateway"
	"github.com/rknightion/synthkit/internal/construct/extdns"
	"github.com/rknightion/synthkit/internal/construct/karpenter"
	"github.com/rknightion/synthkit/internal/construct/lbc"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/k8saddon"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

var capturedLokiPodLogLabelKeys = []string{
	"app_kubernetes_io_name",
	"cluster",
	"container",
	"flags",
	"job",
	"k8s_cluster_name",
	"namespace",
	"service_name",
	"service_namespace",
	"stream",
}

var capturedLokiPodLogMetadataKeys = []string{"pod", "service_instance_id"}

func TestLokiAddonStreamsMatchCapturedShape(t *testing.T) {
	constructs := []struct {
		name  string
		build func(*fixture.Set) (core.Construct, error)
	}{
		{name: "karpenter", build: func(fx *fixture.Set) (core.Construct, error) {
			return karpenter.New(&karpenter.Config{}, fx)
		}},
		{name: "argocd", build: func(fx *fixture.Set) (core.Construct, error) {
			return argocd.New(&argocd.Config{}, fx)
		}},
		{name: "certmanager", build: func(fx *fixture.Set) (core.Construct, error) {
			return certmanager.New(&certmanager.Config{}, fx)
		}},
		{name: "load_balancer_controller", build: func(fx *fixture.Set) (core.Construct, error) {
			return lbc.Registration().Build(&lbc.Config{}, fx)
		}},
		{name: "external_dns", build: func(fx *fixture.Set) (core.Construct, error) {
			return extdns.Registration().Build(&extdns.Config{}, fx)
		}},
		{name: "envoy_gateway", build: func(fx *fixture.Set) (core.Construct, error) {
			return envoygateway.New(&envoygateway.Config{}, fx)
		}},
	}

	for _, tc := range constructs {
		t.Run(tc.name, func(t *testing.T) {
			cl := capturedLokiPodLogCluster(t)
			c, err := tc.build(&fixture.Set{Seed: "test", Cluster: cl})
			if err != nil {
				t.Fatalf("build: %v", err)
			}

			logs := &coretest.LogCapture{}
			for i := range 20 {
				w := coretest.World(&coretest.MetricCapture{}, logs, nil)
				now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
				if err := c.Tick(context.Background(), now, w); err != nil {
					t.Fatalf("tick %d: %v", i, err)
				}
			}
			if len(logs.Streams) == 0 {
				t.Fatal("no Loki streams emitted across 20 ticks")
			}

			for streamIndex, stream := range logs.Streams {
				if got := sortedMapKeys(stream.Labels); !reflect.DeepEqual(got, capturedLokiPodLogLabelKeys) {
					t.Errorf("stream %d label keys = %v, want exactly %v", streamIndex, got, capturedLokiPodLogLabelKeys)
				}
				if stream.Labels["job"] != stream.Labels["namespace"]+"/"+stream.Labels["container"] {
					t.Errorf("stream %d job=%q, want namespace/container form", streamIndex, stream.Labels["job"])
				}
				if stream.Labels["flags"] != "F" {
					t.Errorf("stream %d flags=%q, want F", streamIndex, stream.Labels["flags"])
				}
				if stream.Labels["stream"] != "stdout" && stream.Labels["stream"] != "stderr" {
					t.Errorf("stream %d stream=%q, want stdout or stderr", streamIndex, stream.Labels["stream"])
				}
				if len(stream.Lines) == 0 {
					t.Errorf("stream %d has no lines", streamIndex)
					continue
				}
				for lineIndex, line := range stream.Lines {
					if got := sortedMapKeys(line.Meta); !reflect.DeepEqual(got, capturedLokiPodLogMetadataKeys) {
						t.Errorf("stream %d line %d metadata keys = %v, want exactly %v", streamIndex, lineIndex, got, capturedLokiPodLogMetadataKeys)
					}
					if line.Meta["pod"] == "" || line.Meta["service_instance_id"] == "" {
						t.Errorf("stream %d line %d metadata values must be non-empty: %v", streamIndex, lineIndex, line.Meta)
					}
					wantServiceID := stream.Labels["namespace"] + "." + line.Meta["pod"] + "." + stream.Labels["container"]
					if line.Meta["service_instance_id"] != wantServiceID {
						t.Errorf("stream %d line %d service_instance_id=%q, want %q", streamIndex, lineIndex, line.Meta["service_instance_id"], wantServiceID)
					}
				}
			}
		})
	}
}

func TestNewLokiPodLogStreamPreservesContent(t *testing.T) {
	first := time.Date(2026, 6, 13, 12, 0, 0, 123, time.UTC)
	second := first.Add(time.Second)
	input := []loki.Line{
		{T: first, Body: `{"level":"info","msg":"first"}`},
		{T: second, Body: `{"level":"error","msg":"second"}`},
	}

	got := k8saddon.NewLokiPodLogStream(k8saddon.LokiPodLogConfig{
		Cluster:          "test-cluster",
		Namespace:        "kube-system",
		Container:        "controller",
		AppName:          "karpenter",
		ServiceName:      "karpenter",
		ServiceNamespace: "kube-system",
		Stream:           "stderr",
		Flags:            "F",
		Pod:              "karpenter-abc-123",
	}, input)

	if len(got.Lines) != len(input) {
		t.Fatalf("line count = %d, want %d", len(got.Lines), len(input))
	}
	for i := range input {
		if !got.Lines[i].T.Equal(input[i].T) {
			t.Errorf("line %d timestamp = %v, want %v", i, got.Lines[i].T, input[i].T)
		}
		if got.Lines[i].Body != input[i].Body {
			t.Errorf("line %d body = %q, want unchanged %q", i, got.Lines[i].Body, input[i].Body)
		}
	}
}

func capturedLokiPodLogCluster(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	var workloads []fixture.Workload
	for _, addon := range []string{
		"karpenter", "argocd", "cert_manager", "load_balancer_controller", "external_dns", "envoy_gateway",
	} {
		for _, wl := range fixture.AddonWorkloads(addon) {
			wl.PodNames = fixture.WorkloadPodNames("test", wl, cl.Nodes)
			wl.NodeIdx = make([]int, wl.Replicas)
			for i := range wl.NodeIdx {
				wl.NodeIdx[i] = i % len(cl.Nodes)
			}
			workloads = append(workloads, wl)
		}
	}
	cl.SubstrateWorkloads = workloads
	return cl
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
