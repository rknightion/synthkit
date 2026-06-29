// SPDX-License-Identifier: AGPL-3.0-only

// Package faro POSTs synthetic Faro Web SDK beacons to a Grafana Cloud Faro
// collector (the /collect ingest endpoint). This is the ONLY way a frontend app
// registers in the Frontend Observability app ("kowalski"): the collector is the
// sole writer of the AppConfig firstReceivedDataAt / lastReceivedDataAt lifecycle
// timestamps, and the app-list UI only shows apps where firstReceivedDataAt != nil.
// Pushing logs/traces straight to Loki/Tempo (as older RUM emitters used to) can
// never register an app, however correct the labels are.
//
// The collector also owns the Loki label + Tempo attribute mapping, so by sending
// well-formed Faro payloads we get exactly the same output a real Faro SDK produces
// (app_id/kind stream labels, the logfmt body, gf.feo11y.app.* on traces) without
// hand-rolling any of it.
//
// Wire format: ONE faro.Payload per HTTP POST to <collector>/<appKey> (the handler
// decodes a single payload, not a batch). Auth is the appKey in the URL path only —
// the collector is a public ingest endpoint (browsers POST to it), so no bearer
// token. Server-side POSTs are not subject to the app's CORS origin list.
//
// Payload structs below mirror github.com/grafana/faro/pkg/go exactly (JSON tags
// verified against faro.gen.go) but are re-declared here so the generator does NOT
// take the heavy go.opentelemetry.io/collector/pdata dependency that the upstream
// faro.Traces type drags in. Browser spans stay on the OTLP sink,
// so the Traces field is intentionally omitted from Payload.
package faro

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// ── Faro payload model (JSON contract — mirrors grafana/faro/pkg/go) ──────────

// Payload is one Faro beacon: the signals captured for a single session/page/view,
// plus the meta that identifies the app, session and environment. Traces are
// intentionally omitted (browser spans go via the OTLP sink — see package doc).
type Payload struct {
	Events       []Event       `json:"events,omitempty"`
	Exceptions   []Exception   `json:"exceptions,omitempty"`
	Measurements []Measurement `json:"measurements,omitempty"`
	Meta         Meta          `json:"meta,omitempty"`
}

// Meta identifies the app/session/page/browser the signals belong to. The collector
// derives the canonical app identity (app_id, app_key, stack_id, service_name) from
// the URL appKey + registered AppConfig — Meta.App.Name only feeds the body app_name
// and is the service_name fallback when no session.overrides.serviceName is set.
type Meta struct {
	SDK     SDK     `json:"sdk,omitempty"`
	App     App     `json:"app,omitempty"`
	Browser Browser `json:"browser,omitempty"`
	Page    Page    `json:"page,omitempty"`
	Session Session `json:"session,omitempty"`
	View    View    `json:"view,omitempty"`
}

type SDK struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type App struct {
	Name        string `json:"name,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Release     string `json:"release,omitempty"`
	Version     string `json:"version,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type Browser struct {
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	OS        string `json:"os,omitempty"`
	Mobile    bool   `json:"mobile,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	Language  string `json:"language,omitempty"`
}

type Page struct {
	ID  string `json:"id,omitempty"`
	URL string `json:"url,omitempty"`
}

type Session struct {
	ID string `json:"id,omitempty"`
}

type View struct {
	Name string `json:"name,omitempty"`
}

// TraceContext carries the W3C trace ids that join a Faro signal to its OTLP span.
// Field JSON names (trace_id/span_id) match the faro contract exactly.
type TraceContext struct {
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

// Action groups the signals emitted during one user interaction (the Faro user-actions
// feature: https://grafana.com/docs/grafana-cloud/monitor-applications/frontend-observability/instrument/user-actions/).
// The PARENT `faro.user.action` event carries ID + Name (no ParentID); every CHILD signal
// emitted during that action carries Name + ParentID (= the parent's ID). The collector
// writes these as the `action_id` / `action_name` / `action_parent_id` logfmt body fields,
// which the Frontend Observability "User actions" view groups + filters on (parent line =
// `event_name=faro.user.action`; child line = has `action_parent_id`, is NOT the parent).
// Seeing a `faro.user.action` event is also what flips the app's `action_received_at`
// lifecycle flag server-side, enabling the User-actions tab. JSON names match
// github.com/grafana/faro/pkg/go faro.Action exactly. Pointer so signals outside an action
// omit it entirely (absent = no action_* fields, exactly like a real SDK).
type Action struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	ParentID string `json:"parentId,omitempty"`
}

// Measurement is a numeric sample set (e.g. one web-vital). The collector emits each
// Values entry as both <key> and value_<key>, and each Context entry as context_<key>.
type Measurement struct {
	Type      string             `json:"type,omitempty"`
	Values    map[string]float64 `json:"values,omitempty"`
	Context   map[string]string  `json:"context,omitempty"`
	Trace     *TraceContext      `json:"trace,omitempty"`
	Action    *Action            `json:"action,omitempty"`
	Timestamp time.Time          `json:"timestamp,omitempty"`
}

// Event is a named browser event (navigation, faro.tracing.fetch, …). The collector
// emits Attributes as event_data_<key> and Name as event_name.
type Event struct {
	Name       string            `json:"name"`
	Domain     string            `json:"domain,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Trace      *TraceContext     `json:"trace,omitempty"`
	Action     *Action           `json:"action,omitempty"`
	Timestamp  time.Time         `json:"timestamp,omitempty"`
}

