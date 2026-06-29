// SPDX-License-Identifier: AGPL-3.0-only

// Package selfobs instruments the generator PROCESS itself with real operational telemetry —
// RED metrics on the synthetic-push pipeline, Go runtime metrics, per-tick traces, and the
// operational log stream — and ships it to a SEPARATE Grafana Cloud stack (the staff
// self-observability stack) over a single OTLP endpoint. It is the self-observability counterpart of
// internal/profiling.
//
// Isolation is load-bearing and mirrors the Pyroscope path:
//   - It is the ONLY package that imports the OpenTelemetry SDK. The synthetic-data sinks
//     (promrw/loki/otlp/faro) report push outcomes through the stdlib-only internal/pushhook seam,
//     and the runner reports per-tick outcomes through its stdlib-only TickFunc seam — neither
//     imports this package, so the synthetic-data path never links the SDK. (The synthetic OTLP
//     sink hand-encodes proto and never touches the OTel SDK; the "OTel metrics SDK is banned" rule
//     in CLAUDE.md is about that synthetic path, and this package is its sole sanctioned exception —
//     for the generator's OWN telemetry, to a SEPARATE stack.)
//   - It builds its OWN TracerProvider/MeterProvider/LoggerProvider and NEVER installs them as OTel
//     globals (no otel.SetTracerProvider / SetMeterProvider / SetLoggerProvider). Handles are
//     injected explicitly, so the synthetic otlp sink (which bypasses the OTel global API) is
//     completely unaffected.
//   - It uses its own credential triplet (GC_SELF_OTLP_*), never GC_TOKEN, targeting the self-obs
//     stack — not the synthetic-data destination.
//   - It is decoupled from DRY_RUN and default-OFF. Disabled (or under-configured) ⇒ a no-op handle:
//     every method is inert and the generator behaves byte-for-byte as it does today.
package selfobs

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/pushhook"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	serviceName        = "synthkit"
	scopeName          = "synthkit/selfobs"
	defaultMetricFlush = 15 * time.Second // periodic metric reader interval (SELFOBS_METRIC_INTERVAL override)
	heartbeatInterval  = 5 * time.Minute  // operational heartbeat log cadence
)

// Options configures self-observability. The triplet (Endpoint/User/Password) is distinct from
// every synthetic sink's credentials — it targets the self-obs OTLP gateway, not the synthetic
// destination.
type Options struct {
	Enabled  bool              // SELFOBS_ENABLED — master on/off
	Endpoint string            // GC_SELF_OTLP_ENDPOINT — base OTLP gateway URL (…/otlp); /v1/{signal} appended
	User     string            // GC_SELF_OTLP_USER — HTTP Basic user = self-obs stack id
	Password string            // GC_SELF_OTLP_PASSWORD — token with metrics/logs/traces:write (NOT GC_TOKEN)
	Tags     map[string]string // extra resource attributes, merged over the built-ins
	Version  string            // stamped as service.version (git sha or "dev")
	DryRun   bool              // stamped as resource run_mode (live|dry_run); identity/audit only —
	//                            main gates selfobs OFF under DRY_RUN, so emitted telemetry is "live"

	// MetricInterval is the periodic metric reader flush cadence (SELFOBS_METRIC_INTERVAL). Applies
	// to self-obs METRICS only — traces/logs use their own batchers. Zero ⇒ defaultMetricFlush.
	MetricInterval time.Duration
}

// CardinalityPoint is one (blueprint, construct) distinct-series reading for the cardinality gauge.
// Defined here so the runner need not import selfobs; main.go adapts runner.Inventory() to these.
type CardinalityPoint struct {
	Blueprint, Kind, Name string
	Distinct              int64
}

// Gauges supplies live process state for observable gauges as plain callbacks, so selfobs never
// imports the runner or control package. The MeterProvider invokes these on its own collection
// goroutine. All callbacks are optional (nil ⇒ that gauge is not registered).
type Gauges struct {
	LedgerSize       func() int64              // total request-ledger size summed across all blueprints
	VolumeMultiplier func() float64            // control-plane master volume knob (one knob moves all load)
	BlueprintCount   func() int                // number of loaded blueprints (operability cardinality aid)
	Cardinality      func() []CardinalityPoint // per-blueprint/construct distinct-series counts
	QueueDepth       func() map[string]int     // delivery-queue depth by sink (I41); nil = gauge not registered
}

