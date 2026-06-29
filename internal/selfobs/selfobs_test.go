// SPDX-License-Identifier: AGPL-3.0-only

package selfobs

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/pushhook"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// recordingLogExporter captures every LogRecord the SimpleProcessor exports, so a test can assert
// the structured push-failure logs selfobs emits.
type recordingLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *recordingLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, recs...)
	return nil
}
func (e *recordingLogExporter) Shutdown(context.Context) error   { return nil }
func (e *recordingLogExporter) ForceFlush(context.Context) error { return nil }
func (e *recordingLogExporter) all() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdklog.Record(nil), e.records...)
}

func logAttrs(r sdklog.Record) map[string]otellog.Value {
	m := map[string]otellog.Value{}
	r.WalkAttributes(func(kv otellog.KeyValue) bool {
		m[string(kv.Key)] = kv.Value
		return true
	})
	return m
}

// TestPushObserver_FailureLog asserts that a FAILED push emits exactly one structured LogRecord
// (event=push_error) carrying the failure context as attributes — sink/outcome/http.status_code,
// plus blueprint ONLY when set (an absent dimension is omitted, never ""). A successful push emits
// no log. This is the structured-failure path the unstructured log bridge could not provide.
func TestPushObserver_FailureLog(t *testing.T) {
	exp := &recordingLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })

	so := noop()
	so.enabled = true
	so.logger = lp.Logger(scopeName)
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
	if err := so.initInstruments(mp, Gauges{}); err != nil {
		t.Fatalf("initInstruments: %v", err)
	}
	obs := so.PushObserver()
	ctx := context.Background()

	// Success → no log.
	obs(ctx, pushhook.Event{Sink: "loki", Blueprint: "initech", Status: 204, Items: 1})
	// Substrate-scoped hard failure (no blueprint) → ERROR, blueprint omitted.
	obs(ctx, pushhook.Event{Sink: "promrw", Status: 0, Err: errors.New("connection refused")})
	// Blueprint-scoped 429 → WARN, blueprint present.
	obs(ctx, pushhook.Event{Sink: "otlp", Blueprint: "newco", Status: 429, Err: errors.New("too many requests")})

	recs := exp.all()
	if len(recs) != 2 {
		t.Fatalf("expected 2 push-failure logs (success emits none), got %d", len(recs))
	}

	// First failure: ERROR, event=push_error, sink=promrw, outcome=error, status=0, NO blueprint.
	a0 := logAttrs(recs[0])
	if recs[0].SeverityText() != "ERROR" {
		t.Errorf("hard-failure severity = %q, want ERROR", recs[0].SeverityText())
	}
	if v, ok := a0["event"]; !ok || v.AsString() != "push_error" {
		t.Errorf("missing event=push_error; attrs=%v", a0)
	}
	if v, ok := a0["sink"]; !ok || v.AsString() != "promrw" {
		t.Errorf("sink attr = %v, want promrw", a0["sink"])
	}
	if v, ok := a0["outcome"]; !ok || v.AsString() != "error" {
		t.Errorf("outcome attr = %v, want error", a0["outcome"])
	}
	if _, ok := a0["blueprint"]; ok {
		t.Errorf("blueprint attr present on substrate push (must be omitted when empty); attrs=%v", a0)
	}

	// Second failure: WARN (rate-limited), blueprint=newco present, status=429.
	a1 := logAttrs(recs[1])
	if recs[1].SeverityText() != "WARN" {
		t.Errorf("rate-limited severity = %q, want WARN", recs[1].SeverityText())
	}
	if v, ok := a1["blueprint"]; !ok || v.AsString() != "newco" {
		t.Errorf("blueprint attr = %v, want newco", a1["blueprint"])
	}
	if v, ok := a1["http.status_code"]; !ok || v.AsInt64() != 429 {
		t.Errorf("http.status_code attr = %v, want 429", a1["http.status_code"])
	}
}

