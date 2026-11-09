// SPDX-License-Identifier: AGPL-3.0-only

package operationaldashboard_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/selfobs"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// The fixture receives actual public selfobs API exports, rather than assigning projected
// labels to an invented process. Backend projection and PromQL execution are checked separately
// by the Python CLI suite with promtool; this proof pins the resource facts that projection uses.
func TestExportScopeMatchesRealReceiverResources(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	for _, deployment := range []string{"alpha", "beta"} {
		for _, process := range []string{"first-process", "replacement-process"} {
			t.Run(deployment+"/"+process, func(t *testing.T) {
				var mu sync.Mutex
				resources := map[string][]map[string]string{}
				record := func(path string, attrs []*common.KeyValue) {
					values := map[string]string{}
					for _, attr := range attrs {
						values[attr.Key] = attr.Value.GetStringValue()
					}
					mu.Lock()
					resources[path] = append(resources[path], values)
					mu.Unlock()
				}
				receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var reader io.Reader = r.Body
					if r.Header.Get("Content-Encoding") == "gzip" {
						gz, err := gzip.NewReader(r.Body)
						if err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
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
						var req metrics.ExportMetricsServiceRequest
						if err := proto.Unmarshal(body, &req); err != nil {
							t.Error(err)
						}
						for _, item := range req.ResourceMetrics {
							record(r.URL.Path, item.Resource.Attributes)
						}
					case "/v1/logs":
						var req logs.ExportLogsServiceRequest
						if err := proto.Unmarshal(body, &req); err != nil {
							t.Error(err)
						}
						for _, item := range req.ResourceLogs {
							record(r.URL.Path, item.Resource.Attributes)
						}
					case "/v1/traces":
						var req traces.ExportTraceServiceRequest
						if err := proto.Unmarshal(body, &req); err != nil {
							t.Error(err)
						}
						for _, item := range req.ResourceSpans {
							record(r.URL.Path, item.Resource.Attributes)
						}
					default:
						t.Errorf("unexpected receiver path %s", r.URL.Path)
					}
					w.WriteHeader(http.StatusOK)
				}))
				defer receiver.Close()
				so, err := selfobs.Start(selfobs.Options{
					Enabled: true, Endpoint: receiver.URL, User: "fixture", Password: "fixture",
					Tags:           selfobs.ParseTags("service.namespace=" + deployment + ",service.instance.id=" + process),
					MetricInterval: time.Hour,
				}, selfobs.Gauges{})
				if err != nil {
					t.Fatal(err)
				}
				var shutdownOnce sync.Once
				shutdown := func() {
					shutdownOnce.Do(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer cancel()
						so.Shutdown(ctx)
					})
				}
				defer shutdown()
				if err := so.ObserveTick(context.Background(), "fixture", "host", "fixture", func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
				so.EmitEvent("config_change", nil, "fixture")
				shutdown()
				mu.Lock()
				defer mu.Unlock()
				for _, path := range []string{"/v1/metrics", "/v1/logs", "/v1/traces"} {
					if len(resources[path]) == 0 {
						t.Fatalf("no actual export at %s", path)
					}
					for _, attrs := range resources[path] {
						if attrs["service.namespace"] != deployment || attrs["service.name"] != "synthkit" || attrs["service.instance.id"] != process {
							t.Fatalf("%s: resource identity does not match declared deployment/process", path)
						}
					}
				}
				out := filepath.Join(t.TempDir(), "export")
				commandCtx, commandCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer commandCancel()
				command := exec.CommandContext(commandCtx, "python3", "export_selfobs_dashboard.py", "--namespace", "fixture", "--deployment", deployment, "--out", out)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("export command: %v: %s", err, output)
				}
				data, err := os.ReadFile(filepath.Join(out, "queries.json"))
				if err != nil {
					t.Fatal(err)
				}
				var inventory struct {
					Queries []struct {
						Signal, Expression string
					}
				}
				if err := json.Unmarshal(data, &inventory); err != nil {
					t.Fatal(err)
				}
				for _, query := range inventory.Queries {
					var wanted string
					switch query.Signal {
					case "metrics":
						attrs := resources["/v1/metrics"][0]
						wanted = `job="` + attrs["service.namespace"] + "/" + attrs["service.name"] + `"`
					case "logs":
						wanted = `service_namespace="` + resources["/v1/logs"][0]["service.namespace"] + `"`
					case "traces":
						wanted = `resource.service.namespace="` + resources["/v1/traces"][0]["service.namespace"] + `"`
					case "profiles":
						continue // profiles have a separately documented PYROSCOPE_TAGS prerequisite
					default:
						t.Fatalf("unverified exported signal %q", query.Signal)
					}
					if !strings.Contains(query.Expression, wanted) {
						t.Fatalf("query is not constrained to the identity actually received over OTLP: %s", query.Expression)
					}
				}
			})
		}
	}
}