// SelfObs holds the self-observability providers and instruments. A nil-but-typed disabled handle
// is returned when self-obs is off or under-configured; all of its methods are inert.
type SelfObs struct {
	enabled bool

	tracer trace.Tracer
	logger otellog.Logger

	pushCount     metric.Int64Counter
	pushItems     metric.Int64Counter
	pushBytes     metric.Int64Counter
	pushDuration  metric.Float64Histogram
	tickCount     metric.Int64Counter
	tickDuration  metric.Float64Histogram
	cycleDuration metric.Float64Histogram
	droppedTicks  metric.Int64Counter
	queueBlocked  metric.Int64Counter
	flushCount    metric.Int64Counter
	flushDuration metric.Float64Histogram
	flushBatch    metric.Int64Histogram
	fleetOp       metric.Int64Counter
	fleetDuration metric.Float64Histogram

	stop      chan struct{} // closed by Shutdown to stop the heartbeat goroutine
	stopOnce  sync.Once
	shutdowns []func(context.Context) error
}

// Start builds and wires self-observability. Like profiling.Start it returns a usable handle even
// when disabled/under-configured (a no-op), so callers never need a nil check:
//
//	so, err := selfobs.Start(opts, gauges)
//	if err != nil { log.Printf("selfobs: %v", err) }
//	defer so.Shutdown(ctx)
func Start(opts Options, gauges Gauges) (*SelfObs, error) {
	if !opts.Enabled {
		return noop(), nil
	}
	if opts.Endpoint == "" || opts.User == "" || opts.Password == "" {
		log.Printf("selfobs: SELFOBS_ENABLED=true but GC_SELF_OTLP_ENDPOINT/USER/PASSWORD incomplete — skipping")
		return noop(), nil
	}

	ctx := context.Background()
	base := strings.TrimRight(opts.Endpoint, "/")
	headers := map[string]string{"Authorization": "Basic " + basicAuth(opts.User, opts.Password)}
	res := buildResource(opts)

	so := &SelfObs{enabled: true}

	// ── Traces ──
	texp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(base+"/v1/traces"),
		otlptracehttp.WithHeaders(headers),
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
		otlptracehttp.WithTimeout(15*time.Second),
	)
	if err != nil {
		return noop(), fmt.Errorf("selfobs: trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(texp), sdktrace.WithResource(res))
	so.tracer = tp.Tracer(scopeName)
	so.shutdowns = append(so.shutdowns, tp.Shutdown)

	// ── Metrics ──
	mexp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(base+"/v1/metrics"),
		otlpmetrichttp.WithHeaders(headers),
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
		otlpmetrichttp.WithTimeout(15*time.Second),
	)
	if err != nil {
		return noop(), fmt.Errorf("selfobs: metric exporter: %w", err)
	}
	metricFlush := opts.MetricInterval
	if metricFlush <= 0 {
		metricFlush = defaultMetricFlush
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(mexp, sdkmetric.WithInterval(metricFlush))),
		sdkmetric.WithResource(res),
	)
	so.shutdowns = append(so.shutdowns, mp.Shutdown)
	if err := so.initInstruments(mp, gauges); err != nil {
		return noop(), fmt.Errorf("selfobs: instruments: %w", err)
	}
	// Go runtime metrics against OUR provider (explicit — never the global).
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		log.Printf("selfobs: runtime metrics: %v", err)
	}

	// ── Logs ──
	lexp, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(base+"/v1/logs"),
		otlploghttp.WithHeaders(headers),
		otlploghttp.WithCompression(otlploghttp.GzipCompression),
		otlploghttp.WithTimeout(15*time.Second),
	)
	if err != nil {
		return noop(), fmt.Errorf("selfobs: log exporter: %w", err)
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(lexp)), sdklog.WithResource(res))
	so.logger = lp.Logger(scopeName)
	so.shutdowns = append(so.shutdowns, lp.Shutdown)

	// Operational heartbeat: a recurring INFO log line so the self-obs LOG stream stays alive in a
	// healthy DRY_RUN=false run (where the sinks are otherwise near-silent — the verbose [dry-run …]
	// summaries never fire). Emitted via the std log, which main bridges to OTLP after Start returns.
	so.startHeartbeat(gauges, heartbeatInterval)

	log.Printf("selfobs: started → %s (service=%s, signals=metrics+traces+logs, metric_interval=%s, heartbeat=%s)", redactURL(base), serviceName, metricFlush, heartbeatInterval)
	return so, nil
}