// TestRedactURL covers the URL-redaction helper: credentials are stripped, non-credential URLs are
// left structurally unchanged, and malformed / empty inputs return the hard sentinel.
func TestRedactURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://user:pass@host/otlp", "https://host/otlp"},
		{"https://1234:token@prometheus.grafana.net/otlp", "https://prometheus.grafana.net/otlp"},
		{"https://host/otlp", "https://host/otlp"},         // no userinfo — unchanged
		{"http://localhost:4317", "http://localhost:4317"}, // local — unchanged
		{"", "<unparseable endpoint>"},                     // empty
		{"://bad url \x00", "<unparseable endpoint>"},      // unparseable
	}
	for _, c := range cases {
		got := redactURL(c.in)
		if got != c.want {
			t.Errorf("redactURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStart_DisabledIsNoOp(t *testing.T) {
	so, err := Start(Options{Enabled: false}, Gauges{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if so.Enabled() {
		t.Fatal("disabled handle reports Enabled()=true")
	}
	if so.PushObserver() != nil {
		t.Fatal("disabled handle must return a nil PushObserver (sinks stay uninstrumented)")
	}
	if so.FleetObserver() != nil {
		t.Fatal("disabled handle must return a nil FleetObserver (fleet controller stays uninstrumented)")
	}
	if so.Tracer() == nil {
		t.Fatal("Tracer() must be non-nil even when disabled (no-op tracer)")
	}
	// Methods must not panic on the disabled handle.
	so.RecordTick(context.Background(), "bp", "ec2", "x", time.Second, nil)
	so.Shutdown(context.Background())
}

func TestStart_IncompleteCredsIsNoOp(t *testing.T) {
	so, err := Start(Options{Enabled: true, Endpoint: "https://x/otlp"}, Gauges{}) // no user/password
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if so.Enabled() {
		t.Fatal("incomplete-cred handle must be a no-op")
	}
}

// TestStart_NeverInstallsGlobals is the load-bearing isolation guard: after starting an ENABLED
// self-obs (providers built; exporters are lazy so no network), the OTel GLOBAL tracer provider
// must still be the default no-op — proving selfobs never called otel.SetTracerProvider and so can
// never contaminate (or be contaminated by) the synthetic otlp sink or any other global user.
func TestStart_NeverInstallsGlobals(t *testing.T) {
	so, err := Start(Options{
		Enabled:  true,
		Endpoint: "https://otlp.invalid/otlp",
		User:     "1",
		Password: "tok",
	}, Gauges{})
	if err != nil {
		t.Fatalf("Start(enabled): %v", err)
	}
	defer so.Shutdown(context.Background())

	if !so.Enabled() {
		t.Fatal("enabled Start should yield an enabled handle")
	}
	// The global tracer must be the no-op default: a span it starts is NOT recording.
	_, span := otel.GetTracerProvider().Tracer("probe").Start(context.Background(), "probe")
	if span.IsRecording() {
		t.Fatal("global TracerProvider is recording — selfobs leaked a provider into OTel globals")
	}
	// Our own tracer, by contrast, IS recording.
	_, ours := so.Tracer().Start(context.Background(), "ours")
	if !ours.IsRecording() {
		t.Fatal("selfobs tracer should produce recording spans")
	}
}

// TestHeartbeatLine pins the operational heartbeat content: a single INFO line carrying live
// liveness state (ledger size + volume multiplier + blueprint count) so the self-obs LOG stream is
// never silent in a healthy DRY_RUN=false production run (the dry-run summaries don't fire there).
func TestHeartbeatLine(t *testing.T) {
	got := heartbeatLine(Gauges{
		LedgerSize:       func() int64 { return 42 },
		VolumeMultiplier: func() float64 { return 1.5 },
		BlueprintCount:   func() int { return 3 },
	})
	want := "selfobs: heartbeat ledger=42 multiplier=1.50 blueprints=3"
	if got != want {
		t.Errorf("heartbeatLine =\n  %q\nwant\n  %q", got, want)
	}
	// Nil gauges must not panic and must still produce a liveness line.
	if got := heartbeatLine(Gauges{}); got != "selfobs: heartbeat ledger=0" {
		t.Errorf("heartbeatLine(nil gauges) = %q", got)
	}
	// A severity-neutral line: it must classify as INFO (no WARN/error tokens).
	if _, txt := severityOf(want); txt != "INFO" {
		t.Errorf("heartbeat line must be INFO severity, got %q", txt)
	}
}

// safeWriter counts Write calls under a mutex so the heartbeat goroutine and the test can race-free
// observe emission.
type safeWriter struct {
	mu sync.Mutex
	n  int
}

func (w *safeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.n++
	w.mu.Unlock()
	return len(p), nil
}

func (w *safeWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n
}

// TestHeartbeat_EmitsAndStops verifies the heartbeat goroutine fires on its interval (through the
// std log, which is what main bridges to OTLP) and stops cleanly on Shutdown.
func TestHeartbeat_EmitsAndStops(t *testing.T) {
	// Rebinds the process-global std logger, so this test must NOT be parallelized (no
	// t.Parallel here, and none in sibling tests). If the package is ever parallelized,
	// route capture through an injected io.Writer instead of log.SetOutput.
	w := &safeWriter{}
	log.SetOutput(w)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	so := noop()
	so.enabled = true
	so.startHeartbeat(Gauges{LedgerSize: func() int64 { return 1 }}, 5*time.Millisecond)

	// Poll up to a generous deadline rather than a fixed sleep: under -race + parallel load the
	// heartbeat goroutine can be CPU-starved, so a fixed 60ms window is flaky. The intent is only
	// that it fires repeatedly on its interval.
	deadline := time.Now().Add(2 * time.Second)
	for w.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := w.count(); got < 2 {
		t.Fatalf("expected ≥2 heartbeats at a 5ms interval within 2s, got %d", got)
	}

	so.Shutdown(context.Background())
	time.Sleep(30 * time.Millisecond) // let any in-flight tick land and the goroutine exit
	at := w.count()
	time.Sleep(60 * time.Millisecond)
	if after := w.count(); after != at {
		t.Fatalf("heartbeat kept firing after Shutdown: %d → %d", at, after)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		ev   pushhook.Event
		want string
	}{
		{pushhook.Event{DryRun: true}, "dry_run"},
		{pushhook.Event{Status: 200}, "ok"},
		{pushhook.Event{Status: 429, Err: errors.New("x")}, "rate_limited"},
		{pushhook.Event{Status: 400, Err: errors.New("x")}, "client_error"},
		{pushhook.Event{Status: 503, Err: errors.New("x")}, "server_error"},
		{pushhook.Event{Status: 0, Err: errors.New("x")}, "error"},
	}
	for _, c := range cases {
		if got := classify(c.ev); got != c.want {
			t.Errorf("classify(%+v) = %q, want %q", c.ev, got, c.want)
		}
	}
}

func TestEmitEventDisabledNoop(t *testing.T) {
	var s *SelfObs                                                              // nil handle
	s.EmitEvent("config_change", map[string]string{"source": "manual"}, "body") // must not panic
	s2 := &SelfObs{}                                                            // enabled=false
	s2.EmitEvent("config_change", nil, "body")
}

// TestObserveTick_Disabled proves the runner seam is a transparent pass-through on the disabled
// handle: it calls fn with the SAME ctx and returns fn's error unchanged (so the runner's behaviour
// and error aggregation are byte-for-byte identical when self-obs is off).
func TestObserveTick_Disabled(t *testing.T) {
	so := noop() // disabled
	ctx := context.Background()
	sentinel := errors.New("boom")
	var gotCtx context.Context
	err := so.ObserveTick(ctx, "bp", "ec2", "ec2", func(c context.Context) error {
		gotCtx = c
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("ObserveTick must return fn's error unchanged: got %v", err)
	}
	if gotCtx != ctx {
		t.Fatal("disabled ObserveTick must pass the SAME ctx (no span injected)")
	}
	// nil-receiver paths used elsewhere must also be safe.
	if got := so.ObserveTick(ctx, "bp", "x", "x", func(context.Context) error { return nil }); got != nil {
		t.Fatalf("ObserveTick ok path returned %v", got)
	}
}

// TestRecorder_Metrics drives the recording logic directly through a ManualReader, validating the
// instrument names, outcome attribution, the observable gauges, and that ObserveTick records a tick.
func TestRecorder_Metrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	so := noop()
	so.enabled = true
	if err := so.initInstruments(mp, Gauges{
		LedgerSize:       func() int64 { return 7 },
		VolumeMultiplier: func() float64 { return 1.0 },
		BlueprintCount:   func() int { return 2 },
	}); err != nil {
		t.Fatalf("initInstruments: %v", err)
	}

	obs := so.PushObserver()
	ctx := context.Background()
	obs(ctx, pushhook.Event{Sink: "loki", Blueprint: "initech", Items: 5, Bytes: 100, Status: 204, Duration: 10 * time.Millisecond})
	obs(ctx, pushhook.Event{Sink: "promrw", Items: 3, Status: 429, Duration: time.Millisecond, Err: errors.New("429")})
	if err := so.ObserveTick(ctx, "bp", "rds", "rds", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("ObserveTick: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	for _, want := range []string{"synthkit.push", "synthkit.push.items", "synthkit.push.duration",
		"synthkit.tick", "synthkit.tick.duration", "synthkit.ledger.size", "synthkit.volume.multiplier",
		"synthkit.blueprint.count"} {
		if !names[want] {
			t.Errorf("missing metric %q (got %v)", want, names)
		}
	}

	// The tick instruments must carry the ticked instance under `construct_instance`, NOT `instance`:
	// the OTLP→Prometheus mapping derives the `instance` label from the resource service.instance.id,
	// so a datapoint attribute also named `instance` is silently clobbered (the per-instance tick
	// breakdown vanishes). Pin the non-colliding key on both tick instruments.
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "synthkit.tick" && m.Name != "synthkit.tick.duration" {
				continue
			}
			var attrSets []attribute.Set
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					attrSets = append(attrSets, dp.Attributes)
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					attrSets = append(attrSets, dp.Attributes)
				}
			default:
				t.Fatalf("%s: unexpected data type %T", m.Name, m.Data)
			}
			for _, set := range attrSets {
				if _, has := set.Value(attribute.Key("instance")); has {
					t.Errorf("%s uses the colliding `instance` attribute (clobbered on OTLP→Prom); attrs=%v", m.Name, set.ToSlice())
				}
				v, has := set.Value(attribute.Key("construct_instance"))
				if !has || v.AsString() != "rds" {
					t.Errorf("%s missing construct_instance=\"rds\"; attrs=%v", m.Name, set.ToSlice())
				}
			}
		}
	}
}

// TestQueueInstruments drives the delivery-queue self-obs (I35) through a ManualReader: the
// enqueue_blocked counter (fed by the queue.Observer seam) and the depth observable gauge
// (fed by the injected Gauges.QueueDepth callback), both attributed by sink.
func TestQueueInstruments(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	so := noop()
	so.enabled = true
	if err := so.initInstruments(mp, Gauges{
		QueueDepth: func() map[string]int { return map[string]int{"promrw": 7} },
	}); err != nil {
		t.Fatalf("initInstruments: %v", err)
	}

	ctx := context.Background()
	so.EnqueueBlocked("promrw", 5*time.Millisecond) // implements queue.Observer

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var blockedVal, depthVal int64
	var sawBlocked, sawDepth bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "synthkit.queue.enqueue_blocked":
				sawBlocked = true
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					if v, ok := dp.Attributes.Value(attribute.Key("sink")); ok && v.AsString() == "promrw" {
						blockedVal = dp.Value
					}
				}
			case "synthkit.queue.depth":
				sawDepth = true
				for _, dp := range m.Data.(metricdata.Gauge[int64]).DataPoints {
					if v, ok := dp.Attributes.Value(attribute.Key("sink")); ok && v.AsString() == "promrw" {
						depthVal = dp.Value
					}
				}
			}
		}
	}
	if !sawBlocked || blockedVal != 1 {
		t.Errorf("enqueue_blocked: saw=%v value=%d, want true/1", sawBlocked, blockedVal)
	}
	if !sawDepth || depthVal != 7 {
		t.Errorf("queue.depth: saw=%v value=%d, want true/7", sawDepth, depthVal)
	}
}

