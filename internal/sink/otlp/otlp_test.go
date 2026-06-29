// SPDX-License-Identifier: AGPL-3.0-only

package otlp

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// TestWriteGzipsTraces verifies the OTLP/HTTP trace export is gzip-compressed with
// Content-Encoding: gzip (Content-Type stays application/x-protobuf), and the
// decompressed body still decodes to the ExportTraceServiceRequest envelope.
// Grafana Cloud's OTLP gateway accepts gzip — this cuts trace transfer ~3-8x.
func TestWriteGzipsTraces(t *testing.T) {
	var gotEncoding, gotType string
	var firstSpanName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		gotType = r.Header.Get("Content-Type")
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("body is not valid gzip: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(zr)
		// Decode the hand-rolled envelope: repeated field 1 (LEN) of ResourceSpans.
		b := raw
		for len(b) > 0 {
			num, typ, n := protowire.ConsumeTag(b)
			if n < 0 || num != 1 || typ != protowire.BytesType {
				t.Errorf("unexpected envelope tag num=%d typ=%d n=%d", num, typ, n)
				break
			}
			b = b[n:]
			rsBytes, n := protowire.ConsumeBytes(b)
			if n < 0 {
				t.Errorf("bad ResourceSpans length prefix")
				break
			}
			b = b[n:]
			var rs tracepb.ResourceSpans
			if err := proto.Unmarshal(rsBytes, &rs); err != nil {
				t.Errorf("ResourceSpans unmarshal: %v", err)
				break
			}
			if firstSpanName == "" && len(rs.ScopeSpans) > 0 && len(rs.ScopeSpans[0].Spans) > 0 {
				firstSpanName = rs.ScopeSpans[0].Spans[0].Name
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "frontend"},
		Spans: []Span{{
			Name: "http.request", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
			Start: now, End: now.Add(time.Millisecond), Kind: KindClient, Status: StatusOK,
		}},
	}}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if gotEncoding != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", gotEncoding)
	}
	if gotType != "application/x-protobuf" {
		t.Errorf("Content-Type = %q, want application/x-protobuf", gotType)
	}
	if firstSpanName != "http.request" {
		t.Errorf("decompressed traces did not round-trip, first span = %q", firstSpanName)
	}
}

// TestWriteMultiResource verifies that multiple Resource blocks in a single Write
// call all appear as separate ResourceSpans in the envelope.
func TestWriteMultiResource(t *testing.T) {
	var gotResourceCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("body is not valid gzip: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(zr)
		b := raw
		for len(b) > 0 {
			num, typ, n := protowire.ConsumeTag(b)
			if n < 0 || num != 1 || typ != protowire.BytesType {
				break
			}
			b = b[n:]
			_, n = protowire.ConsumeBytes(b)
			if n < 0 {
				break
			}
			b = b[n:]
			gotResourceCount++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	now := time.Now()
	res := []Resource{
		{
			Attrs: map[string]any{"service.name": "frontend"},
			Spans: []Span{{
				Name: "page.load", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
				Start: now, End: now.Add(time.Millisecond),
			}},
		},
		{
			Attrs: map[string]any{"service.name": "backend"},
			Spans: []Span{{
				Name: "http.handler", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "fedcba9876543210",
				ParentID: "0123456789abcdef",
				Start:    now, End: now.Add(time.Millisecond),
			}},
		},
	}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if gotResourceCount != 2 {
		t.Errorf("envelope contained %d ResourceSpans, want 2", gotResourceCount)
	}
}

// TestWriteSkipsInvalidSpans verifies spans with invalid trace/span IDs are
// silently skipped and the rest of the write succeeds.
func TestWriteSkipsInvalidSpans(t *testing.T) {
	var gotSpanCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(zr)
		b := raw
		for len(b) > 0 {
			num, typ, n := protowire.ConsumeTag(b)
			if n < 0 || num != 1 || typ != protowire.BytesType {
				break
			}
			b = b[n:]
			rsBytes, n := protowire.ConsumeBytes(b)
			if n < 0 {
				break
			}
			b = b[n:]
			var rs tracepb.ResourceSpans
			if err := proto.Unmarshal(rsBytes, &rs); err != nil {
				continue
			}
			for _, ss := range rs.ScopeSpans {
				gotSpanCount += len(ss.Spans)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "svc"},
		Spans: []Span{
			{Name: "valid", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef", Start: now, End: now.Add(time.Millisecond)},
			{Name: "bad-trace", TraceID: "notHex!", SpanID: "0123456789abcdef", Start: now, End: now.Add(time.Millisecond)},
			{Name: "empty-span", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "", Start: now, End: now.Add(time.Millisecond)},
		},
	}}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if gotSpanCount != 1 {
		t.Errorf("server received %d span(s), want 1 (invalid spans skipped)", gotSpanCount)
	}
}

// TestDryRunInventory verifies that dry-run records resource attrs, span names, and
// span attrs per service.name, and Inventory returns sorted slices.
func TestDryRunInventory(t *testing.T) {
	s := New("http://unused", "u", "tok", true)
	now := time.Now()
	res := []Resource{
		{
			Attrs: map[string]any{"service.name": "svc-a", "deployment.environment": "prod"},
			Spans: []Span{
				{Name: "op.one", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
					Start: now, End: now.Add(time.Millisecond), Attrs: map[string]any{"http.method": "GET"}},
				{Name: "op.two", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "fedcba9876543210",
					Start: now, End: now.Add(time.Millisecond), Attrs: map[string]any{"db.statement": "SELECT 1"}},
			},
		},
		{
			Attrs: map[string]any{"service.name": "svc-b"},
			Spans: []Span{
				{Name: "op.three", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "1111111111111111",
					Start: now, End: now.Add(time.Millisecond)},
			},
		},
	}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("Write (dry-run): %v", err)
	}

	resAttrs, spanNames, spanAttrs := s.Inventory()

	// svc-a resource attrs
	if got := resAttrs["svc-a"]; len(got) != 2 {
		t.Errorf("svc-a resAttrs = %v, want 2 keys", got)
	}
	// svc-a span names should be sorted
	if want := []string{"op.one", "op.two"}; len(spanNames["svc-a"]) != 2 ||
		spanNames["svc-a"][0] != want[0] || spanNames["svc-a"][1] != want[1] {
		t.Errorf("svc-a spanNames = %v, want %v", spanNames["svc-a"], want)
	}
	// svc-a span attrs
	if len(spanAttrs["svc-a"]) != 2 {
		t.Errorf("svc-a spanAttrs = %v, want 2 keys", spanAttrs["svc-a"])
	}
	// svc-b
	if got := spanNames["svc-b"]; len(got) != 1 || got[0] != "op.three" {
		t.Errorf("svc-b spanNames = %v, want [op.three]", got)
	}
}

// TestWriteEmptyIsNoop verifies Write([]) returns nil without any HTTP calls.
func TestWriteEmptyIsNoop(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
	}))
	defer srv.Close()
	s := New(srv.URL, "u", "tok", false)
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	if err := s.Write(context.Background(), []Resource{}); err != nil {
		t.Fatalf("Write([]): %v", err)
	}
	if posts != 0 {
		t.Errorf("posts = %d, want 0 (empty write is noop)", posts)
	}
}