// startHeartbeat launches the heartbeat goroutine. It is only called from the enabled path of Start.
func (s *SelfObs) startHeartbeat(g Gauges, interval time.Duration) {
	s.stop = make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				log.Print(heartbeatLine(g))
			}
		}
	}()
}

// heartbeatLine renders the heartbeat content from the live gauge callbacks. Nil callbacks degrade
// gracefully (ledger=0, knobs omitted) and the line carries no WARN/error tokens so it stays INFO.
func heartbeatLine(g Gauges) string {
	var ledger int64
	if g.LedgerSize != nil {
		ledger = g.LedgerSize()
	}
	if g.VolumeMultiplier == nil && g.BlueprintCount == nil {
		return fmt.Sprintf("selfobs: heartbeat ledger=%d", ledger)
	}
	var mult float64
	if g.VolumeMultiplier != nil {
		mult = g.VolumeMultiplier()
	}
	var bps int
	if g.BlueprintCount != nil {
		bps = g.BlueprintCount()
	}
	return fmt.Sprintf("selfobs: heartbeat ledger=%d multiplier=%.2f blueprints=%d", ledger, mult, bps)
}

func noop() *SelfObs {
	return &SelfObs{
		enabled: false,
		tracer:  tracenoop.NewTracerProvider().Tracer(scopeName),
		logger:  lognoop.NewLoggerProvider().Logger(scopeName),
	}
}

// redactURL strips any userinfo (credentials) from a URL so it is safe to log. On parse failure or
// empty input it returns "<unparseable endpoint>" as a hard sentinel — credentials are never
// silently leaked by an edge-case URL format.
func redactURL(raw string) string {
	if raw == "" {
		return "<unparseable endpoint>"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable endpoint>"
	}
	u.User = nil
	return u.String()
}

// Enabled reports whether self-obs is live (used to gate the global log redirect).
func (s *SelfObs) Enabled() bool { return s != nil && s.enabled }

// Tracer returns the self-obs tracer (a no-op tracer on the disabled handle, so callers can
// unconditionally start spans).
func (s *SelfObs) Tracer() trace.Tracer { return s.tracer }

// ObserveTick is the runner's stdlib-only tick seam (runner.TickFunc): it wraps one instance Tick
// (or ProjectBatch) call in a self-obs span + duration/outcome metric, then returns fn's error
// UNCHANGED so the runner's error aggregation is byte-for-byte identical. On the disabled handle it
// simply calls fn(ctx) — zero overhead, no span, ctx unchanged — so the synthetic path never links
// the SDK and the runner needs no nil-check (it stores this method value, defaulting to nil = direct
// call). The span ctx flows into Tick → the sinks, so push spans nest under it.
func (s *SelfObs) ObserveTick(ctx context.Context, blueprint, kind, name string, fn func(context.Context) error) error {
	if !s.enabled {
		return fn(ctx)
	}
	tickCtx, span := s.tracer.Start(ctx, "tick", trace.WithAttributes(
		attribute.String("construct_instance", name),
		attribute.String("construct_kind", kind),
		attribute.String("blueprint", blueprint),
	))
	start := time.Now()
	err := fn(tickCtx)
	s.RecordTick(tickCtx, blueprint, kind, name, time.Since(start), err)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "tick error")
	}
	span.End()
	return err
}

