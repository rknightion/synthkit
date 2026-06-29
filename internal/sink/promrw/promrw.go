// SPDX-License-Identifier: AGPL-3.0-only

// Package promrw pushes synthetic series to a Prometheus remote_write endpoint (Mimir).
// ALL metrics travel this sink with FINAL pre-mangled names — the OTel metrics SDK is
// deliberately excluded. See ARCHITECTURE.md §6 + signals/.
package promrw

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/highcard"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/httpretry"

	"github.com/golang/snappy"
	"google.golang.org/protobuf/proto"
)

// RW2 protocol surface: Prometheus Remote-Write v2 (io.prometheus.write.v2.Request).
const (
	RemoteWriteVersion = "2.0.0"
	ContentTypeRW2     = "application/x-protobuf;proto=io.prometheus.write.v2.Request"
)

// Sink pushes series to a Prometheus remote_write endpoint.
type Sink struct {
	url    string
	hc     *http.Client
	auth   string
	dryRun bool
	capFn  func() int // live cap closure: called per-push to read the current series budget (0/nil = unlimited)

	// Observe, when non-nil, is called once per push with the outcome (self-observability seam,
	// set only by package main when enabled). nil ⇒ the push path is unchanged. On a live push
	// Bytes is the snappy-compressed on-wire size and Status is the HTTP response status (0 on a
	// transport error); the dry-run path reports Bytes 0.
	Observe pushhook.Observer

	// Quiet, when true, suppresses the per-push "[dry-run promrw] …" log line (inventory is still
	// recorded). Set on throwaway dry sinks used for offline projection (e.g. bpsource cardinality
	// preview) so a validate/save click doesn't spew inventory lines into a live process log. The
	// `-once -dump` path leaves it false so the inventory still prints.
	Quiet bool

	invMu     sync.Mutex                     // guards inv, invKind, invNative and captured
	inv       map[string]map[string]struct{} // dry-run inventory: metric name → set of label keys
	invKind   map[string]Kind                // dry-run inventory: metric name → instrument kind
	invNative map[string]bool                // dry-run inventory: metric name → true when any series carried a native histogram

	// Capture, when true (dry-run only), additionally retains every pushed series (with label VALUES)
	// so tests can assert on values, not just key presence. Off by default — never set in production
	// (it would grow unbounded). Read back via Captured().
	Capture  bool
	captured []Series

	liveMu   sync.Mutex
	liveSeen map[string]struct{} // distinct series signatures seen since start (live cardinality aid)
}

// New creates a Sink for the given remote_write URL, credentials, and optional series-cap closure.
// capFn may be nil (unlimited). dryRun=true logs pushes without hitting the network.
func New(url, user, token string, dryRun bool, capFn func() int) *Sink {
	return &Sink{
		url:    url,
		hc:     &http.Client{Timeout: 15 * time.Second},
		auth:   "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token)),
		dryRun: dryRun,
		capFn:  capFn,
	}
}

// uuidClassLabels is the set of high-cardinality UUID-class label keys that must never
// appear as Mimir labels. These keys are unique per request/session, which would create
// unbounded cardinality in Mimir's time-series index (invariant M4). It sources the canonical
// high-card set from internal/highcard so the promrw sink, the Loki sink, and the telemetryspec
// DSL capability matrix agree by construction.
//
// NOTE: "uid" is intentionally excluded despite being UUID-shaped. Kubernetes kube_pod_info
// uses "uid" as a pod UID label — cardinality is bounded by pod count and the label is
// required for cross-metric joins. "model" is also legal (bounded model-name vocabulary).
// Only keys that are truly request/session/correlation scoped belong (see internal/highcard).
var uuidClassLabels = highcard.Set()

// recordDistinct accumulates the distinct (name + sorted labels) signatures seen on a push.
// Cheap approximate live cardinality (high-water; does not shrink when emitters disable).
// Called from Write regardless of dry-run.
func (s *Sink) recordDistinct(batch []Series) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	if s.liveSeen == nil {
		s.liveSeen = map[string]struct{}{}
	}
	for _, m := range batch {
		s.liveSeen[seriesSig(m)] = struct{}{}
	}
}

// DistinctSeries returns the count of distinct series signatures seen since start.
func (s *Sink) DistinctSeries() int {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return len(s.liveSeen)
}

// seriesSig is a stable signature: metric name + labels sorted by key. Deterministic per series.
func seriesSig(m Series) string {
	keys := make([]string, 0, len(m.Labels))
	for k := range m.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(m.Name)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m.Labels[k])
	}
	return b.String()
}

