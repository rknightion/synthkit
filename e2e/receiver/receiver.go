// SPDX-License-Identifier: AGPL-3.0-only

// Package receiver is the e2e sidecar: it decodes every synthkit egress lane (RW2 metrics,
// OTLP traces, OTLP metrics, Loki logs) into an inventory.Schema for -dump correlation. It is
// a TEST harness — not on the synthetic-data path — so it may import the sinks + otlp proto.
package receiver

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"sync"

	"github.com/golang/snappy"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/rknightion/synthkit/e2e/inventory"
	writev2 "github.com/rknightion/synthkit/internal/sink/promrw/writev2"
)

// Receiver accepts each synthkit egress lane over HTTP and accumulates the schema
// (metric names + label keys, log sources + stream-label keys, trace services + span names).
type Receiver struct {
	mu      sync.Mutex
	metrics map[string]map[string]bool // name → label-key set
	logs    map[string]map[string]bool // source → stream-key set
	traces  map[string]map[string]bool // service → span-name set
}

// New returns a zero-state Receiver ready to use.
func New() *Receiver {
	return &Receiver{
		metrics: map[string]map[string]bool{},
		logs:    map[string]map[string]bool{},
		traces:  map[string]map[string]bool{},
	}
}

// add merges vals into the set at m[k], creating it if absent.
func add(m map[string]map[string]bool, k string, vals ...string) {
	if m[k] == nil {
		m[k] = map[string]bool{}
	}
	for _, v := range vals {
		if v != "" {
			m[k][v] = true
		}
	}
}

// Handler returns an http.Handler routing all synthkit egress paths.
func (r *Receiver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/write", r.handleRW2)
	mux.HandleFunc("POST /v1/traces", r.handleTraces)
	mux.HandleFunc("POST /v1/metrics", r.handleOTLPMetrics)
	mux.HandleFunc("POST /loki/api/v1/push", r.handleLoki)
	mux.HandleFunc("GET /__inventory", r.handleInventory)
	return mux
}

// handleRW2 decodes a snappy-compressed writev2.Request body.
func (r *Receiver) handleRW2(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	raw, err := snappy.Decode(nil, body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var pb writev2.Request
	if err := proto.Unmarshal(raw, &pb); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sym := pb.Symbols
	for _, ts := range pb.Timeseries {
		var name string
		var keys []string
		refs := ts.LabelsRefs
		for i := 0; i+1 < len(refs); i += 2 {
			ni, vi := int(refs[i]), int(refs[i+1])
			if ni >= len(sym) || vi >= len(sym) {
				continue
			}
			k := sym[ni]
			if k == "__name__" {
				name = sym[vi]
			} else {
				keys = append(keys, k)
			}
		}
		if name != "" {
			add(r.metrics, name, keys...)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// gunzip decompresses the request body if Content-Encoding is gzip; otherwise returns raw bytes.
func gunzip(req *http.Request) ([]byte, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if req.Header.Get("Content-Encoding") != "gzip" {
		return body, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// handleTraces decodes the otlp sink's hand-rolled envelope:
// repeated field-1 LEN records, each a marshalled ResourceSpans.
// (The sink does NOT emit a TracesData / ExportTraceServiceRequest wrapper.)
func (r *Receiver) handleTraces(w http.ResponseWriter, req *http.Request) {
	raw, err := gunzip(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num != 1 || typ != protowire.BytesType {
			// Skip any unexpected field rather than bailing out.
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				break
			}
			b = b[skip:]
			continue
		}
		rsBytes, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		var rs tracepb.ResourceSpans
		if err := proto.Unmarshal(rsBytes, &rs); err != nil {
			continue
		}
		svc := "unknown"
		for _, a := range rs.GetResource().GetAttributes() {
			if a.GetKey() == "service.name" {
				svc = a.GetValue().GetStringValue()
				break
			}
		}
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				add(r.traces, svc, sp.GetName())
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleOTLPMetrics decodes the otlp metrics sink's hand-rolled envelope:
// repeated field-1 LEN records, each a marshalled ResourceMetrics (the sink hand-encodes an
// ExportMetricsServiceRequest the same way the traces sink does — metrics.go ~88). Decoding
// via the generated metricspb structs + getters means proto field numbers can never drift.
func (r *Receiver) handleOTLPMetrics(w http.ResponseWriter, req *http.Request) {
	raw, err := gunzip(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num != 1 || typ != protowire.BytesType {
			// Skip any unexpected field rather than bailing out.
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				break
			}
			b = b[skip:]
			continue
		}
		rmBytes, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		var rm metricspb.ResourceMetrics
		if err := proto.Unmarshal(rmBytes, &rm); err != nil {
			continue
		}
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				add(r.metrics, m.GetName())
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleLoki decodes a gzip+JSON Loki push body.
func (r *Receiver) handleLoki(w http.ResponseWriter, req *http.Request) {
	raw, err := gunzip(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var push struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(raw, &push); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range push.Streams {
		// Key EXACTLY as the loki sink's dry-run inventory does (loki.go: src :=
		// st.Labels["source"]) — strictly the "source" label, with "" when absent.
		// No service_name/job/"stream" fallback: a fallback would re-key the
		// source-less manifests stream (job=integrations/kubernetes/manifests) under
		// its service_name on this side while -dump keys it under "", so the two
		// inventories would never correlate. Mirroring the sink keeps both sides aligned.
		src := s.Stream["source"]
		keys := make([]string, 0, len(s.Stream))
		for k := range s.Stream {
			keys = append(keys, k)
		}
		add(r.logs, src, keys...)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Snapshot returns a point-in-time copy of the accumulated Schema.
func (r *Receiver) Snapshot() inventory.Schema {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := inventory.Schema{
		Metrics:    map[string][]string{},
		LogSources: map[string][]string{},
		Traces:     map[string][]string{},
	}
	flatten := func(src map[string]map[string]bool, dst map[string][]string) {
		for k, set := range src {
			vals := make([]string, 0, len(set))
			for v := range set {
				vals = append(vals, v)
			}
			sort.Strings(vals)
			dst[k] = vals
		}
	}
	flatten(r.metrics, out.Metrics)
	flatten(r.logs, out.LogSources)
	flatten(r.traces, out.Traces)
	return out
}

// handleInventory returns the accumulated Schema as JSON (GET /__inventory).
func (r *Receiver) handleInventory(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(r.Snapshot())
}