// TestFleetObserver_Metrics drives the FM observer through a ManualReader, asserting it records
// the synthkit.fleet.op counter (with op/outcome attribution) and the duration histogram, and
// emits a structured failure log on a failed op.
func TestFleetObserver_Metrics(t *testing.T) {
	exp := &recordingLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	so := noop()
	so.enabled = true
	so.logger = lp.Logger(scopeName)
	if err := so.initInstruments(mp, Gauges{}); err != nil {
		t.Fatalf("initInstruments: %v", err)
	}

	obs := so.FleetObserver()
	ctx := context.Background()
	obs(ctx, fleethook.Event{Collector: "c1", Op: fleethook.OpHeartbeat, Duration: 5 * time.Millisecond})
	obs(ctx, fleethook.Event{Collector: "c2", Op: fleethook.OpHeartbeat, Err: errors.New("503")})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	for _, want := range []string{"synthkit.fleet.op", "synthkit.fleet.duration"} {
		if !names[want] {
			t.Errorf("missing metric %q (got %v)", want, names)
		}
	}

	// Exactly one structured failure log (the 503 heartbeat), event=fleet_error.
	var failures int
	for _, r := range exp.all() {
		if v, ok := logAttrs(r)["event"]; ok && v.AsString() == "fleet_error" {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("fleet_error logs = %d, want 1", failures)
	}
}

// TestObserveCycle_Disabled asserts that ObserveCycle on a disabled (no-op) handle is a safe
// no-op: it must not panic regardless of arguments.
func TestObserveCycle_Disabled(t *testing.T) {
	so := noop()
	ctx := context.Background()
	// Must not panic — 0 dropped, nonzero dropped, zero duration, all edge cases.
	so.ObserveCycle(ctx, "bp1", 500*time.Millisecond, 0)
	so.ObserveCycle(ctx, "bp1", 500*time.Millisecond, 3)
	so.ObserveCycle(ctx, "", 0, 0)
}

// TestObserveCycle_Records uses a ManualReader (same pattern as TestRecorder_Metrics) to assert
// that ObserveCycle emits both synthkit.cycle.duration and synthkit.dropped_ticks with correct
// blueprint attributes and that the dropped counter accumulates correctly.
func TestObserveCycle_Records(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	so := noop()
	so.enabled = true
	if err := so.initInstruments(mp, Gauges{}); err != nil {
		t.Fatalf("initInstruments: %v", err)
	}

	ctx := context.Background()
	// Two cycles for "web", one with 2 drops and one with 0 drops.
	so.ObserveCycle(ctx, "web", 100*time.Millisecond, 2)
	so.ObserveCycle(ctx, "web", 200*time.Millisecond, 0)
	// One cycle for "infra" with 1 drop.
	so.ObserveCycle(ctx, "infra", 50*time.Millisecond, 1)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	names := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = m
		}
	}

	// Both instruments must be present.
	if _, ok := names["synthkit.cycle.duration"]; !ok {
		t.Errorf("missing metric synthkit.cycle.duration (got %v)", func() []string {
			var ks []string
			for k := range names {
				ks = append(ks, k)
			}
			return ks
		}())
	}
	if _, ok := names["synthkit.dropped_ticks"]; !ok {
		t.Errorf("missing metric synthkit.dropped_ticks (got %v)", func() []string {
			var ks []string
			for k := range names {
				ks = append(ks, k)
			}
			return ks
		}())
	}

	// Verify dropped_ticks total: "web"→2, "infra"→1, total=3.
	dropped, ok := names["synthkit.dropped_ticks"]
	if !ok {
		return // already reported above
	}
	sum, ok := dropped.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("synthkit.dropped_ticks data type = %T, want metricdata.Sum[int64]", dropped.Data)
	}
	// Sum across all blueprint datapoints must be 3.
	var total int64
	for _, dp := range sum.DataPoints {
		total += dp.Value
	}
	if total != 3 {
		t.Errorf("synthkit.dropped_ticks total = %d, want 3", total)
	}
	// Each datapoint must carry a blueprint attribute.
	for _, dp := range sum.DataPoints {
		if _, has := dp.Attributes.Value(attribute.Key("blueprint")); !has {
			t.Errorf("synthkit.dropped_ticks datapoint missing blueprint attribute: %v", dp.Attributes.ToSlice())
		}
	}
	// "infra" must have exactly 1 dropped tick.
	for _, dp := range sum.DataPoints {
		v, _ := dp.Attributes.Value(attribute.Key("blueprint"))
		if v.AsString() == "infra" && dp.Value != 1 {
			t.Errorf("infra dropped_ticks = %d, want 1", dp.Value)
		}
	}
}

