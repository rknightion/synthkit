// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"compress/gzip"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
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
	standard := map[string]bool{}
	originalWriter := log.Writer()
	t.Cleanup(func() { log.SetOutput(originalWriter) })
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
				role := ""
				for _, a := range rl.Resource.Attributes {
					if a.Key == "ha.role" {
						role = a.Value.GetStringValue()
					}
				}
				for _, scope := range rl.ScopeLogs {
					for _, entry := range scope.LogRecords {
						if strings.Contains(entry.Body.GetStringValue(), "ha-standard-log-"+role) {
							mu.Lock()
							standard[role] = true
							mu.Unlock()
						}
					}
				}
			}
		default:
			t.Errorf("unexpected operational path %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cfg := &config.Config{SelfObsEnabled: true, SelfOTLPEndpoint: srv.URL, SelfOTLPUser: "user", SelfOTLPPassword: "token", SelfObsMetricInterval: time.Hour, SelfObsTags: "ha.role=forged"}
	var old *selfobs.SelfObs
	var standbyOps haOperational
	for _, role := range []ha.Role{ha.RoleStandby, ha.RoleLeader} {
		v := &haView{runner: runner.New(runner.Sinks{}, nil, runner.Options{})}
		ops, err := startHAOperational(cfg, v, role)
		if err != nil {
			t.Fatal(err)
		}
		if role == ha.RoleStandby {
			old = ops.so
			standbyOps = ops
		}
		_ = ops.so.ObserveTick(context.Background(), "test", "host", "instance", func(context.Context) error { return nil })
		ops.so.EmitEvent("config_change", nil, "test")
		if role == ha.RoleLeader {
			late, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = standbyOps.stop(late)
			cancel()
		}
		log.Print("ha-standard-log-" + string(role))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = ops.stop(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if log.Writer() != originalWriter {
			t.Fatal("role shutdown retained its log destination")
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
	if !standard["standby"] || !standard["leader"] {
		t.Fatalf("standard logs missing from real role-scoped OTLP exports: %v", standard)
	}
	tags := haRoleTags(map[string]string{"ha.role": "standby", "other": "value"}, ha.RoleLeader)
	if tags["ha.role"] != "leader" || tags["other"] != "value" {
		t.Fatal("profiling role tag wiring")
	}
}

func TestHAOperationalDisabledPreservesStandardLogger(t *testing.T) {
	original := log.Writer()
	t.Cleanup(func() { log.SetOutput(original) })
	v := &haView{runner: runner.New(runner.Sinks{}, nil, runner.Options{})}
	ops, e := startHAOperational(&config.Config{}, v, ha.RoleStandby)
	if e != nil {
		t.Fatal(e)
	}
	if log.Writer() != original {
		t.Fatal("disabled self-observability replaced standard logging")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = ops.stop(ctx); e != nil {
		t.Fatal(e)
	}
	if log.Writer() != original {
		t.Fatal("disabled shutdown changed standard logging")
	}
}