// RecordTick records the duration + outcome of one instance tick. Inert when disabled.
func (s *SelfObs) RecordTick(ctx context.Context, blueprint, kind, instance string, dur time.Duration, err error) {
	if !s.enabled {
		return
	}
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	// construct_instance (NOT instance — OTLP→Prom derives instance from service.instance.id).
	common := []attribute.KeyValue{
		attribute.String("construct_instance", instance),
		attribute.String("construct_kind", kind),
		attribute.String("blueprint", blueprint),
	}
	s.tickCount.Add(ctx, 1, metric.WithAttributes(append(common, attribute.String("outcome", outcome))...))
	s.tickDuration.Record(ctx, dur.Seconds(), metric.WithAttributes(common...))
	if err != nil {
		s.logTickError(blueprint, kind, instance, err)
	}
}

// logTickError emits one structured ERROR LogRecord describing a failed instance tick. It rides the
// same self-obs log pipe as logPushFailure (never a synthetic sink) and is only reached from
// RecordTick on the err != nil path, so the enabled gate is already satisfied. An absent blueprint
// (substrate-scoped construct) is OMITTED, never "" — an absent dimension is never empty-string.
func (s *SelfObs) logTickError(blueprint, kind, instance string, err error) {
	var rec otellog.Record
	now := time.Now()
	rec.SetTimestamp(now)
	rec.SetObservedTimestamp(now)
	// scope reads "blueprint/kind:instance" — but a substrate-scoped construct has no blueprint, so
	// omit the leading "blueprint/" rather than emit a dangling slash (an absent dimension is OMITTED).
	scope := kind + ":" + instance
	if blueprint != "" {
		scope = blueprint + "/" + scope
	}
	rec.SetBody(otellog.StringValue(fmt.Sprintf("selfobs: tick %s error: %v", scope, err)))
	rec.SetSeverity(otellog.SeverityError)
	rec.SetSeverityText("ERROR")
	rec.AddAttributes(
		otellog.String("event", "tick_error"),
		otellog.String("construct_kind", kind),
		otellog.String("construct_instance", instance),
	)
	if blueprint != "" {
		rec.AddAttributes(otellog.String("blueprint", blueprint))
	}
	s.logger.Emit(context.Background(), rec)
}

// ObserveCycle is the runner's Seam 2 entry point (runner.CycleFunc): it records the wall-clock
// duration of one full blueprint cycle and, when ticks were dropped, increments the dropped-tick
// counter. Both instruments are keyed by blueprint so per-blueprint performance is visible. No-op
// on the disabled handle — the runner stores this as a plain func value so no nil check is needed.
func (s *SelfObs) ObserveCycle(ctx context.Context, blueprint string, dur time.Duration, dropped int) {
	if !s.enabled {
		return
	}
	bpAttr := metric.WithAttributes(attribute.String("blueprint", blueprint))
	s.cycleDuration.Record(ctx, dur.Seconds(), bpAttr)
	if dropped > 0 {
		s.droppedTicks.Add(ctx, int64(dropped), bpAttr)
	}
	// Cycle root span, backdated to the cycle window (start = end − dur), so the generation side of
	// the pipeline appears in Tempo + spanmetrics alongside the delivery (flush) spans. CONSUMER
	// kind so the metrics-generator meters it (the INTERNAL tick spans are not metered).
	end := time.Now()
	_, span := s.tracer.Start(ctx, "cycle",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithTimestamp(end.Add(-dur)),
		trace.WithAttributes(
			attribute.String("blueprint", blueprint),
			attribute.Int("dropped_ticks", dropped),
		))
	span.End(trace.WithTimestamp(end))
}

