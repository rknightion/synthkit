// SPDX-License-Identifier: AGPL-3.0-only

package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// decodeResourceLogs gunzips the captured body, walks the ExportLogsServiceRequest envelope
// (field 1, repeated LEN ResourceLogs) and unmarshals each. Deliberately self-contained: the
// collector/logs/v1 package is not importable here (grpc-gateway deps absent from go.sum), so
// the envelope is the only thing proving the hand-encoding is wire-correct.
func decodeResourceLogs(t *testing.T, body []byte) []*logspb.ResourceLogs {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var out []*logspb.ResourceLogs
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 || num != 1 || typ != protowire.BytesType {
			t.Fatalf("unexpected envelope tag num=%d typ=%d", num, typ)
		}
		raw = raw[n:]
		v, n := protowire.ConsumeBytes(raw)
		if n < 0 {
			t.Fatal("bad LEN record")
		}
		raw = raw[n:]
		rl := &logspb.ResourceLogs{}
		if err := proto.Unmarshal(v, rl); err != nil {
			t.Fatal(err)
		}
		out = append(out, rl)
	}
	return out
}

// postLogs ships resources through a real (non-dry-run) sink and returns the decoded blocks.
func postLogs(t *testing.T, resources []LogResource) []*logspb.ResourceLogs {
	t.Helper()
	var body []byte
	var gotPath, gotEncoding, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		body, gotPath = b, r.URL.Path
		gotEncoding, gotType = r.Header.Get("Content-Encoding"), r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewLogs(srv.URL, "user", "token", false)
	if err := s.Write(context.Background(), resources); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if gotPath != "/v1/logs" {
		t.Errorf("path = %q, want /v1/logs", gotPath)
	}
	if gotEncoding != "gzip" || gotType != "application/x-protobuf" {
		t.Errorf("headers: encoding=%q type=%q", gotEncoding, gotType)
	}
	return decodeResourceLogs(t, body)
}

// podLogResource is the shape the k8s-monitoring podLogsViaOpenTelemetry transport really sends
// (reality-corpus/k8s/k3d-lab.json, 2026-08-25): k8s + service identity on the resource,
// log.iostream/logtag on the record.
func podLogResource(ts time.Time) LogResource {
	return LogResource{
		Attrs: map[string]any{
			"cluster":                "lab",
			"k8s.cluster.name":       "lab",
			"k8s.namespace.name":     "prod",
			"k8s.pod.name":           "api-7d9f-abcde",
			"k8s.container.name":     "api",
			"k8s.deployment.name":    "api",
			"k8s.node.name":          "node-1",
			"app_kubernetes_io_name": "api",
			"service.name":           "api",
			"service.namespace":      "prod",
			"service.instance.id":    "prod.api-7d9f-abcde.api",
		},
		Records: []LogRecord{{
			Time:         ts,
			ObservedTime: ts,
			Body:         `level=info msg="request handled"`,
			Attrs:        map[string]any{"log.iostream": "stdout", "logtag": "F"},
		}},
	}
}