// TestPushObserver_SignalType asserts every push instrument carries a signal_type label derived
// deterministically from the sink (metrics/traces/logs/rum/profiles), enabling per-signal-class
// throughput views without a new seam.
func TestPushObserver_SignalType(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	so := noop()
	so.enabled = true
	if err := so.initInstruments(mp, Gauges{}); err != nil {
		t.Fatalf("initInstruments: %v", err)
	}
	obs := so.PushObserver()
	ctx := context.Background()
	obs(ctx, pushhook.Event{Sink: "loki", Blueprint: "b", Items: 5, Bytes: 100, Status: 204})
	obs(ctx, pushhook.Event{Sink: "promrw", Blueprint: "b", Items: 3, Status: 200})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	want := map[string]string{"loki": "logs", "promrw": "metrics"}
	seen := map[string]string{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "synthkit.push" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				sink, _ := dp.Attributes.Value(attribute.Key("sink"))
				st, has := dp.Attributes.Value(attribute.Key("signal_type"))
				if !has {
					t.Errorf("synthkit.push datapoint missing signal_type; attrs=%v", dp.Attributes.ToSlice())
					continue
				}
				seen[sink.AsString()] = st.AsString()
			}
		}
	}
	for sink, st := range want {
		if seen[sink] != st {
			t.Errorf("sink %q signal_type = %q, want %q (seen=%v)", sink, seen[sink], st, seen)
		}
	}
}