// PushObserver returns the pushhook.Observer that records push RED metrics + a child span. Returns
// nil on the disabled handle, so the sinks' Observe field stays nil (zero overhead).
func (s *SelfObs) PushObserver() pushhook.Observer {
	if !s.enabled {
		return nil
	}
	return func(ctx context.Context, ev pushhook.Event) {
		oc := classify(ev)
		st := signalType(ev.Sink)
		s.pushCount.Add(ctx, 1, metric.WithAttributes(
			attribute.String("sink", ev.Sink),
			attribute.String("signal_type", st),
			attribute.String("blueprint", ev.Blueprint),
			attribute.String("outcome", oc),
		))
		bpItemsAttr := metric.WithAttributes(
			attribute.String("sink", ev.Sink), attribute.String("signal_type", st),
			attribute.String("blueprint", ev.Blueprint))
		if ev.Items > 0 {
			s.pushItems.Add(ctx, int64(ev.Items), bpItemsAttr)
		}
		if ev.Bytes > 0 {
			s.pushBytes.Add(ctx, int64(ev.Bytes), bpItemsAttr)
		}
		if !ev.DryRun {
			s.pushDuration.Record(ctx, ev.Duration.Seconds(), bpItemsAttr)
		}
		// Structured failure log: a push error surfaces ONLY as a metric + trace span otherwise — the
		// log stream never carried the reason or its sink/blueprint context (the std-log bridge can't,
		// the sinks don't log pushes). Emit one structured LogRecord per failure so the operational log
		// stream is filterable by sink/blueprint/outcome (event="push_error"). Rate-limited is WARN
		// (transient backpressure); every other failure is ERROR.
		if ev.Err != nil {
			s.logPushFailure(ctx, ev, oc)
		}
		// Attach a child span only when the runner passed a live tick span through ctx.
		if !ev.DryRun && trace.SpanFromContext(ctx).SpanContext().IsValid() {
			end := time.Now()
			_, span := s.tracer.Start(ctx, "push "+ev.Sink,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithTimestamp(end.Add(-ev.Duration)),
				trace.WithAttributes(
					attribute.String("sink", ev.Sink),
					attribute.String("blueprint", ev.Blueprint),
					attribute.Int("items", ev.Items),
					attribute.Int("http.status_code", ev.Status),
				),
			)
			if ev.Err != nil {
				span.RecordError(ev.Err)
				span.SetStatus(codes.Error, oc)
			}
			span.End(trace.WithTimestamp(end))
		}
	}
}

// logPushFailure emits one structured ERROR/WARN LogRecord describing a failed synthetic push. It
// rides the same self-obs log pipe as EmitEvent (never a synthetic sink) and is only reached from
// PushObserver, so the enabled gate is already satisfied. Attributes give the log stream the
// failure context the free-text bridge could not: an absent blueprint (substrate-scoped push) is
// OMITTED, never "" (an absent dimension is never empty-string); status is omitted when unknown (0).
func (s *SelfObs) logPushFailure(_ context.Context, ev pushhook.Event, outcome string) {
	sev, sevText := otellog.SeverityError, "ERROR"
	if outcome == "rate_limited" {
		sev, sevText = otellog.SeverityWarn, "WARN"
	}
	var rec otellog.Record
	now := time.Now()
	rec.SetTimestamp(now)
	rec.SetObservedTimestamp(now)
	rec.SetBody(otellog.StringValue(fmt.Sprintf("selfobs: push %s %s: %v", ev.Sink, outcome, ev.Err)))
	rec.SetSeverity(sev)
	rec.SetSeverityText(sevText)
	rec.AddAttributes(
		otellog.String("event", "push_error"),
		otellog.String("sink", ev.Sink),
		otellog.String("outcome", outcome),
	)
	if ev.Blueprint != "" {
		rec.AddAttributes(otellog.String("blueprint", ev.Blueprint))
	}
	if ev.Status != 0 {
		rec.AddAttributes(otellog.Int("http.status_code", ev.Status))
	}
	s.logger.Emit(context.Background(), rec)
}

