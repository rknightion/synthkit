// SPDX-License-Identifier: AGPL-3.0-only

// Package loki pushes synthetic log streams to a Loki push endpoint using the
// 3-tuple form [ts, line, {structured metadata}]. Stream labels must be
// low-cardinality; the sink ASSERTS on every push that no forbidden key
// (UUID-class identifiers) appears in Stream.Labels — high-card identifiers ride
// in structured metadata instead. See ARCHITECTURE.md invariants I14/I15.
package loki

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/highcard"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// DefaultForbiddenStreamLabels is the set of keys that must NEVER be Loki stream labels
// (they must go in structured metadata instead). These are UUID-class high-cardinality
// keys that would explode stream cardinality on a production-shaped stack (I14/I15). It
// sources the canonical high-card set from internal/highcard so the Loki sink, the promrw
// sink, and the telemetryspec DSL capability matrix agree by construction.
var DefaultForbiddenStreamLabels = highcard.Fields()

// Sink pushes log streams to a Loki push endpoint.
type Sink struct {
	url      string
	auth     string
	hc       *http.Client
	dryRun   bool
	highCard map[string]struct{} // keys forbidden as stream labels

	// Observe, when non-nil, is called once per push with the outcome (self-observability seam,
	// set only by package main when enabled). nil ⇒ the push path is unchanged.
	Observe pushhook.Observer

	// Quiet, when true, suppresses the per-push "[dry-run loki] …" log line (inventory still
	// recorded). Set on throwaway dry sinks used for offline projection (bpsource cardinality
	// preview) so a validate/save click doesn't spew lines into a live process log.
	Quiet bool

	invMu     sync.Mutex
	invStream map[string]map[string]struct{} // dry-run: source → stream-label keys
	invMeta   map[string]map[string]struct{} // dry-run: source → structured-metadata keys
}

// New creates a Loki sink. extraForbidden extends DefaultForbiddenStreamLabels so the
// wiring layer can add deployment-specific high-card keys (e.g. "model" for AI workloads).
// dryRun=true logs pushes without hitting the network.
func New(url, user, token string, dryRun bool, extraForbidden ...string) *Sink {
	forbidden := make(map[string]struct{}, len(DefaultForbiddenStreamLabels)+len(extraForbidden))
	for _, k := range DefaultForbiddenStreamLabels {
		forbidden[k] = struct{}{}
	}
	for _, k := range extraForbidden {
		forbidden[k] = struct{}{}
	}
	return &Sink{
		url:      url,
		auth:     "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token)),
		hc:       &http.Client{Timeout: 15 * time.Second},
		dryRun:   dryRun,
		highCard: forbidden,
	}
}

// Loki push JSON: {"streams":[{"stream":{...}, "values":[["<ns>","<line>",{meta}], ...]}]}
type pushBody struct {
	Streams []pushStream `json:"streams"`
}
type pushStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]any           `json:"values"`
}

// record accumulates the dry-run inventory keyed by the `source` stream label: the union of
// stream-label keys and structured-metadata keys seen per source (offline diff vs signals/logs.md).
func (s *Sink) record(streams []Stream) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	if s.invStream == nil {
		s.invStream = map[string]map[string]struct{}{}
		s.invMeta = map[string]map[string]struct{}{}
	}
	for _, st := range streams {
		src := st.Labels["source"]
		sl := s.invStream[src]
		if sl == nil {
			sl = map[string]struct{}{}
			s.invStream[src] = sl
		}
		for k := range st.Labels {
			sl[k] = struct{}{}
		}
		ml := s.invMeta[src]
		if ml == nil {
			ml = map[string]struct{}{}
			s.invMeta[src] = ml
		}
		for _, ln := range st.Lines {
			for k := range ln.Meta {
				ml[k] = struct{}{}
			}
		}
	}
}

// Inventory returns the captured dry-run inventory per source: stream-label keys and metadata keys.
func (s *Sink) Inventory() (stream map[string][]string, meta map[string][]string) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	return sortInv(s.invStream), sortInv(s.invMeta)
}

// gzipBytes compresses b with gzip at BestSpeed — ~90% of the ratio at a fraction of the CPU.
// Loki accepts Content-Encoding: gzip.
func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sortInv(m map[string]map[string]struct{}) map[string][]string {
	out := make(map[string][]string, len(m))
	for src, keys := range m {
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		out[src] = ks
	}
	return out
}

// Write pushes log streams to the Loki push endpoint. Every stream's labels are checked
// against the forbidden set (I14/I15) — a violation returns an error immediately so the
// emitter bug surfaces loudly rather than silently exploding stream cardinality.
// Timestamps are formatted as nanosecond-epoch strings (Loki 3-tuple wire format).
func (s *Sink) Write(ctx context.Context, streams []Stream) error {
	total := 0
	body := pushBody{}
	for _, st := range streams {
		if len(st.Lines) == 0 {
			continue
		}
		// HIGH-CARDINALITY STREAM-LABEL ASSERTION (I14) — load-bearing invariant.
		for k := range st.Labels {
			if _, bad := s.highCard[k]; bad {
				return fmt.Errorf("loki: high-cardinality key %q used as a stream label (must be structured metadata) — labels=%v", k, st.Labels)
			}
		}
		vals := make([][]any, 0, len(st.Lines))
		for _, ln := range st.Lines {
			tuple := []any{strconv.FormatInt(ln.T.UnixNano(), 10), ln.Body}
			if len(ln.Meta) > 0 {
				tuple = append(tuple, ln.Meta)
			}
			vals = append(vals, tuple)
			total++
		}
		body.Streams = append(body.Streams, pushStream{Stream: st.Labels, Values: vals})
	}
	if total == 0 {
		return nil
	}
	blueprint := streams[0].Labels["blueprint"]
	if s.dryRun {
		s.record(streams)
		if !s.Quiet {
			log.Printf("[dry-run loki] %d streams, %d lines e.g. %v %q", len(body.Streams), total, streams[0].Labels, streams[0].Lines[0].Body)
		}
		s.observe(ctx, blueprint, total, 0, 0, time.Duration(0), true, nil)
		return nil
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	gz, err := gzipBytes(buf)
	if err != nil {
		return fmt.Errorf("loki push: gzip: %w", err)
	}
	var start time.Time
	if s.Observe != nil {
		start = time.Now()
	}
	var lastStatus int
	retryErr := httpretry.EmitOncePolicy().Do(ctx, func(rctx context.Context) (int, error) {
		// Rebuild request per attempt — bytes.NewReader is re-readable only from the start,
		// so create a fresh reader from the same gz slice each time.
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, s.url, bytes.NewReader(gz))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("Authorization", s.auth)
		resp, err := s.hc.Do(req)
		if err != nil {
			lastStatus = 0
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			lastStatus = resp.StatusCode
			return resp.StatusCode, fmt.Errorf("loki push: HTTP %d: %s", resp.StatusCode, b)
		}
		lastStatus = resp.StatusCode
		return resp.StatusCode, nil
	})
	s.observe(ctx, blueprint, total, len(gz), lastStatus, time.Since(start), false, retryErr)
	return retryErr
}

// observe fires the self-observability hook (no-op when unset).
func (s *Sink) observe(ctx context.Context, blueprint string, items, byteLen, status int, dur time.Duration, dryRun bool, err error) {
	if s.Observe == nil {
		return
	}
	s.Observe(ctx, pushhook.Event{
		Sink: "loki", Blueprint: blueprint, Items: items,
		Bytes: byteLen, Status: status, Duration: dur, DryRun: dryRun, Err: err,
	})
}
