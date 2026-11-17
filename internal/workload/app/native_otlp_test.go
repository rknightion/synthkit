// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	sigilsink "github.com/rknightion/synthkit/internal/sink/sigil"
	v1 "github.com/rknightion/synthkit/internal/sink/sigil/v1"
	"github.com/rknightion/synthkit/internal/state"
	"github.com/rknightion/synthkit/internal/telemetryspec"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// TestAgentPipelineTLSWire crosses the real native generation/trace/metrics
// encoders. The injected stages/ratings are process edges, not runtime admission
// or a claim that the production hook/rating HTTP adapters have been installed.
func TestAgentPipelineTLSWire(t *testing.T) {
	var mu sync.Mutex
	var gens []*v1.Generation
	var steps []*v1.WorkflowStep
	var traceRequests []*collectortrace.ExportTraceServiceRequest
	var metricRequests []*collectormetrics.ExportMetricsServiceRequest
	var ratingInputs []sigil.ConversationRatingInput
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if r.Header.Get("Content-Encoding") == "gzip" {
			z, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			body, err = io.ReadAll(z)
			z.Close()
			if err != nil {
				t.Error(err)
				return
			}
		}
		if r.Method != "POST" {
			t.Error("not POST")
		}
		switch r.URL.Path {
		case "/api/v1/generations:export":
			var req v1.ExportGenerationsRequest
			if err := protojson.Unmarshal(body, &req); err != nil {
				t.Error(err)
			}
			gens = append(gens, req.Generations...)
			w.WriteHeader(202)
		case "/api/v1/workflow-steps:export":
			var req v1.ExportWorkflowStepsRequest
			if err := protojson.Unmarshal(body, &req); err != nil {
				t.Error(err)
			}
			steps = append(steps, req.WorkflowSteps...)
			w.WriteHeader(202)
		case "/v1/traces":
			var req collectortrace.ExportTraceServiceRequest
			if err := proto.Unmarshal(body, &req); err != nil {
				t.Error(err)
			}
			traceRequests = append(traceRequests, &req)
		case "/v1/metrics":
			var req collectormetrics.ExportMetricsServiceRequest
			if err := proto.Unmarshal(body, &req); err != nil {
				t.Error(err)
			}
			metricRequests = append(metricRequests, &req)
		case "/rating":
			var req sigil.ConversationRatingInput
			if err := json.Unmarshal(body, &req); err != nil {
				t.Error(err)
			}
			ratingInputs = append(ratingInputs, req)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	// Existing sinks intentionally use the standard transport. Replace it only in
	// this serial test with the local server's trusted TLS client, never insecure TLS.
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	c := pipelineConfig(t)
	c.AgentPipeline.RequestKinds = []PipelineKind{{Name: "long", Weight: 1}, {Name: "short", Weight: 0}}
	c.AgentPipeline.Outcomes = []PipelineOutcome{{Name: "ready", Weight: 1, ReviewPassed: true}, {Name: "manual", Revisions: 2}}
	c.AgentPipeline.Ratings.SampleRate = 1
	native, err := sigilsink.New(server.URL, "test", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	rating := &wireRatingWriter{client: server.Client(), url: server.URL + "/rating"}
	raw, err := Registration().Build(c, pipelineBinding(c, &pipelineStages{}, rating))
	if err != nil {
		t.Fatal(err)
	}
	w := raw.(*Workload)
	mc := &coretest.MetricCapture{}
	world := &core.World{Sigil: native, Traces: otlp.New(server.URL, "test", "test", false), OTLPMetrics: otlp.NewMetrics(server.URL, "test", "test", false), Metrics: mc, Shape: shape.New("", nil)}
	r := pipelineRequest(42)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = w.ProjectBatch(ctx, r.Start, world, []*ledger.Request{r}); err != nil {
		t.Fatal(err)
	}
	if err = w.Tick(ctx, r.Start, world); err != nil {
		t.Fatal(err)
	}
	if err = w.Tick(ctx, r.Start.Add(time.Minute), world); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gens) != 4 || len(ratingInputs) != 1 || len(metricRequests) != 2 {
		t.Fatalf("wire counts gens=%d ratings=%d metrics=%d", len(gens), len(ratingInputs), len(metricRequests))
	}
	spans := map[string]string{}
	parents := map[string]string{}
	services := map[string]bool{}
	arraySeen := false
	for _, req := range traceRequests {
		for _, res := range req.ResourceSpans {
			attrs := wireAttrs(res.Resource.Attributes)
			svc := attrs["service.name"].GetStringValue()
			services[svc] = true
			if got := attrs["k8s.pod.name"].GetStringValue(); got != svc+"-actual-hash" {
				t.Errorf("unplaced wire pod %s", got)
			}
			for _, scope := range res.ScopeSpans {
				for _, s := range scope.Spans {
					id := hex.EncodeToString(s.SpanId)
					if _, dup := spans[id]; dup {
						t.Error("duplicate span ID")
					}
					spans[id] = svc
					parents[id] = hex.EncodeToString(s.ParentSpanId)
					if hex.EncodeToString(s.TraceId) != r.TraceID {
						t.Error("split trace")
					}
					a := wireAttrs(s.Attributes)
					if value := a[sigil.AttrParentGenerationIDs]; value != nil {
						arraySeen = true
						if value.GetArrayValue() == nil {
							t.Error("stringified parent array")
						}
					}
				}
			}
		}
	}
	if len(services) != 5 || !arraySeen {
		t.Fatalf("services=%v python-array=%v", services, arraySeen)
	}
	for id, parent := range parents {
		if parent != "" && spans[parent] == "" {
			t.Errorf("orphan %s -> %s", id, parent)
		}
	}
	for _, g := range gens {
		if spans[g.SpanId] == "" || g.TraceId != r.TraceID || g.ConversationId != r.SessionID || g.AgentName == "retriever" {
			t.Errorf("unjoined generation %+v", g)
		}
		b, _ := protojson.Marshal(g)
		if strings.Contains(string(b), "UNTRUSTED") {
			t.Error("unsafe carry reached actual encoder")
		}
	}
	hasSource := false
	for _, s := range steps {
		if s.AgentName == "retriever" {
			hasSource = true
			if len(s.LinkedGenerationIds) != 0 {
				t.Error("source fake generation")
			}
		}
	}
	if !hasSource {
		t.Error("missing actual source workflow step")
	}
	counts := func(req *collectormetrics.ExportMetricsServiceRequest) map[string]uint64 {
		out := map[string]uint64{}
		for _, res := range req.ResourceMetrics {
			svc := wireAttrs(res.Resource.Attributes)["service.name"].GetStringValue()
			if svc == "source" {
				t.Error("source SDK model metrics")
			}
			for _, scope := range res.ScopeMetrics {
				if scope.Scope.Name != sigil.ScopeGo && scope.Scope.Name != sigil.ScopePython {
					continue
				}
				for _, m := range scope.Metrics {
					if m.Name != pipelineDurationMetric && m.Name != pipelineTokenMetric {
						t.Error("invented SDK instrument")
					}
					for _, point := range m.GetHistogram().DataPoints {
						attrs := wireAttrs(point.Attributes)
						if attrs["gen_ai.agent.name"] == nil {
							t.Error("SDK identity missing")
						}
						out[svc+"/"+m.Name+"/"+attrs["gen_ai.token.type"].GetStringValue()] = point.Count
					}
				}
			}
		}
		return out
	}
	first, last := counts(metricRequests[0]), counts(metricRequests[1])
	if len(first) != 12 {
		t.Fatalf("native model series %d, want four model services with duration and two token types", len(first))
	}
	for key, count := range first {
		if count != 1 {
			t.Errorf("wire execution count %s=%d, want 1", key, count)
		}
	}
	if !reflect.DeepEqual(first, last) {
		t.Fatal("Tick fabricated additional observations")
	}
	for _, s := range mc.All() {
		if strings.HasPrefix(s.Name, "gen_ai_client_") || strings.HasPrefix(s.Name, "agento11y_") {
			t.Error("duplicate SDK/backend metric push")
		}
	}
	if ratingInputs[0].RatingID == "" || ratingInputs[0].GenerationID != gens[len(gens)-1].Id {
		t.Fatal("rating not conversation-correlated")
	}
}

// The acceptance contract is the pinned SDK native-export path, not merely
// absence of the injection sentinel. Accepted nonempty text AND JSON must remain
// in native generations for default/no_tool_content/full_with_metadata_spans,
// while shared tool spans contain them only for explicit full (no per-tool opt-in
// exists on this component). Metadata-only preserves native message/tool shape.
func TestAgentPipelineCaptureWire(t *testing.T) {
	for _, sdk := range []string{sigil.SDKGo, sigil.SDKPython} {
		for _, mode := range []string{"", "default", sigil.CaptureNoToolContent, sigil.CaptureFull, sigil.CaptureFullWithMetadataSpans, sigil.CaptureMetadataOnly} {
			name := mode
			if name == "" {
				name = "default"
			}
			t.Run(sdk+"/"+name, func(t *testing.T) {
				var mu sync.Mutex
				var gens []*v1.Generation
				var traces []*collectortrace.ExportTraceServiceRequest
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					if r.Header.Get("Content-Encoding") == "gzip" {
						zr, e := gzip.NewReader(bytes.NewReader(body))
						if e != nil {
							t.Error(e)
							w.WriteHeader(500)
							return
						}
						body, err = io.ReadAll(zr)
						zr.Close()
						if err != nil {
							t.Error(err)
							return
						}
					}
					switch r.URL.Path {
					case "/api/v1/generations:export":
						var req v1.ExportGenerationsRequest
						if e := protojson.Unmarshal(body, &req); e != nil {
							t.Error(e)
						}
						gens = append(gens, req.Generations...)
						w.WriteHeader(202)
					case "/api/v1/workflow-steps:export":
						w.WriteHeader(202)
					case "/v1/traces":
						var req collectortrace.ExportTraceServiceRequest
						if e := proto.Unmarshal(body, &req); e != nil {
							t.Error(e)
						}
						traces = append(traces, &req)
					default:
						t.Error("unexpected route", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer server.Close()
				old := http.DefaultTransport
				http.DefaultTransport = server.Client().Transport
				defer func() { http.DefaultTransport = old }()
				c := pipelineConfig(t)
				c.AgentPipeline.Ratings.SampleRate = 0
				c.AgentPipeline.Outcomes = []PipelineOutcome{{Name: "ready", Weight: 1, ReviewPassed: true}, {Name: "manual", Revisions: 2}}
				for i := range c.Services {
					a := c.Services[i].AgentObservability
					a.SDK = sdk
					a.CaptureMode = mode
					c.Services[i].Runtime = "python"
					if sdk == sigil.SDKGo {
						c.Services[i].Runtime = "go"
					}
					a.Metadata = map[string]any{sigil.AttrConversationTitle: "private-title", "sigil.conversation.title": "legacy-private-title", "call_error": "raw-mirrored-error", "safe-metadata": "retained"}
					a.GenerationTags = map[string]string{"safe-tag": "retained"}
				}
				jsonOnly := false
				stage := &decisionStages{before: func(_ context.Context, _ sigil.GenerationStage, r sigil.ToolResult) (sigil.ToolResultDecision, error) {
					r.Content = "accepted-native-text"
					r.ContentJSON = []byte(`{"accepted":"native-json"}`)
					if jsonOnly {
						r.Content = ""
					}
					return sigil.ToolResultDecision{Allowed: true, Transformed: &r}, nil
				}}
				sink, e := sigilsink.New(server.URL, "test", "test", false)
				if e != nil {
					t.Fatal(e)
				}
				raw, e := Registration().Build(c, pipelineBinding(c, stage, nil))
				if e != nil {
					t.Fatal(e)
				}
				world := &core.World{Sigil: sink, Traces: otlp.New(server.URL, "test", "test", false)}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for i := range 2 {
					jsonOnly = i == 1
					r := pipelineRequest(100 + i)
					if e = raw.ProjectBatch(ctx, r.Start, world, []*ledger.Request{r}); e != nil {
						t.Fatal(e)
					}
				}
				mu.Lock()
				defer mu.Unlock()
				foundCall, foundResult := false, false
				foundTextResult, foundJSONResult := false, false
				for _, g := range gens {
					if len(g.Input) == 0 || len(g.Output) == 0 || g.Usage == nil || g.Usage.TotalTokens == 0 {
						t.Error("capture lost native message/usage structure")
					}
					metadata := g.GetMetadata().AsMap()
					effective := mode
					if effective == "" || effective == "default" {
						effective = sigil.CaptureNoToolContent
					}
					if metadata["agento11y.sdk.content_capture_mode"] != effective {
						t.Error("missing SDK capture marker", metadata)
					}
					if metadata["safe-metadata"] != "retained" || g.Tags["safe-tag"] != "retained" {
						t.Error("arbitrary authored metadata/tags were scrubbed")
					}
					if mode == sigil.CaptureMetadataOnly {
						if g.SystemPrompt != "" || metadata[sigil.AttrConversationTitle] != nil || metadata["call_error"] != nil {
							t.Error("metadata-only retained SDK content mirror")
						}
						if sdk == sigil.SDKGo && metadata["sigil.conversation.title"] != nil {
							t.Error("Go legacy title retained")
						}
						if sdk == sigil.SDKPython && metadata["sigil.conversation.title"] != "legacy-private-title" {
							t.Error("Python authored legacy metadata changed without source")
						}
					} else if g.SystemPrompt == "" {
						t.Error("non-metadata mode stripped generation system prompt")
					}
					for _, tool := range g.Tools {
						if tool.Name == "" || tool.Type != "function" {
							t.Error("capture lost tool definition identity")
						}
					}
					for _, messages := range [][]*v1.Message{g.Input, g.Output} {
						for _, m := range messages {
							if m.Role == v1.MessageRole_MESSAGE_ROLE_UNSPECIFIED {
								t.Error("lost role")
							}
							for _, part := range m.Parts {
								switch payload := part.Payload.(type) {
								case *v1.Part_Text:
									if mode == sigil.CaptureMetadataOnly && payload.Text != "" {
										t.Error("text not stripped")
									}
								case *v1.Part_ToolCall:
									foundCall = true
									if payload.ToolCall.Id == "" || payload.ToolCall.Name != "retrieve_source_note" {
										t.Error("lost tool-call identity")
									}
									if mode == sigil.CaptureMetadataOnly {
										if len(payload.ToolCall.InputJson) > 0 {
											t.Error("tool arguments retained")
										}
									} else if string(payload.ToolCall.InputJson) != "{}" {
										t.Error("generation tool arguments stripped outside metadata_only")
									}
								case *v1.Part_ToolResult:
									foundResult = true
									result := payload.ToolResult
									if result.ToolCallId == "" || result.Name != "retrieve_source_note" {
										t.Error("lost result identity")
									}
									if mode == sigil.CaptureMetadataOnly {
										if result.Content != "" || len(result.ContentJson) > 0 {
											t.Error("result content not stripped")
										}
									} else {
										if string(result.ContentJson) != `{"accepted":"native-json"}` {
											t.Error("generation JSON result stripped")
										}
										foundJSONResult = true
										if result.Content == "accepted-native-text" {
											foundTextResult = true
										}
									}
								default:
									t.Errorf("part structural payload lost: %T", payload)
								}
							}
						}
					}
				}
				if !foundCall || !foundResult {
					t.Error("tool message structure absent")
				}
				if mode != sigil.CaptureMetadataOnly && (!foundTextResult || !foundJSONResult) {
					t.Error("accepted nonempty native content never observed")
				}
				textSpan, jsonSpan := false, false
				toolSpans := 0
				for _, req := range traces {
					for _, res := range req.ResourceSpans {
						for _, scope := range res.ScopeSpans {
							for _, span := range scope.Spans {
								attrs := wireAttrs(span.Attributes)
								if attrs["gen_ai.operation.name"].GetStringValue() != sigil.OpExecuteTool {
									continue
								}
								toolSpans++
								if attrs["gen_ai.tool.call.id"].GetStringValue() == "" {
									t.Error("span lost tool identity")
								}
								value := attrs["gen_ai.tool.call.result"]
								if mode != sigil.CaptureFull {
									if value != nil || attrs["gen_ai.tool.call.arguments"] != nil || attrs["gen_ai.tool.description"] != nil {
										t.Error("tool content leaked to reduced/default trace channel")
									}
								} else {
									if attrs["gen_ai.tool.call.arguments"].GetStringValue() != "{}" {
										t.Error("explicit full omitted actual tool-call arguments")
									}
									if value.GetStringValue() == "accepted-native-text" {
										textSpan = true
									}
									if value.GetStringValue() == `{"accepted":"native-json"}` {
										jsonSpan = true
									}
								}
							}
						}
					}
				}
				if toolSpans != 2 {
					t.Errorf("tool span count %d", toolSpans)
				}
				if mode == sigil.CaptureFull && (!textSpan || !jsonSpan) {
					t.Error("explicit full did not carry both actual text and JSON span cases")
				}
			})
		}
	}
}

func TestAgentPipelineMetadataOnlyShape(t *testing.T) {
	// SDK 75a1c548 contentcapture.Strip: reduce content without removing
	// roles, parts, call/result identity, usage, timing or authored metadata.
	g := sigil.Generation{SystemPrompt: "private", CallError: "raw error", Metadata: map[string]any{sigil.AttrSDKName: sigil.SDKGo, "call_error": "raw mirror", sigil.AttrConversationTitle: "title", "sigil.conversation.title": "old title", "error.category": "timeout", "authored": "kept"}, Tags: map[string]string{"authored": "kept"}, Usage: sigil.Usage{Input: 10, Output: 5, Total: 15}, Tools: []sigil.ToolDef{{Name: "tool", Type: "function", Description: "private", InputSchemaJSON: []byte(`{"type":"object"}`), Deferred: true}}, Input: []sigil.Message{{Role: sigil.RoleUser, Parts: []sigil.Part{{Text: "private", ProviderType: "text"}, {ToolResult: &sigil.ToolResult{ToolCallID: "call-id", Name: "tool", Content: "private", ContentJSON: []byte(`{"private":true}`)}}}}}, Output: []sigil.Message{{Role: sigil.RoleAssistant, Parts: []sigil.Part{{ToolCall: &sigil.ToolCall{ID: "call-id", Name: "tool", InputJSON: []byte(`{"private":true}`)}}, {Media: &sigil.Media{Kind: "image", URL: "data:private", MIMEType: "image/png", Name: "reference"}}}}}}
	out, err := pipelineCaptureGeneration(g, sigil.CaptureMetadataOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Input) != 1 || len(out.Input[0].Parts) != 2 || len(out.Output) != 1 || len(out.Output[0].Parts) != 2 || out.Input[0].Role != sigil.RoleUser || out.Output[0].Role != sigil.RoleAssistant || !reflect.DeepEqual(out.Usage, g.Usage) {
		t.Fatal("structural evidence was dropped")
	}
	tool := out.Tools[0]
	if tool.Name != "tool" || tool.Type != "function" || !tool.Deferred || tool.Description != "" || len(tool.InputSchemaJSON) != 0 {
		t.Fatal("tool definition capture differs")
	}
	call := out.Output[0].Parts[0].ToolCall
	result := out.Input[0].Parts[1].ToolResult
	media := out.Output[0].Parts[1].Media
	if call.ID != "call-id" || call.Name != "tool" || len(call.InputJSON) != 0 || result.ToolCallID != "call-id" || result.Name != "tool" || result.Content != "" || len(result.ContentJSON) != 0 || out.Input[0].Parts[0].Text != "" || media.URL != "" || media.Kind != "image" || media.MIMEType != "image/png" || media.Name != "reference" {
		t.Fatal("content/identity split differs from SDK")
	}
	if out.SystemPrompt != "" || out.CallError != "timeout" || out.Metadata["call_error"] != nil || out.Metadata[sigil.AttrConversationTitle] != nil || out.Metadata["sigil.conversation.title"] != nil || out.Metadata["authored"] != "kept" || out.Tags["authored"] != "kept" {
		t.Fatal("metadata/content capture differs")
	}
	if g.SystemPrompt != "private" || g.Input[0].Parts[1].ToolResult.Content != "private" {
		t.Fatal("capture mutated model-visible completed record")
	}
	// Metadata-only removes thinking content but keeps the part a thinking part:
	// the explicit kind survives the empty content, ProviderType stays advisory
	// metadata, and the model-visible original is untouched.
	g.Output[0].Parts = []sigil.Part{{Thinking: "private thinking", ProviderType: "thinking"}}
	out, err = pipelineCaptureGeneration(g, sigil.CaptureMetadataOnly)
	if err != nil {
		t.Fatal(err)
	}
	if want := (sigil.Part{Kind: sigil.PartKindThinking, ProviderType: "thinking"}); !reflect.DeepEqual(out.Output[0].Parts, []sigil.Part{want}) {
		t.Fatalf("thinking part not stripped to the explicit kind: %+v", out.Output[0].Parts)
	}
	if g.Output[0].Parts[0].Thinking != "private thinking" || g.Output[0].Parts[0].Kind != "" {
		t.Fatal("capture mutated model-visible thinking part")
	}
}

func wireAttrs(kvs []*commonpb.KeyValue) map[string]*commonpb.AnyValue {
	m := map[string]*commonpb.AnyValue{}
	for _, kv := range kvs {
		m[kv.Key] = kv.Value
	}
	return m
}

type wireRatingWriter struct {
	client *http.Client
	url    string
}

func (w *wireRatingWriter) WriteConversationRating(ctx context.Context, _ string, in sigil.ConversationRatingInput) error {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	res, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = io.Copy(io.Discard, res.Body)
	return err
}

type appNativeMetricCapture struct {
	resources []otlp.MetricResource
}

func (c *appNativeMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func appNativeConfig(enabled bool, profiles []string) *Config {
	gauge, counter, histValue := 3.0, 2.0, 0.75
	zone := "blue"
	return &Config{
		OTel: func() *OTelObs {
			if !enabled {
				return nil
			}
			return &OTelObs{Metrics: true}
		}(),
		Services: []ServiceNode{{
			Name:     "api",
			Type:     "web",
			Runtime:  "go",
			Entry:    true,
			Profiles: profiles,
			Metrics: []telemetryspec.MetricSpec{
				{Name: "app_queue_depth", Instrument: telemetryspec.InstrumentGauge, Unit: "{item}", Value: telemetryspec.ValueModel{Const: &gauge}, Labels: map[string]telemetryspec.ValueModel{"zone": {ConstStr: &zone}}},
				{Name: "app_requests_total", Instrument: telemetryspec.InstrumentCounter, Unit: "{request}", Value: telemetryspec.ValueModel{Const: &counter}, Labels: map[string]telemetryspec.ValueModel{"zone": {ConstStr: &zone}}},
				{Name: "app_request_duration", Instrument: telemetryspec.InstrumentHistogram, Unit: "s", Value: telemetryspec.ValueModel{Const: &histValue}, Buckets: []float64{0.5, 1}, Labels: map[string]telemetryspec.ValueModel{"zone": {ConstStr: &zone}}},
			},
		}},
	}
}

func appNativeWorld(mc *coretest.MetricCapture, native *appNativeMetricCapture) *core.World {
	w := &core.World{Shape: shape.New("", nil), EmitSpanMetrics: false}
	if mc != nil {
		w.Metrics = mc
	}
	if native != nil {
		w.OTLPMetrics = native
	}
	return w
}

func appNativeMetric(t *testing.T, resource otlp.MetricResource, name string) otlp.Metric {
	t.Helper()
	for _, metric := range resource.Metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("native metric %q not found", name)
	return otlp.Metric{}
}

func TestAppNativeSignalsFollowExplicitSwitch(t *testing.T) {
	off := buildApp(t, appNativeConfig(false, nil))
	if slices.Contains(off.Signals(), core.OTLPMetrics) {
		t.Fatal("absent otel.metrics switch must not declare native metrics")
	}

	on := buildApp(t, appNativeConfig(true, nil))
	if !slices.Contains(on.Signals(), core.OTLPMetrics) {
		t.Fatal("otel.metrics=true must declare native metrics")
	}

	cfg := appNativeConfig(false, nil)
	cfg.OTel = &OTelObs{Metrics: false}
	if slices.Contains(buildApp(t, cfg).Signals(), core.OTLPMetrics) {
		t.Fatal("otel.metrics=false must not declare native metrics")
	}
}

func TestAppNativeInlineMetricsPreserveShapeAndIdentityBoundary(t *testing.T) {
	w := buildApp(t, appNativeConfig(true, nil))
	native := &appNativeMetricCapture{}
	mc := &coretest.MetricCapture{}
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	if err := w.Tick(context.Background(), now, appNativeWorld(mc, native)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(native.resources) != 1 {
		t.Fatalf("native resources = %d, want one node resource", len(native.resources))
	}
	resource := native.resources[0]
	if got := resource.Attrs["service.name"]; got != "api" {
		t.Errorf("service.name = %v, want api", got)
	}
	if got := resource.Attrs["k8s.cluster.name"]; got != "test-prod-use1" {
		t.Errorf("k8s.cluster.name = %v, want test-prod-use1", got)
	}
	if got := resource.Attrs["deployment.environment.name"]; got != "prod" {
		t.Errorf("deployment.environment.name = %v, want prod", got)
	}

	if got := []string{resource.Metrics[0].Name, resource.Metrics[1].Name, resource.Metrics[2].Name}; !reflect.DeepEqual(got, []string{"app_queue_depth", "app_requests_total", "app_request_duration"}) {
		t.Fatalf("native metric order = %v, want declaration order", got)
	}
	gauge := appNativeMetric(t, resource, "app_queue_depth")
	if gauge.Kind != otlp.MetricGauge || gauge.Unit != "{item}" || len(gauge.Numbers) != 1 {
		t.Fatalf("gauge shape = %+v", gauge)
	}
	if !gauge.Numbers[0].Start.IsZero() || gauge.Numbers[0].Value != 3 {
		t.Errorf("gauge point = %+v, want value 3 and no start", gauge.Numbers[0])
	}

	counter := appNativeMetric(t, resource, "app_requests_total")
	if counter.Kind != otlp.MetricSum || !counter.Monotonic || counter.Temporality != otlp.TemporalityCumulative || counter.Unit != "{request}" {
		t.Fatalf("counter shape = %+v", counter)
	}
	if len(counter.Numbers) != 1 || counter.Numbers[0].Value != 2 || !counter.Numbers[0].Start.Equal(now) {
		t.Errorf("counter point = %+v, want cumulative value 2 and start %s", counter.Numbers, now)
	}

	hist := appNativeMetric(t, resource, "app_request_duration")
	if hist.Kind != otlp.MetricHistogram || hist.Temporality != otlp.TemporalityCumulative || hist.Unit != "s" || len(hist.Histograms) != 1 {
		t.Fatalf("histogram shape = %+v", hist)
	}
	hp := hist.Histograms[0]
	if !hp.Start.Equal(now) || hp.Count != 1 || hp.Sum != 0.75 || !reflect.DeepEqual(hp.Bounds, []float64{0.5, 1}) || !reflect.DeepEqual(hp.BucketCounts, []uint64{0, 1, 0}) {
		t.Errorf("histogram point = %+v", hp)
	}
	if got := hp.Attrs["zone"]; got != "blue" {
		t.Errorf("DSL label zone = %v, want blue", got)
	}
	for _, key := range []string{"service", "service_name", "namespace", "cluster", "job"} {
		if _, ok := hp.Attrs[key]; ok {
			t.Errorf("identity label %q leaked into native datapoint attrs", key)
		}
	}

	// Counter/histogram state is cumulative and keeps the same native start across ticks.
	native2 := &appNativeMetricCapture{}
	if err := w.Tick(context.Background(), now.Add(time.Minute), appNativeWorld(nil, native2)); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	second := appNativeMetric(t, native2.resources[0], "app_requests_total")
	if second.Numbers[0].Value != 4 || !second.Numbers[0].Start.Equal(now) {
		t.Errorf("second counter point = %+v, want value 4 with stable start %s", second.Numbers[0], now)
	}
}

func TestAppNativeProfileMetricsRemainPromrwOnly(t *testing.T) {
	w := buildApp(t, appNativeConfig(true, []string{"runtime_go"}))
	native := &appNativeMetricCapture{}
	mc := &coretest.MetricCapture{}
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	if err := w.Tick(context.Background(), now, appNativeWorld(mc, native)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(native.resources) != 1 {
		t.Fatalf("native resources = %d, want one resource for inline metrics", len(native.resources))
	}
	for _, metric := range native.resources[0].Metrics {
		if metric.Name == "process_cpu_seconds_total" || metric.Name == "go_goroutines" {
			t.Fatalf("catalog profile metric %q was re-emitted as native", metric.Name)
		}
	}
	if len(mc.Find("process_cpu_seconds_total")) == 0 {
		t.Fatal("catalog profile metric did not remain on promrw")
	}
}

func TestAppNativeNilWriterIsSafe(t *testing.T) {
	w := buildApp(t, appNativeConfig(true, nil))
	if err := w.Tick(context.Background(), time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC), &core.World{Shape: shape.New("", nil)}); err != nil {
		t.Fatalf("Tick with nil writers: %v", err)
	}
}

func TestAppNativeSwitchLeavesPromrwPayloadUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	off := buildApp(t, appNativeConfig(false, nil))
	offCapture := &coretest.MetricCapture{}
	if err := off.Tick(context.Background(), now, appNativeWorld(offCapture, nil)); err != nil {
		t.Fatalf("off Tick: %v", err)
	}

	on := buildApp(t, appNativeConfig(true, nil))
	onCapture := &coretest.MetricCapture{}
	if err := on.Tick(context.Background(), now, appNativeWorld(onCapture, &appNativeMetricCapture{})); err != nil {
		t.Fatalf("on Tick: %v", err)
	}
	offSeries, onSeries := offCapture.All(), onCapture.All()
	for _, series := range [][]promrw.Series{offSeries, onSeries} {
		sort.Slice(series, func(i, j int) bool {
			left := series[i].Name + "|" + state.LabelSig(series[i].Labels)
			right := series[j].Name + "|" + state.LabelSig(series[j].Labels)
			return left < right
		})
	}
	if !reflect.DeepEqual(offSeries, onSeries) {
		t.Fatalf("enabling native OTLP changed the Prometheus payload\noff=%#v\non=%#v", offSeries, onSeries)
	}
}

// Keep the test's type assertion explicit: native OTLP is a distinct writer seam from promrw.
var _ core.OTLPMetricWriter = (*appNativeMetricCapture)(nil)
var _ core.MetricWriter = (*coretest.MetricCapture)(nil)
