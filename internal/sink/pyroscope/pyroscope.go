// SPDX-License-Identifier: AGPL-3.0-only

// Package pyroscope pushes synthetic pprof profiles to a Pyroscope endpoint
// via the push.v1 connect-unary API (hand-POST, no generated client).
// The outer HTTP body is the raw (uncompressed) marshalled PushRequest proto;
// the inner RawSample.raw_profile bytes are gzip-compressed pprof.
package pyroscope

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
	pprofpb "github.com/rknightion/synthkit/internal/pyroscope/pprofpb"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
	"github.com/rknightion/synthkit/internal/sink/pyroscope/pushv1"

	"google.golang.org/protobuf/proto"
)

// Sink pushes synthetic pprof profiles to a Pyroscope push.v1 endpoint.
type Sink struct {
	url    string
	auth   string
	hc     *http.Client
	dryRun bool

	// Observe, when non-nil, is called once per push with the outcome
	// (self-observability seam, set only by package main when enabled).
	Observe pushhook.Observer

	invMu sync.Mutex
	inv   map[string]map[string]struct{} // dry-run inventory: profile type → set of label keys
}

// New creates a Sink for the given Pyroscope URL, credentials, and dry-run mode.
// auth is encoded as HTTP Basic ("Basic base64(user:password)").
// dryRun=true records an inventory without hitting the network.
func New(url, user, password string, dryRun bool) *Sink {
	return &Sink{
		url:    url,
		hc:     &http.Client{Timeout: 15 * time.Second},
		auth:   "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password)),
		dryRun: dryRun,
	}
}

// Write pushes a batch of Series to the Pyroscope push.v1 endpoint.
// In dry-run mode it records an inventory and fires the observer without network I/O.
func (s *Sink) Write(ctx context.Context, batch []Series) error {
	if len(batch) == 0 {
		return nil
	}
	if s.dryRun {
		s.record(batch)
		s.observe(ctx, batch, 0, 0, 0, true, nil)
		return nil
	}

	req := encodePush(batch)
	raw, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("pyroscope: marshal PushRequest: %w", err)
	}

	endpoint := s.url + "/push.v1.PusherService/Push"
	var start time.Time
	if s.Observe != nil {
		start = time.Now()
	}
	var lastStatus int
	retryErr := httpretry.EmitOncePolicy().Do(ctx, func(rctx context.Context) (int, error) {
		httpReq, rerr := http.NewRequestWithContext(rctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if rerr != nil {
			return 0, rerr
		}
		httpReq.Header.Set("Content-Type", "application/proto")
		httpReq.Header.Set("Authorization", s.auth)
		httpReq.Header.Set("Connect-Protocol-Version", "1")
		// NOTE: do NOT set Content-Encoding — the body is raw (uncompressed) proto.
		// NOTE: do NOT set X-Scope-OrgID — Grafana Cloud resolves tenant from basic auth.
		resp, derr := s.hc.Do(httpReq)
		if derr != nil {
			lastStatus = 0
			return 0, derr
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		lastStatus = resp.StatusCode
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.StatusCode, nil
		}
		return resp.StatusCode, fmt.Errorf("pyroscope: push status %d", resp.StatusCode)
	})
	s.observe(ctx, batch, lastStatus, len(raw), time.Since(start), false, retryErr)
	if retryErr != nil {
		return fmt.Errorf("pyroscope: push: %w", retryErr)
	}
	return nil
}

// encodePush builds a PushRequest proto from a batch of Series.
func encodePush(batch []Series) *pushv1.PushRequest {
	series := make([]*pushv1.RawProfileSeries, 0, len(batch))
	for _, s := range batch {
		rawProfile := gzipPprof(s.Profile)
		var ts int64
		if s.Profile != nil {
			ts = s.Profile.TimeNanos
		}
		series = append(series, &pushv1.RawProfileSeries{
			Labels: toPB(s.Labels),
			Samples: []*pushv1.RawSample{{
				RawProfile: rawProfile,
				ID:         deterministicID(s.Labels, ts),
			}},
		})
	}
	return &pushv1.PushRequest{Series: series}
}