// ---------------------------------------------------------------------------
// Observe hook
// ---------------------------------------------------------------------------

func TestObserveDryRun(t *testing.T) {
	s := New("http://unused", "u", "tok", true)
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "frontend"},
		Spans: []Span{
			{Name: "span.one", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef", Start: now, End: now.Add(time.Millisecond)},
			{Name: "span.two", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "fedcba9876543210", Start: now, End: now.Add(time.Millisecond)},
		},
	}}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("dry-run Write: %v", err)
	}
	if got.Sink != "otlp" {
		t.Errorf("Sink = %q, want otlp", got.Sink)
	}
	if got.Items != 2 {
		t.Errorf("Items = %d, want 2 (span count)", got.Items)
	}
	if !got.DryRun {
		t.Errorf("DryRun = false, want true")
	}
	if got.Blueprint != "" {
		t.Errorf("Blueprint = %q, want empty (otlp has no blueprint label)", got.Blueprint)
	}
}

func TestObserveNilSafe(t *testing.T) {
	// No Observe set: must not panic and inventory still recorded.
	s := New("http://unused", "u", "tok", true)
	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "svc"},
		Spans: []Span{{
			Name: "op", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
			Start: now, End: now.Add(time.Millisecond),
		}},
	}}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("nil-observer Write: %v", err)
	}
	resAttrs, _, _ := s.Inventory()
	if len(resAttrs) == 0 {
		t.Fatalf("inventory not recorded with nil observer")
	}
}

func TestObserveLivePushSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "frontend"},
		Spans: []Span{{
			Name: "http.request", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
			Start: now, End: now.Add(time.Millisecond),
		}},
	}}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got.Sink != "otlp" || got.Status != 200 || got.Err != nil || got.Items != 1 {
		t.Fatalf("unexpected event: %+v", got)
	}
	if got.Bytes <= 0 {
		t.Fatalf("Bytes should be >0 (gzip body), got %d", got.Bytes)
	}
	if got.DryRun {
		t.Errorf("DryRun should be false on a live push")
	}
}

// TestWriteSurfacesHTTPError verifies non-2xx HTTP status is returned as an error.
func TestWriteSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	s := New(srv.URL, "u", "tok", false)
	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "svc"},
		Spans: []Span{{
			Name: "span", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
			Start: now, End: now.Add(time.Millisecond),
		}},
	}}
	err := s.Write(context.Background(), res)
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
}