// FleetObserver returns the fleethook.Observer that records FM controller RED metrics. Returns nil
// on the disabled handle, so the fleet controller's Observe field stays nil (zero overhead). This is
// the FM counterpart to PushObserver — exporting registration/heartbeat health to the staff stack.
func (s *SelfObs) FleetObserver() fleethook.Observer {
	if !s.enabled {
		return nil
	}
	return func(ctx context.Context, ev fleethook.Event) {
		outcome := "ok"
		if ev.DryRun {
			outcome = "dry_run"
		} else if ev.Err != nil {
			outcome = "error"
		}
		s.fleetOp.Add(ctx, 1, metric.WithAttributes(
			attribute.String("op", ev.Op),
			attribute.String("outcome", outcome),
		))
		// Only heartbeats are timed; record the duration when present.
		if ev.Duration > 0 {
			s.fleetDuration.Record(ctx, ev.Duration.Seconds(), metric.WithAttributes(attribute.String("op", ev.Op)))
			// Backdated FM-operation span (CLIENT — an outbound FM API call), so registration/
			// heartbeat round-trips show up as operational traces next to the cycle/flush spans.
			end := time.Now()
			_, span := s.tracer.Start(ctx, "fleet "+ev.Op,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithTimestamp(end.Add(-ev.Duration)),
				trace.WithAttributes(
					attribute.String("op", ev.Op),
					attribute.String("collector", ev.Collector),
				))
			if ev.Err != nil && !ev.DryRun {
				span.RecordError(ev.Err)
				span.SetStatus(codes.Error, outcome)
			}
			span.End(trace.WithTimestamp(end))
		}
		if ev.Err != nil && !ev.DryRun {
			s.logFleetFailure(ev)
		}
	}
}

// logFleetFailure emits one structured ERROR LogRecord describing a failed FM operation. It rides the
// same self-obs log pipe as the push-failure log (never a synthetic sink) and is only reached from
// FleetObserver, so the enabled gate is already satisfied.
func (s *SelfObs) logFleetFailure(ev fleethook.Event) {
	var rec otellog.Record
	now := time.Now()
	rec.SetTimestamp(now)
	rec.SetObservedTimestamp(now)
	rec.SetBody(otellog.StringValue(fmt.Sprintf("selfobs: fleet %s %s: %v", ev.Op, ev.Collector, ev.Err)))
	rec.SetSeverity(otellog.SeverityError)
	rec.SetSeverityText("ERROR")
	rec.AddAttributes(
		otellog.String("event", "fleet_error"),
		otellog.String("op", ev.Op),
		otellog.String("collector", ev.Collector),
	)
	s.logger.Emit(context.Background(), rec)
}

// EmitEvent emits one structured OTLP LogRecord to the self-obs stack (body + attributes). No-op on
// a disabled handle. Reserved for config-change events (event="config_change") so they ride the
// existing self-obs log pipe — never the synthetic sinks. (Not yet wired into the control plane.)
func (s *SelfObs) EmitEvent(name string, attrs map[string]string, body string) {
	if !s.Enabled() {
		return
	}
	var rec otellog.Record
	now := time.Now()
	rec.SetTimestamp(now)
	rec.SetObservedTimestamp(now)
	rec.SetBody(otellog.StringValue(body))
	rec.SetSeverity(otellog.SeverityInfo)
	rec.SetSeverityText("INFO")
	rec.AddAttributes(otellog.String("event", name))
	for k, v := range attrs {
		rec.AddAttributes(otellog.String(k, v))
	}
	s.logger.Emit(context.Background(), rec)
}

// LogWriter returns the io.Writer to install as the std log output: it tees to stderr (so local
// runs are unchanged) and emits each line as an OTLP LogRecord. On the disabled handle it returns
// os.Stderr — but callers should gate the SetOutput call on Enabled() and not redirect at all.
func (s *SelfObs) LogWriter() io.Writer {
	if !s.enabled {
		return os.Stderr
	}
	return io.MultiWriter(os.Stderr, &logBridge{logger: s.logger})
}