func TestLogsWriteEncodesResourceAndRecordAttrs(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	rls := postLogs(t, []LogResource{podLogResource(ts)})

	if len(rls) != 1 {
		t.Fatalf("want 1 ResourceLogs, got %d", len(rls))
	}
	res := map[string]string{}
	for _, kv := range rls[0].GetResource().GetAttributes() {
		res[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	// The dotted/flat mix is deliberate: it is what the collector really sends.
	for k, want := range map[string]string{
		"k8s.pod.name":           "api-7d9f-abcde",
		"k8s.cluster.name":       "lab",
		"cluster":                "lab",
		"app_kubernetes_io_name": "api",
		"service.instance.id":    "prod.api-7d9f-abcde.api",
	} {
		if res[k] != want {
			t.Errorf("resource attr %q = %q, want %q", k, res[k], want)
		}
	}
	if len(res) != 11 {
		t.Errorf("resource attr count = %d, want 11", len(res))
	}

	sl := rls[0].GetScopeLogs()
	if len(sl) != 1 || len(sl[0].GetLogRecords()) != 1 {
		t.Fatalf("want 1 scope with 1 record, got %v", sl)
	}
	rec := sl[0].GetLogRecords()[0]
	if got := rec.GetBody().GetStringValue(); got != `level=info msg="request handled"` {
		t.Errorf("body = %q", got)
	}
	if rec.GetTimeUnixNano() != uint64(ts.UnixNano()) || rec.GetObservedTimeUnixNano() != uint64(ts.UnixNano()) {
		t.Errorf("timestamps: time=%d observed=%d want %d", rec.GetTimeUnixNano(), rec.GetObservedTimeUnixNano(), ts.UnixNano())
	}
	recAttrs := map[string]string{}
	for _, kv := range rec.GetAttributes() {
		recAttrs[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	if len(recAttrs) != 2 || recAttrs["log.iostream"] != "stdout" || recAttrs["logtag"] != "F" {
		t.Errorf("record attrs = %v, want exactly log.iostream=stdout logtag=F", recAttrs)
	}
	// No severity parser in the captured pipeline ⇒ nothing on the wire.
	if rec.GetSeverityNumber() != logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED || rec.GetSeverityText() != "" {
		t.Errorf("severity leaked: number=%v text=%q", rec.GetSeverityNumber(), rec.GetSeverityText())
	}
}

// TestLogsScopeOmittedWhenUnnamed pins the deliberate divergence from the traces/metrics lanes:
// no "synthkit" fallback scope, because a real filelog pod log carries an empty scope and Loki
// would otherwise mint a scope_name structured-metadata key the corpus does not contain.
func TestLogsScopeOmittedWhenUnnamed(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	rls := postLogs(t, []LogResource{podLogResource(ts)})
	if scope := rls[0].GetScopeLogs()[0].GetScope(); scope != nil {
		t.Errorf("unnamed scope must be omitted, got %q/%q", scope.GetName(), scope.GetVersion())
	}
}

// TestLogsScopeCarriedWhenNamed verifies an emitter that DOES name a scope still gets one
// (application logs from an instrumented SDK, unlike scraped pod logs).
func TestLogsScopeCarriedWhenNamed(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	r := podLogResource(ts)
	r.Scope = Scope{Name: "go.opentelemetry.io/contrib/bridges/otelslog", Version: "0.9.0"}
	rls := postLogs(t, []LogResource{r})
	scope := rls[0].GetScopeLogs()[0].GetScope()
	if scope.GetName() != "go.opentelemetry.io/contrib/bridges/otelslog" || scope.GetVersion() != "0.9.0" {
		t.Errorf("scope = %q/%q", scope.GetName(), scope.GetVersion())
	}
}

func TestLogsSeverityBandsReachTheWire(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		sev      Severity
		wantNum  logspb.SeverityNumber
		wantText string
	}{
		{SeverityDebug, logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG, "DEBUG"},
		{SeverityInfo, logspb.SeverityNumber_SEVERITY_NUMBER_INFO, "INFO"},
		{SeverityWarn, logspb.SeverityNumber_SEVERITY_NUMBER_WARN, "WARN"},
		{SeverityError, logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, "ERROR"},
		{SeverityFatal, logspb.SeverityNumber_SEVERITY_NUMBER_FATAL, "FATAL"},
	} {
		r := podLogResource(ts)
		r.Records[0].Severity = tc.sev
		rec := postLogs(t, []LogResource{r})[0].GetScopeLogs()[0].GetLogRecords()[0]
		if rec.GetSeverityNumber() != tc.wantNum {
			t.Errorf("severity %d: number = %v, want %v", tc.sev, rec.GetSeverityNumber(), tc.wantNum)
		}
		if rec.GetSeverityText() != tc.wantText {
			t.Errorf("severity %d: text = %q, want %q", tc.sev, rec.GetSeverityText(), tc.wantText)
		}
	}
}

func TestLogsTraceCorrelationEncoded(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	r := podLogResource(ts)
	r.Records[0].TraceID = "0123456789abcdef0123456789abcdef"
	r.Records[0].SpanID = "0123456789abcdef"
	rec := postLogs(t, []LogResource{r})[0].GetScopeLogs()[0].GetLogRecords()[0]
	if got := hex.EncodeToString(rec.GetTraceId()); got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("trace id = %q", got)
	}
	if got := hex.EncodeToString(rec.GetSpanId()); got != "0123456789abcdef" {
		t.Errorf("span id = %q", got)
	}
}

// TestLogsMalformedCorrelationKeepsTheRecord is the both-transports-carry-identical-content
// guarantee at the sink: a bad ID costs the correlation field, never the log line.
func TestLogsMalformedCorrelationKeepsTheRecord(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	r := podLogResource(ts)
	r.Records[0].TraceID = "not-hex"
	r.Records[0].SpanID = "abcd" // too short
	rls := postLogs(t, []LogResource{r})
	recs := rls[0].GetScopeLogs()[0].GetLogRecords()
	if len(recs) != 1 {
		t.Fatalf("record dropped: got %d records, want 1", len(recs))
	}
	if len(recs[0].GetTraceId()) != 0 || len(recs[0].GetSpanId()) != 0 {
		t.Errorf("malformed ids must be omitted, got trace=%x span=%x", recs[0].GetTraceId(), recs[0].GetSpanId())
	}
	if recs[0].GetBody().GetStringValue() == "" {
		t.Error("record lost its body")
	}
}

func TestLogsMultiResourceEnvelope(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	a, b := podLogResource(ts), podLogResource(ts)
	b.Attrs["k8s.pod.name"] = "api-7d9f-fghij"
	rls := postLogs(t, []LogResource{a, b})
	if len(rls) != 2 {
		t.Fatalf("want 2 ResourceLogs blocks, got %d", len(rls))
	}
}

func TestLogsWriteEmptyIsNoop(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := NewLogs(srv.URL, "u", "t", false)
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if called {
		t.Error("empty Write must not hit the network")
	}
}

// TestLogsDryRunInventorySplitsAttrs is the point of this lane's inventory: resource attributes
// (the destination's promotion candidates) are kept apart from record attributes (always
// structured metadata). Merging them would erase the distinction the corpus is checked against.
func TestLogsDryRunInventorySplitsAttrs(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	s := NewLogs("http://unused", "u", "t", true)
	s.Capture = true
	s.Quiet = true
	if err := s.Write(context.Background(), []LogResource{podLogResource(ts)}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	resAttrs, recAttrs := s.Inventory()
	if got := len(resAttrs["api"]); got != 11 {
		t.Errorf("resource attrs for service api = %d (%v), want 11", got, resAttrs["api"])
	}
	want := []string{"log.iostream", "logtag"}
	got := recAttrs["api"]
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("record attrs = %v, want %v", got, want)
	}
	for _, k := range want {
		for _, r := range resAttrs["api"] {
			if r == k {
				t.Errorf("record attr %q leaked into the resource inventory", k)
			}
		}
	}
	if n := len(s.Captured()); n != 1 {
		t.Errorf("Captured() = %d resources, want 1", n)
	}
}

func TestLogsObserveDryRun(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	s := NewLogs("http://unused", "u", "t", true)
	s.Quiet = true
	var got pushhook.Event
	s.Observe = func(_ context.Context, e pushhook.Event) { got = e }
	if err := s.Write(context.Background(), []LogResource{podLogResource(ts)}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got.Sink != "otlplogs" || got.Items != 1 || !got.DryRun {
		t.Errorf("push event = %+v, want sink=otlplogs items=1 dryRun=true", got)
	}
}

// TestLogsBlueprintRecoveredFromStamp mirrors the traces/metrics lanes: the scoped writer stamps
// the blueprint as a resource attribute and the sink reports it on the push event.
func TestLogsBlueprintRecoveredFromStamp(t *testing.T) {
	ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	r := podLogResource(ts)
	r.Attrs["blueprint"] = "k8s-logs-events"
	s := NewLogs("http://unused", "u", "t", true)
	s.Quiet = true
	var got pushhook.Event
	s.Observe = func(_ context.Context, e pushhook.Event) { got = e }
	if err := s.Write(context.Background(), []LogResource{r}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got.Blueprint != "k8s-logs-events" {
		t.Errorf("blueprint = %q, want k8s-logs-events", got.Blueprint)
	}
}