// Exception is a captured JS error. Stacktrace frames are structural only (no content).
type Exception struct {
	Type       string            `json:"type,omitempty"`
	Value      string            `json:"value,omitempty"`
	Stacktrace *Stacktrace       `json:"stacktrace,omitempty"`
	Context    map[string]string `json:"context,omitempty"`
	Trace      *TraceContext     `json:"trace,omitempty"`
	Action     *Action           `json:"action,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
}

type Stacktrace struct {
	Frames []Frame `json:"frames,omitempty"`
}

type Frame struct {
	Colno    int    `json:"colno,omitempty"`
	Filename string `json:"filename,omitempty"`
	Function string `json:"function,omitempty"`
	Lineno   int    `json:"lineno,omitempty"`
}

// ── Sink ──────────────────────────────────────────────────────────────────────

// maxConcurrentPOSTs bounds in-flight beacon POSTs so a tick that produces ~100
// payloads doesn't open ~100 sockets at once; well within a 30s tick at ~50ms each.
const maxConcurrentPOSTs = 8

// post() retries via httpretry.EmitOncePolicy — see that package for timing details.
// Transport failures (e.g. EOF on a reused keep-alive connection), 429, and 5xx are all
// retried within the EmitOnce budget. The replaced postAttempts/postRetryBackoff constants
// only covered transport errors; EmitOncePolicy additionally covers 429 and 5xx.

// Sink POSTs Faro beacons to <collector>/<appKey>.
type Sink struct {
	url    string // full collect URL incl. appKey: <collector>/<appKey>
	hc     *http.Client
	dryRun bool

	// Observe, when non-nil, is called ONCE per Write (a batch of beacons) with the aggregate
	// outcome (self-observability seam, set only by package main when enabled). nil ⇒ the push
	// path is unchanged. Faro POSTs one beacon per request concurrently, so this sink reports
	// Items = beacons posted, Status = 200/0 (success/failure of the batch), Bytes = 0.
	Observe pushhook.Observer
}

// New builds a Faro collector sink. collector is the AppConfig collectEndpointURL
// (e.g. https://faro-collector-prod-gb-south-1.grafana.net/collect); appKey is the
// registered app key appended as the URL path segment.
func New(collector, appKey string, dryRun bool) *Sink {
	url := strings.TrimRight(collector, "/") + "/" + appKey
	// The collector's HTTP server closes idle keep-alive connections at its IdleTimeout (30s — see
	// app-o11y-kwl-endpoint internal/server/server.go defaultHTTPIdleTimeout). The generator ticks
	// on the same ~30s cadence, so a pooled connection sits idle across a tick right at that
	// boundary; reusing it on the next tick races the server's close and yields EOF. Make the
	// CLIENT expire idle connections first (20s < 30s) so the next tick dials fresh instead of
	// reusing a connection the collector is about to drop. The post() retry is the safety net for
	// the residual race; this just stops it happening on the common path.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.IdleConnTimeout = 20 * time.Second
	return &Sink{
		url:    url,
		hc:     &http.Client{Timeout: 15 * time.Second, Transport: tr},
		dryRun: dryRun,
	}
}

// URL returns the full collect endpoint (collector + appKey) — exported for logging.
func (s *Sink) URL() string { return s.url }

// Write POSTs each payload as its own beacon (the collector decodes one payload per
// request). POSTs run with bounded concurrency; the first error is returned and the
// rest are allowed to finish. Empty payloads (no signals) are skipped.
func (s *Sink) Write(ctx context.Context, payloads []Payload) error {
	if len(payloads) == 0 {
		return nil
	}
	if s.dryRun {
		signals := 0
		for _, p := range payloads {
			signals += len(p.Measurements) + len(p.Events) + len(p.Exceptions)
		}
		appName := ""
		appEnv := ""
		if len(payloads) > 0 {
			appName = payloads[0].Meta.App.Name
			appEnv = payloads[0].Meta.App.Environment
		}
		log.Printf("[dry-run faro] %d beacon(s), %d signal(s) → %s (app=%s env=%s)",
			len(payloads), signals, s.url, appName, appEnv)
		s.observe(ctx, len(payloads), 0, time.Duration(0), true, nil)
		return nil
	}

	var start time.Time
	if s.Observe != nil {
		start = time.Now()
	}
	sem := make(chan struct{}, maxConcurrentPOSTs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	posted := 0

	for i := range payloads {
		p := payloads[i]
		if len(p.Measurements) == 0 && len(p.Events) == 0 && len(p.Exceptions) == 0 {
			continue
		}
		// The collector mandates X-Faro-Session-Id (set from Meta.Session.ID in post). A beacon with
		// no session id is a guaranteed 400, so drop it loudly here rather than fail the whole batch.
		if p.Meta.Session.ID == "" {
			log.Printf("[faro] skipping beacon with empty session id (app=%s) — would 400 at collector", p.Meta.App.Name)
			continue
		}
		posted++
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := s.post(ctx, p); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	status := 200
	if firstErr != nil {
		status = 0
	}
	s.observe(ctx, posted, status, time.Since(start), false, firstErr)
	return firstErr
}

// observe fires the self-observability hook (no-op when unset). Faro carries no blueprint
// dimension, so Blueprint is always ""; Bytes is 0 (per-beacon bodies are not totalled).
func (s *Sink) observe(ctx context.Context, beacons, status int, dur time.Duration, dryRun bool, err error) {
	if s.Observe == nil {
		return
	}
	s.Observe(ctx, pushhook.Event{
		Sink: "faro", Blueprint: "", Items: beacons,
		Bytes: 0, Status: status, Duration: dur, DryRun: dryRun, Err: err,
	})
}

// gzipBytes compresses b with gzip at BestSpeed — ~90% of the ratio at a fraction of the CPU,
// the right tradeoff for a generator pushing continuously. The Faro collector accepts
// Content-Encoding: gzip (the browser SDK uses it).
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

func (s *Sink) post(ctx context.Context, p Payload) error {
	buf, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("faro marshal: %w", err)
	}
	gz, err := gzipBytes(buf)
	if err != nil {
		return fmt.Errorf("faro gzip: %w", err)
	}
	return httpretry.EmitOncePolicy().Do(ctx, func(rctx context.Context) (int, error) {
		// Fresh request per attempt — bytes.NewReader rewinds from the gz slice each time.
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, s.url, bytes.NewReader(gz))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
		// The collector requires X-Faro-Session-Id (set by the SDK's SessionInstrumentation); without it
		// it rejects the beacon with HTTP 400. One beacon == one session, so use the payload's session id.
		if p.Meta.Session.ID != "" {
			req.Header.Set("X-Faro-Session-Id", p.Meta.Session.ID)
		}
		resp, err := s.hc.Do(req)
		if err != nil {
			// Transport-level failure (EOF on a reused keep-alive conn, reset, timeout). EmitOncePolicy
			// treats status=0 as retryable — it will retry within the budget.
			return 0, fmt.Errorf("faro post: %w", err)
		}
		// Got a real HTTP response — drain+close for keep-alive reuse.
		if resp.StatusCode/100 != 2 {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return resp.StatusCode, fmt.Errorf("faro post: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		return resp.StatusCode, nil
	})
}