// TestFlushObserved_Metrics drives the queue flush seam through a ManualReader: a successful and a
// failed flush must produce the count (by outcome), the latency histogram, and the batch-size
// histogram, all attributed by sink.
func TestFlushObserved_Metrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	so := noop()
	so.enabled = true
	if err := so.initInstruments(mp, Gauges{}); err != nil {
		t.Fatalf("initInstruments: %v", err)
	}
	so.FlushObserved("promrw", 4200, 12*time.Millisecond, nil)
	so.FlushObserved("promrw", 10, 3*time.Millisecond, errors.New("503"))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var okCount, errCount int64
	var sawDur, sawBatch bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "synthkit.queue.flush":
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					oc, _ := dp.Attributes.Value(attribute.Key("outcome"))
					switch oc.AsString() {
					case "ok":
						okCount = dp.Value
					case "error":
						errCount = dp.Value
					}
				}
			case "synthkit.queue.flush.duration":
				sawDur = len(m.Data.(metricdata.Histogram[float64]).DataPoints) > 0
			case "synthkit.queue.flush.batch":
				sawBatch = len(m.Data.(metricdata.Histogram[int64]).DataPoints) > 0
			}
		}
	}
	if okCount != 1 || errCount != 1 {
		t.Errorf("flush count ok=%d err=%d, want 1/1", okCount, errCount)
	}
	if !sawDur || !sawBatch {
		t.Errorf("flush histograms: duration=%v batch=%v, want both true", sawDur, sawBatch)
	}
}