// Shutdown flushes and stops every provider with a bounded deadline so a dead endpoint can never
// hang process exit. Safe (no-op) on the disabled handle.
func (s *SelfObs) Shutdown(ctx context.Context) {
	if !s.enabled {
		return
	}
	if s.stop != nil {
		s.stopOnce.Do(func() { close(s.stop) })
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, fn := range s.shutdowns {
		if err := fn(ctx); err != nil {
			log.Printf("selfobs: shutdown: %v", err)
		}
	}
}

func (s *SelfObs) initInstruments(mp metric.MeterProvider, g Gauges) error {
	m := mp.Meter(scopeName)
	var err error
	if s.pushCount, err = m.Int64Counter("synthkit.push",
		metric.WithDescription("synthetic-data push attempts by sink/blueprint/outcome")); err != nil {
		return err
	}
	if s.pushItems, err = m.Int64Counter("synthkit.push.items",
		metric.WithDescription("logical items pushed (series/lines/spans/beacons)")); err != nil {
		return err
	}
	if s.pushBytes, err = m.Int64Counter("synthkit.push.bytes", metric.WithUnit("By"),
		metric.WithDescription("wire bytes pushed (sinks that build their own body; promrw excluded)")); err != nil {
		return err
	}
	if s.pushDuration, err = m.Float64Histogram("synthkit.push.duration", metric.WithUnit("s"),
		metric.WithDescription("per-push wall-clock")); err != nil {
		return err
	}
	if s.tickCount, err = m.Int64Counter("synthkit.tick",
		metric.WithDescription("instance tick invocations by instance/outcome")); err != nil {
		return err
	}
	if s.tickDuration, err = m.Float64Histogram("synthkit.tick.duration", metric.WithUnit("s"),
		metric.WithDescription("per-instance-tick wall-clock")); err != nil {
		return err
	}
	if s.cycleDuration, err = m.Float64Histogram("synthkit.cycle.duration", metric.WithUnit("s"),
		metric.WithDescription("wall-clock duration of one full blueprint cycle")); err != nil {
		return err
	}
	if s.droppedTicks, err = m.Int64Counter("synthkit.dropped_ticks",
		metric.WithDescription("tick invocations dropped because the prior cycle was still running")); err != nil {
		return err
	}
	if s.queueBlocked, err = m.Int64Counter("synthkit.queue.enqueue_blocked",
		metric.WithDescription("times a tick blocked enqueuing to a full delivery queue (backpressure) by sink")); err != nil {
		return err
	}
	if s.flushCount, err = m.Int64Counter("synthkit.queue.flush",
		metric.WithDescription("delivery-queue flushes by sink/outcome (one per shard batch shipped)")); err != nil {
		return err
	}
	// Fine explicit buckets: queue flushes are sub-second to a few seconds, so the OTel default
	// histogram buckets (smallest 5s) are useless — these make flush-latency quantiles meaningful
	// (unlike the push/tick/cycle duration histograms, which keep defaults and use the mean).
	if s.flushDuration, err = m.Float64Histogram("synthkit.queue.flush.duration", metric.WithUnit("s"),
		metric.WithDescription("per-flush wall-clock of one delivery-queue batch ship, by sink"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10)); err != nil {
		return err
	}
	if s.flushBatch, err = m.Int64Histogram("synthkit.queue.flush.batch",
		metric.WithDescription("items shipped per delivery-queue flush, by sink (deadline-driven small vs capacity-driven full)")); err != nil {
		return err
	}
	if s.fleetOp, err = m.Int64Counter("synthkit.fleet.op",
		metric.WithDescription("Fleet Management controller operations by op/outcome (register/heartbeat/unregister)")); err != nil {
		return err
	}
	if s.fleetDuration, err = m.Float64Histogram("synthkit.fleet.duration", metric.WithUnit("s"),
		metric.WithDescription("per-FM-operation wall-clock (heartbeat GetConfig round-trip)")); err != nil {
		return err
	}
	// Observable gauges from the injected callbacks.
	if g.LedgerSize != nil {
		if _, err = m.Int64ObservableGauge("synthkit.ledger.size",
			metric.WithDescription("total request-ledger size across all blueprints"),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				o.Observe(g.LedgerSize())
				return nil
			})); err != nil {
			return err
		}
	}
	if g.VolumeMultiplier != nil {
		if _, err = m.Float64ObservableGauge("synthkit.volume.multiplier",
			metric.WithDescription("control-plane master volume multiplier"),
			metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
				o.Observe(g.VolumeMultiplier())
				return nil
			})); err != nil {
			return err
		}
	}
	if g.BlueprintCount != nil {
		if _, err = m.Int64ObservableGauge("synthkit.blueprint.count",
			metric.WithDescription("number of loaded blueprints"),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				o.Observe(int64(g.BlueprintCount()))
				return nil
			})); err != nil {
			return err
		}
	}
	if g.Cardinality != nil {
		if _, err = m.Int64ObservableGauge("synthkit.cardinality.series",
			metric.WithDescription("distinct synthetic series per blueprint/construct (internal X-ray bookkeeping)"),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				for _, p := range g.Cardinality() {
					o.Observe(p.Distinct, metric.WithAttributes(
						attribute.String("blueprint", p.Blueprint),
						attribute.String("construct_kind", p.Kind),
						attribute.String("construct_instance", p.Name)))
				}
				return nil
			})); err != nil {
			return err
		}
	}
	if g.QueueDepth != nil {
		if _, err = m.Int64ObservableGauge("synthkit.queue.depth",
			metric.WithDescription("current delivery-queue depth (enqueued-but-unflushed items) by sink (I41)"),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				for sink, d := range g.QueueDepth() {
					o.Observe(int64(d), metric.WithAttributes(attribute.String("sink", sink)))
				}
				return nil
			})); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueBlocked implements queue.Observer (structurally — selfobs imports no queue package):