// Write pushes a batch of series to remote_write. In dry-run it logs a summary instead of pushing.
// When the series-cap is set and the batch exceeds it, the batch is truncated with a loud WARN log.
func (s *Sink) Write(ctx context.Context, batch []Series) error {
	if len(batch) == 0 {
		return nil
	}
	s.recordDistinct(batch)
	if s.capFn != nil {
		if c := s.capFn(); c > 0 && len(batch) > c {
			log.Printf("[promrw] WARN series-cap hit: batch=%d > SERIES_CAP=%d — truncating (safety valve)", len(batch), c)
			batch = batch[:c]
		}
	}

	// M4 UUID-class label guard: reject any metric that carries a request/session-scoped
	// key as a Mimir label — these would create unbounded cardinality. See uuidClassLabels
	// for the canonical high-card key set (internal/highcard) and the uid/model exclusions.
	for _, m := range batch {
		for k := range m.Labels {
			if _, bad := uuidClassLabels[k]; bad {
				return fmt.Errorf("promrw: UUID-class label key %q on metric %q would create unbounded Mimir cardinality (M4)", k, m.Name)
			}
		}
	}

	if s.dryRun {
		s.record(batch) // acquires invMu internally
		if s.Capture {
			s.invMu.Lock()
			s.captured = append(s.captured, batch...)
			s.invMu.Unlock()
		}
		if !s.Quiet {
			log.Printf("[dry-run promrw] %d series e.g. %s%v=%.3f", len(batch), batch[0].Name, sortedLabels(batch[0].Labels), batch[0].Value)
		}
		s.observe(ctx, batch, 0, 0, time.Duration(0), true, nil)
		return nil
	}
	req := encodeRequest(batch)
	raw, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("promrw: marshal RW2 request: %w", err)
	}
	compressed := snappy.Encode(nil, raw)

	var start time.Time
	if s.Observe != nil {
		start = time.Now()
	}
	var lastStatus int
	retryErr := httpretry.MetricsPolicy().Do(ctx, func(rctx context.Context) (int, error) {
		httpReq, rerr := http.NewRequestWithContext(rctx, http.MethodPost, s.url, bytes.NewReader(compressed))
		if rerr != nil {
			return 0, rerr
		}
		httpReq.Header.Set("Content-Type", ContentTypeRW2)
		httpReq.Header.Set("Content-Encoding", "snappy")
		httpReq.Header.Set("X-Prometheus-Remote-Write-Version", RemoteWriteVersion)
		httpReq.Header.Set("Authorization", s.auth)
		httpReq.Header.Set("User-Agent", "synthkit-promrw/2")
		resp, derr := s.hc.Do(httpReq)
		if derr != nil {
			lastStatus = 0 // transport error: no HTTP status (matches the otlp sink)
			return 0, derr
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		lastStatus = resp.StatusCode
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.StatusCode, nil
		}
		// 415/406 ⇒ receiver rejects RW2 — permanent, surfaced (MetricsPolicy retries only 429/5xx).
		return resp.StatusCode, fmt.Errorf("promrw: remote_write status %d", resp.StatusCode)
	})
	s.observe(ctx, batch, lastStatus, len(compressed), time.Since(start), false, retryErr)
	if retryErr != nil {
		return fmt.Errorf("remote_write: %w", retryErr)
	}
	return nil
}

// observe fires the self-observability hook (no-op when unset). blueprint is recovered from the
// stamped "blueprint" label; direct Write callers without a blueprint label leave it empty.
func (s *Sink) observe(ctx context.Context, batch []Series, status, bytesOut int, dur time.Duration, dryRun bool, err error) {
	if s.Observe == nil {
		return
	}
	blueprint := ""
	if len(batch) > 0 {
		blueprint = batch[0].Labels["blueprint"]
	}
	s.Observe(ctx, pushhook.Event{
		Sink: "promrw", Blueprint: blueprint, Items: len(batch),
		Bytes: bytesOut, Status: status, Duration: dur, DryRun: dryRun, Err: err,
	})
}

// record accumulates the dry-run inventory: every distinct metric name and the union of label keys
// seen on it (full distinct-series inventory for offline diff against signals/).
// Callers must hold invMu or must be on the dry-run path (which takes invMu separately for captured).
func (s *Sink) record(batch []Series) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	if s.inv == nil {
		s.inv = map[string]map[string]struct{}{}
	}
	if s.invKind == nil {
		s.invKind = map[string]Kind{}
	}
	if s.invNative == nil {
		s.invNative = map[string]bool{}
	}
	for _, m := range batch {
		keys := s.inv[m.Name]
		if keys == nil {
			keys = map[string]struct{}{}
			s.inv[m.Name] = keys
		}
		for k := range m.Labels {
			keys[k] = struct{}{}
		}
		s.invKind[m.Name] = m.Kind
		if m.Native != nil {
			s.invNative[m.Name] = true
		}
	}
}

// Kinds returns the dry-run instrument-kind inventory: metric name → Kind (companion to
// Inventory, captured from state.Collect's Add/Set/Observe origin). Empty outside dry-run.
func (s *Sink) Kinds() map[string]Kind {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	out := make(map[string]Kind, len(s.invKind))
	maps.Copy(out, s.invKind)
	return out
}

// Natives returns the dry-run native-histogram inventory: metric name → true when at least
// one recorded series of that name carried a native histogram. Companion to Kinds().
func (s *Sink) Natives() map[string]bool {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	out := make(map[string]bool, len(s.invNative))
	maps.Copy(out, s.invNative)
	return out
}

// Inventory returns the captured dry-run inventory: metric name → sorted union of label keys.
func (s *Sink) Inventory() map[string][]string {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	out := make(map[string][]string, len(s.inv))
	for name, keys := range s.inv {
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		out[name] = ks
	}
	return out
}

// Captured returns every series retained while Capture was true (dry-run). Empty unless Capture is set.
func (s *Sink) Captured() []Series {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	return s.captured
}

// SeriesFor returns the captured series with the given metric name (Capture must have been set).
func (s *Sink) SeriesFor(name string) []Series {
	var out []Series
	for _, m := range s.captured {
		if m.Name == name {
			out = append(out, m)
		}
	}
	return out
}

func sortedLabels(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