// ---------------------------------------------------------------------------
// convertSpans ID-length guards (L1 fix)
// ---------------------------------------------------------------------------

// TestConvertSpansRejectsShortTraceID verifies that spans whose TraceID decodes to
// fewer than 16 bytes are skipped — only the valid 32-hex-char (16-byte) TraceID passes.
func TestConvertSpansRejectsShortTraceID(t *testing.T) {
	// Short but valid hex: 30 chars = 15 bytes → must be skipped.
	shortTraceID := "0123456789abcdef0123456789abcd"   // 30 chars
	validTraceID := "0123456789abcdef0123456789abcdef" // 32 chars
	validSpanID := "0123456789abcdef"                  // 16 chars

	var spansReceived int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(zr)
		b := raw
		for len(b) > 0 {
			num, typ, n := protowire.ConsumeTag(b)
			if n < 0 || num != 1 || typ != protowire.BytesType {
				break
			}
			b = b[n:]
			rsBytes, n := protowire.ConsumeBytes(b)
			if n < 0 {
				break
			}
			b = b[n:]
			var rs tracepb.ResourceSpans
			if err := proto.Unmarshal(rsBytes, &rs); err != nil {
				continue
			}
			for _, ss := range rs.ScopeSpans {
				atomic.AddInt64(&spansReceived, int64(len(ss.Spans)))
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "svc"},
		Spans: []Span{
			{Name: "short-trace", TraceID: shortTraceID, SpanID: validSpanID, Start: now, End: now.Add(time.Millisecond)},
			{Name: "valid", TraceID: validTraceID, SpanID: validSpanID, Start: now, End: now.Add(time.Millisecond)},
		},
	}}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := atomic.LoadInt64(&spansReceived); got != 1 {
		t.Errorf("server received %d span(s), want 1 (short TraceID must be skipped)", got)
	}
}

// TestConvertSpansRejectsShortSpanID verifies that spans whose SpanID decodes to fewer
// than 8 bytes are skipped — only the valid 16-hex-char (8-byte) SpanID passes.
func TestConvertSpansRejectsShortSpanID(t *testing.T) {
	validTraceID := "0123456789abcdef0123456789abcdef" // 32 chars
	shortSpanID := "0123456789abcd"                    // 14 chars = 7 bytes → skip
	validSpanID := "0123456789abcdef"                  // 16 chars = 8 bytes → keep

	var spansReceived int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(zr)
		b := raw
		for len(b) > 0 {
			num, typ, n := protowire.ConsumeTag(b)
			if n < 0 || num != 1 || typ != protowire.BytesType {
				break
			}
			b = b[n:]
			rsBytes, n := protowire.ConsumeBytes(b)
			if n < 0 {
				break
			}
			b = b[n:]
			var rs tracepb.ResourceSpans
			if err := proto.Unmarshal(rsBytes, &rs); err != nil {
				continue
			}
			for _, ss := range rs.ScopeSpans {
				atomic.AddInt64(&spansReceived, int64(len(ss.Spans)))
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "svc"},
		Spans: []Span{
			{Name: "short-span", TraceID: validTraceID, SpanID: shortSpanID, Start: now, End: now.Add(time.Millisecond)},
			{Name: "valid", TraceID: validTraceID, SpanID: validSpanID, Start: now, End: now.Add(time.Millisecond)},
		},
	}}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := atomic.LoadInt64(&spansReceived); got != 1 {
		t.Errorf("server received %d span(s), want 1 (short SpanID must be skipped)", got)
	}
}

// ---------------------------------------------------------------------------
// Retry: 503 → success; 500 → no retry (OTLP spec)
// ---------------------------------------------------------------------------

// TestOTLPRetriesOn503 proves the otlp live path retries on 503 and succeeds.
func TestOTLPRetriesOn503(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "svc"},
		Spans: []Span{{
			Name: "op", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
			Start: now, End: now.Add(time.Millisecond),
		}},
	}}
	if err := s.Write(context.Background(), res); err != nil {
		t.Fatalf("expected success after 503 retry, got: %v", err)
	}
	if n := atomic.LoadInt64(&calls); n < 2 {
		t.Errorf("calls = %d, want ≥2 (first 503 + at least one retry)", n)
	}
}

// TestOTLPDoesNotRetryOn500 proves 500 is NOT retried by the OTLP policy.
// Per the OTLP spec, 500 is a permanent server-side failure for OTLP exporters.
func TestOTLPDoesNotRetryOn500(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	now := time.Now()
	res := []Resource{{
		Attrs: map[string]any{"service.name": "svc"},
		Spans: []Span{{
			Name: "op", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
			Start: now, End: now.Add(time.Millisecond),
		}},
	}}
	err := s.Write(context.Background(), res)
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if n := atomic.LoadInt64(&calls); n != 1 {
		t.Errorf("calls = %d, want exactly 1 (500 must not be retried per OTLP spec)", n)
	}
}

