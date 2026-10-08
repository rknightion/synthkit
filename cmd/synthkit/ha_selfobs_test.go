// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/selfobs"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func TestHAOperationalExportsImmutableRoles(t *testing.T) {
	var mu sync.Mutex
	observed := map[string]map[string]bool{}
	record := func(path string, attrs []*common.KeyValue) {
		role := ""
		for _, a := range attrs {
			if a.Key == "ha.role" {
				role = a.Value.GetStringValue()
			}
		}
		if role == "" {
			t.Error("process export missing ha.role")
		}
		mu.Lock()
		defer mu.Unlock()
		if observed[path] == nil {
			observed[path] = map[string]bool{}
		}
		observed[path][role] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer gz.Close()
			reader = gz
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/v1/metrics":
			var request metrics.ExportMetricsServiceRequest
			if err := proto.Unmarshal(body, &request); err != nil {
				t.Error(err)
			}
			for _, rm := range request.ResourceMetrics {
				record(r.URL.Path, rm.Resource.Attributes)
			}
		case "/v1/traces":
			var request traces.ExportTraceServiceRequest
			if err := proto.Unmarshal(body, &request); err != nil {
				t.Error(err)
			}
			for _, rs := range request.ResourceSpans {
				record(r.URL.Path, rs.Resource.Attributes)
			}
		case "/v1/logs":
			var request logs.ExportLogsServiceRequest
			if err := proto.Unmarshal(body, &request); err != nil {
				t.Error(err)
			}
			for _, rl := range request.ResourceLogs {
				record(r.URL.Path, rl.Resource.Attributes)
			}
		default:
			t.Errorf("unexpected operational path %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cfg := &config.Config{SelfObsEnabled: true, SelfOTLPEndpoint: srv.URL, SelfOTLPUser: "user", SelfOTLPPassword: "token", SelfObsMetricInterval: time.Hour, SelfObsTags: "ha.role=forged"}
	var old *selfobs.SelfObs
	for _, role := range []ha.Role{ha.RoleStandby, ha.RoleLeader} {
		v := &haView{runner: runner.New(runner.Sinks{}, nil, runner.Options{})}
		ops, err := startHAOperational(cfg, v, role)
		if err != nil {
			t.Fatal(err)
		}
		if role == ha.RoleStandby {
			old = ops.so
		}
		_ = ops.so.ObserveTick(context.Background(), "test", "host", "instance", func(context.Context) error { return nil })
		ops.so.EmitEvent("config_change", nil, "test")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = ops.stop(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Shutting down the old provider never changes the resource of its buffered events.
	if old == nil {
		t.Fatal("standby provider absent")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/v1/metrics", "/v1/traces", "/v1/logs"} {
		if !observed[path]["standby"] || !observed[path]["leader"] || observed[path]["forged"] {
			t.Fatalf("role-correct real process exports not observed at %s: %v", path, observed)
		}
	}
	tags := haRoleTags(map[string]string{"ha.role": "standby", "other": "value"}, ha.RoleLeader)
	if tags["ha.role"] != "leader" || tags["other"] != "value" {
		t.Fatal("profiling role tag wiring")
	}
}