// the delivery queue calls it when a tick blocks on a full per-shard buffer (backpressure).
func (s *SelfObs) EnqueueBlocked(sink string, _ time.Duration) {
	if s == nil || s.queueBlocked == nil {
		return
	}
	s.queueBlocked.Add(context.Background(), 1, metric.WithAttributes(attribute.String("sink", sink)))
}

// FlushObserved implements queue.Observer (structurally): the delivery queue calls it once per
// completed flush of a shard batch, on the queue's own background goroutine (no tick span in ctx).
// It records flush count/duration/batch-size and emits a `flush <sink>` span backdated to the
// flush window — the span that makes the QUEUED sinks (promrw/loki/otlp/pyroscope) visible in
// Tempo + spanmetrics again, since the decoupled queue ships them off the traced tick path.
func (s *SelfObs) FlushObserved(sink string, items int, d time.Duration, err error) {
	if s == nil || !s.enabled || s.flushCount == nil {
		return
	}
	ctx := context.Background()
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	s.flushCount.Add(ctx, 1, metric.WithAttributes(
		attribute.String("sink", sink), attribute.String("outcome", outcome)))
	sinkAttr := metric.WithAttributes(attribute.String("sink", sink))
	s.flushDuration.Record(ctx, d.Seconds(), sinkAttr)
	s.flushBatch.Record(ctx, int64(items), sinkAttr)

	end := time.Now()
	_, span := s.tracer.Start(ctx, "flush "+sink,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithTimestamp(end.Add(-d)),
		trace.WithAttributes(
			attribute.String("sink", sink),
			attribute.String("signal_type", signalType(sink)),
			attribute.Int("items", items),
		))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "flush error")
	}
	span.End(trace.WithTimestamp(end))
}

// sinkSignalType maps each sink name to the telemetry signal type it ships, so push throughput can
// be grouped by signal class (metrics/traces/logs/rum/profiles) and not only by sink name. The
// mapping is deterministic and lives selfobs-side — the synthetic sinks are untouched.
var sinkSignalType = map[string]string{
	"promrw":    "metrics",
	"loki":      "logs",
	"otlp":      "traces",
	"faro":      "rum",
	"pyroscope": "profiles",
}

// signalType returns the signal class for a sink, or "other" for an unrecognised sink (so a new
// sink never produces an empty label — an absent dimension is never "").
func signalType(sink string) string {
	if st, ok := sinkSignalType[sink]; ok {
		return st
	}
	return "other"
}

// classify maps a push Event to a bounded outcome label.
func classify(ev pushhook.Event) string {
	switch {
	case ev.DryRun:
		return "dry_run"
	case ev.Err == nil:
		return "ok"
	case ev.Status == 429:
		return "rate_limited"
	case ev.Status >= 400 && ev.Status < 500:
		return "client_error"
	case ev.Status >= 500 && ev.Status < 600:
		return "server_error"
	default:
		return "error"
	}
}