// TestRecordTick_ErrorLog asserts that a failing tick emits exactly one structured LogRecord
// (event=tick_error, severity ERROR) carrying the construct identity as attributes, and that a
// successful tick emits no log. The blueprint attribute is present only when non-empty (an absent
// dimension is omitted, never "").
func TestRecordTick_ErrorLog(t *testing.T) {
	exp := &recordingLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	so := noop()
	so.enabled = true
	so.logger = lp.Logger(scopeName)
	if err := so.initInstruments(mp, Gauges{}); err != nil {
		t.Fatalf("initInstruments: %v", err)
	}

	ctx := context.Background()
	boom := errors.New("dial timeout")

	// Success tick → no log.
	so.RecordTick(ctx, "initech", "rds", "db-prod", 50*time.Millisecond, nil)
	if got := exp.all(); len(got) != 0 {
		t.Fatalf("success tick must emit no log; got %d record(s)", len(got))
	}

	// Blueprint-scoped failure → ERROR, blueprint attr present.
	so.RecordTick(ctx, "initech", "rds", "db-prod", 50*time.Millisecond, boom)
	recs := exp.all()
	if len(recs) != 1 {
		t.Fatalf("expected 1 tick_error log, got %d", len(recs))
	}
	r := recs[0]
	if r.SeverityText() != "ERROR" {
		t.Errorf("severity = %q, want ERROR", r.SeverityText())
	}
	if r.Severity() != otellog.SeverityError {
		t.Errorf("Severity() = %v, want SeverityError", r.Severity())
	}
	a := logAttrs(r)
	if v, ok := a["event"]; !ok || v.AsString() != "tick_error" {
		t.Errorf("event attr = %v, want tick_error; all attrs=%v", a["event"], a)
	}
	if v, ok := a["construct_kind"]; !ok || v.AsString() != "rds" {
		t.Errorf("construct_kind attr = %v, want rds", a["construct_kind"])
	}
	if v, ok := a["construct_instance"]; !ok || v.AsString() != "db-prod" {
		t.Errorf("construct_instance attr = %v, want db-prod", a["construct_instance"])
	}
	if v, ok := a["blueprint"]; !ok || v.AsString() != "initech" {
		t.Errorf("blueprint attr = %v, want initech", a["blueprint"])
	}

	// Substrate-scoped failure (blueprint="") → blueprint attr must be absent.
	so.RecordTick(ctx, "", "ec2", "i-abc123", 10*time.Millisecond, errors.New("network error"))
	all := exp.all()
	if len(all) != 2 {
		t.Fatalf("expected 2 total tick_error logs after substrate tick, got %d", len(all))
	}
	a2 := logAttrs(all[1])
	if _, ok := a2["blueprint"]; ok {
		t.Errorf("blueprint attr must be absent on substrate tick (blueprint=\"\"); attrs=%v", a2)
	}
}

// TestBuildResource_RunMode pins the run_mode resource attribute (live vs dry_run) and the
// pid-disambiguated instance id, which dashboards use to drop dry-run/dev-box noise.
func TestBuildResource_RunMode(t *testing.T) {
	for _, tc := range []struct {
		dry  bool
		want string
	}{{false, "live"}, {true, "dry_run"}} {
		res := buildResource(Options{Version: "v1", DryRun: tc.dry})
		attrs := map[string]string{}
		for _, kv := range res.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		if attrs["run_mode"] != tc.want {
			t.Errorf("DryRun=%v: run_mode=%q, want %q", tc.dry, attrs["run_mode"], tc.want)
		}
		if id := attrs["service.instance.id"]; !strings.Contains(id, "-") {
			t.Errorf("service.instance.id %q should be host-pid", id)
		}
	}
}