// toPB converts a slice of sink LabelPairs to pushv1 LabelPairs.
func toPB(labels []LabelPair) []*pushv1.LabelPair {
	out := make([]*pushv1.LabelPair, len(labels))
	for i, lp := range labels {
		out[i] = &pushv1.LabelPair{Name: lp.Name, Value: lp.Value}
	}
	return out
}

// gzipPprof marshals a pprof Profile proto and returns the gzip-compressed bytes.
func gzipPprof(p *pprofpb.Profile) []byte {
	var raw []byte
	if p != nil {
		var err error
		raw, err = proto.Marshal(p)
		if err != nil {
			// Marshal of a well-formed proto should not fail; return empty gzip body.
			raw = nil
		}
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(raw)
	_ = w.Close()
	return buf.Bytes()
}

// deterministicID returns a UUID-shaped string that is stable for fixed (labels, timeNanos)
// and differs across timeNanos values. Pyroscope uses the ID for retry dedup, so it must
// vary per tick to prevent later profiles being silently dropped.
//
// Algorithm: FNV-1a 64-bit hash over sorted "name=value" label pairs + timeNanos,
// formatted as "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" using the low 128 bits (hash
// combined with a simple xor shift for the second half).
func deterministicID(labels []LabelPair, timeNanos int64) string {
	sorted := make([]LabelPair, len(labels))
	copy(sorted, labels)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	h := fnv.New64a()
	for _, lp := range sorted {
		_, _ = fmt.Fprintf(h, "%s=%s\n", lp.Name, lp.Value)
	}
	_, _ = fmt.Fprintf(h, "%d\n", timeNanos)
	hi := h.Sum64()

	// Produce a second 64-bit word by mixing in timeNanos differently.
	h2 := fnv.New64a()
	_, _ = fmt.Fprintf(h2, "%d\n", timeNanos)
	for _, lp := range sorted {
		_, _ = fmt.Fprintf(h2, "%s=%s\n", lp.Name, lp.Value)
	}
	lo := h2.Sum64()

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		hi>>32,
		(hi>>16)&0xffff,
		hi&0xffff,
		lo>>48,
		lo&0x0000ffffffffffff,
	)
}

// record accumulates the dry-run inventory: profile type key → union of label keys seen.
// The inventory key is the __profile_type__ label value if present, else __name__ value.
func (s *Sink) record(batch []Series) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	if s.inv == nil {
		s.inv = map[string]map[string]struct{}{}
	}
	for _, ser := range batch {
		key := inventoryKey(ser.Labels)
		if s.inv[key] == nil {
			s.inv[key] = map[string]struct{}{}
		}
		for _, lp := range ser.Labels {
			s.inv[key][lp.Name] = struct{}{}
		}
	}
}

// inventoryKey returns __profile_type__ value if present, else __name__ value, else "unknown".
func inventoryKey(labels []LabelPair) string {
	fallback := "unknown"
	for _, lp := range labels {
		if lp.Name == "__profile_type__" {
			return lp.Value
		}
		if lp.Name == "__name__" {
			fallback = lp.Value
		}
	}
	return fallback
}

// Inventory returns the captured dry-run inventory: profile-type key → sorted label keys.
// Empty outside dry-run (or before any Write calls).
func (s *Sink) Inventory() map[string][]string {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	out := make(map[string][]string, len(s.inv))
	for key, names := range s.inv {
		ks := make([]string, 0, len(names))
		for k := range names {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		out[key] = ks
	}
	return out
}

// observe fires the self-observability hook (no-op when unset).
func (s *Sink) observe(ctx context.Context, batch []Series, status, bytesLen int, dur time.Duration, dryRun bool, err error) {
	if s.Observe == nil {
		return
	}
	s.Observe(ctx, pushhook.Event{
		Sink:      "pyroscope",
		Blueprint: "",
		Items:     len(batch),
		Bytes:     bytesLen,
		Status:    status,
		Duration:  dur,
		DryRun:    dryRun,
		Err:       err,
	})
}