// ---------------------------------------------------------------------------
// InstrumentationScope stamping
// ---------------------------------------------------------------------------

// firstResourceSpans gunzips the request body, walks the hand-rolled
// ExportTraceServiceRequest envelope (repeated field 1, wire-type LEN), and returns
// the first ResourceSpans decoded from the first record. Fatal on any parse error.
func firstResourceSpans(t *testing.T, r *http.Request) *tracepb.ResourceSpans {
	t.Helper()
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatalf("firstResourceSpans: gzip: %v", err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("firstResourceSpans: read: %v", err)
	}
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 || num != 1 || typ != protowire.BytesType {
			t.Fatalf("firstResourceSpans: unexpected envelope tag num=%d typ=%d", num, typ)
		}
		b = b[n:]
		rsBytes, n := protowire.ConsumeBytes(b)
		if n < 0 {
			t.Fatalf("firstResourceSpans: bad length prefix")
		}
		var rs tracepb.ResourceSpans
		if err := proto.Unmarshal(rsBytes, &rs); err != nil {
			t.Fatalf("firstResourceSpans: unmarshal: %v", err)
		}
		return &rs
	}
	t.Fatal("firstResourceSpans: empty envelope")
	return nil
}

// TestWriteStampsInstrumentationScope verifies that a non-zero Resource.Scope is
// forwarded as the OTLP InstrumentationScope name+version on the wire.
func TestWriteStampsInstrumentationScope(t *testing.T) {
	var got *tracepb.ResourceSpans
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = firstResourceSpans(t, r)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	s := New(srv.URL, "u", "t", false)
	res := Resource{
		Attrs: map[string]any{"service.name": "svc"},
		Scope: Scope{Name: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", Version: "0.58.0"},
		Spans: []Span{{Name: "GET /", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef", Kind: KindServer, Start: time.Unix(1, 0), End: time.Unix(2, 0)}},
	}
	if err := s.Write(context.Background(), []Resource{res}); err != nil {
		t.Fatal(err)
	}
	sc := got.ScopeSpans[0].Scope
	if sc.Name != "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp" || sc.Version != "0.58.0" {
		t.Errorf("scope = %q/%q, want otelhttp/0.58.0", sc.Name, sc.Version)
	}
}

// TestWriteScopeDefaultsToSynthkit verifies that a Resource with zero Scope falls back
// to the legacy "synthkit" scope name (back-compat: existing callers set no Scope).
func TestWriteScopeDefaultsToSynthkit(t *testing.T) {
	var got *tracepb.ResourceSpans
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = firstResourceSpans(t, r)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	s := New(srv.URL, "u", "t", false)
	res := Resource{
		Attrs: map[string]any{"service.name": "svc"},
		// Scope intentionally omitted — zero value
		Spans: []Span{{Name: "op", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef", Start: time.Unix(1, 0), End: time.Unix(2, 0)}},
	}
	if err := s.Write(context.Background(), []Resource{res}); err != nil {
		t.Fatal(err)
	}
	sc := got.ScopeSpans[0].Scope
	if sc.Name != "synthkit" {
		t.Errorf("scope.Name = %q, want \"synthkit\" (default fallback)", sc.Name)
	}
	if sc.Version != "" {
		t.Errorf("scope.Version = %q, want \"\" (zero scope has no version)", sc.Version)
	}
}

func TestConvertSpansSkipsMalformedParentID(t *testing.T) {
	now := time.Now()
	in := []Span{
		// 4-byte ParentID ("deadbeef" = 4 bytes) — not a valid 8-byte span id → must be skipped.
		{Name: "bad-parent", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "1111111111111111",
			ParentID: "deadbeef", Kind: KindClient, Start: now, End: now.Add(time.Millisecond)},
		// valid (8-byte ParentID) → kept.
		{Name: "ok", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "2222222222222222",
			ParentID: "1111111111111111", Kind: KindClient, Start: now, End: now.Add(time.Millisecond)},
		// root (empty ParentID) → kept.
		{Name: "root", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "3333333333333333",
			Kind: KindServer, Start: now, End: now.Add(time.Millisecond)},
	}
	out, skipped := convertSpans(in)
	if skipped != 1 {
		t.Fatalf("skipped=%d, want 1 (the malformed-ParentID span)", skipped)
	}
	if len(out) != 2 {
		t.Fatalf("len(out)=%d, want 2 (valid + root kept)", len(out))
	}
	for _, sp := range out {
		if string(sp.SpanId) == "" {
			t.Fatal("kept a span with empty SpanId")
		}
	}
}
