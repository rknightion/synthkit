// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// AutomationFlow declares a bounded simulated operational sequence, not a live orchestrator.
// Names identify operations in spans; no arbitrary attributes or content are accepted.
type AutomationFlow struct {
	Name  string           `yaml:"name"`
	Steps []AutomationStep `yaml:"steps"`
}

// AutomationStep is exactly one approval decision or HTTP task.
type AutomationStep struct {
	Name     string              `yaml:"name"`
	Approval *AutomationApproval `yaml:"approval"`
	HTTP     *AutomationHTTP     `yaml:"http"`
}

// AutomationApproval models one decision-duration budget and rejection policy.
type AutomationApproval struct {
	DurationMS           int64   `yaml:"duration_ms"`
	RejectionProbability float64 `yaml:"rejection_probability"`
}

// AutomationHTTP models immediate simulated resends, without payloads, headers or backoff.
type AutomationHTTP struct {
	Method                  string  `yaml:"method"`
	URL                     string  `yaml:"url"`
	DurationMS              int64   `yaml:"duration_ms"`
	MaxAttempts             int     `yaml:"max_attempts"`
	FailureProbability      float64 `yaml:"failure_probability"`
	RetrySuccessProbability float64 `yaml:"retry_success_probability"`
}

// validateAutomation keeps the first automation surface deliberately separate from graph/DSL composition.
func validateAutomation(cfg *Config) error {
	f := cfg.Automation
	if f == nil {
		return nil
	}
	if strings.TrimSpace(f.Name) == "" || len(f.Steps) < 1 || len(f.Steps) > 32 {
		return fmt.Errorf("app automation: require a name and 1–32 steps")
	}
	if len(cfg.Services) != 1 {
		return fmt.Errorf("app automation: require one job service")
	}
	s := cfg.Services[0]
	if s.Type != "job" || !s.tracesEnabled() || len(s.Calls) > 0 {
		return fmt.Errorf("app automation: require a traced job service without calls")
	}
	if len(s.Profiles) > 0 || len(s.Spans) > 0 || len(s.Logs) > 0 || len(s.Metrics) > 0 || s.AgenticFlow != nil || s.Pyroscope != nil || len(cfg.Models) > 0 || cfg.otelMetricsEnabled() {
		return fmt.Errorf("app automation: profiles, inline telemetry, agentic flow, models, profiling and native metrics are unsupported")
	}
	probability := func(p float64) bool { return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1 }
	names := map[string]bool{}
	// Check in milliseconds before multiplication/conversion, preventing time.Duration overflow.
	const maxMS = int64(24 * time.Hour / time.Millisecond)
	totalMS := int64(2)
	for i, step := range f.Steps {
		name := strings.TrimSpace(step.Name)
		if name == "" || names[name] {
			return fmt.Errorf("app automation: step %d needs a unique name", i)
		}
		names[name] = true
		if (step.Approval == nil) == (step.HTTP == nil) {
			return fmt.Errorf("app automation: step %q requires exactly one approval or http", name)
		}
		var duration int64
		attempts := 1
		if a := step.Approval; a != nil {
			duration = a.DurationMS
			if !probability(a.RejectionProbability) {
				return fmt.Errorf("app automation: step %q invalid rejection probability", name)
			}
		} else {
			h := step.HTTP
			duration = h.DurationMS
			if h.MaxAttempts < 0 || h.MaxAttempts > 5 {
				return fmt.Errorf("app automation: step %q max_attempts must be 0–5", name)
			}
			attempts = automationAttempts(h)
			if !probability(h.FailureProbability) || !probability(h.RetrySuccessProbability) {
				return fmt.Errorf("app automation: step %q invalid HTTP probability", name)
			}
			switch h.Method {
			case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
			default:
				return fmt.Errorf("app automation: step %q requires a known HTTP method", name)
			}
			u, err := url.Parse(h.URL)
			if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(h.URL, "#") {
				return fmt.Errorf("app automation: step %q requires an absolute HTTP(S) URL without user info, query or fragment", name)
			}
			if strings.HasSuffix(u.Host, ":") {
				return fmt.Errorf("app automation: step %q has empty port", name)
			}
			if port := u.Port(); port != "" {
				n, err := strconv.Atoi(port)
				if err != nil || n < 1 || n > 65535 {
					return fmt.Errorf("app automation: step %q invalid port", name)
				}
			}
		}
		if duration <= 0 || duration > (maxMS-totalMS)/int64(attempts) {
			return fmt.Errorf("app automation: positive durations and worst-case sequence at most 24 hours required")
		}
		totalMS += duration * int64(attempts)
	}
	return nil
}

func automationAttempts(h *AutomationHTTP) int {
	if h.MaxAttempts == 0 {
		return 1
	}
	return h.MaxAttempts
}

// automationUnitDraw consumes no mutable shape RNG: projection and metrics replay the same facts.
func automationUnitDraw(rootSpanID string, step, attempt int, purpose string) float64 {
	seed := fmt.Sprintf("%s\x00%d\x00%d\x00%s", rootSpanID, step, attempt, purpose)
	sum := sha256.Sum256([]byte(seed))
	return float64(binary.BigEndian.Uint64(sum[:8])>>11) / float64(uint64(1)<<53)
}

type automationStepPlan struct {
	ordinal         int
	start, duration time.Duration
	approved        bool
	attempts        []bool // true is a failed send; retries stop at the first success
	failed          bool
}

type automationPlan struct {
	duration  time.Duration
	errorType string
	steps     []automationStepPlan
}

func planAutomation(f *AutomationFlow, r *ledger.Request) automationPlan {
	p := automationPlan{duration: time.Millisecond}
	for i, s := range f.Steps {
		step := automationStepPlan{ordinal: i, start: p.duration}
		if a := s.Approval; a != nil {
			step.duration = time.Duration(a.DurationMS) * time.Millisecond
			step.approved = automationUnitDraw(r.SpanID, i, 0, "approval") >= a.RejectionProbability
			if !step.approved {
				p.errorType = "_OTHER"
				step.failed = true
			}
		} else {
			h := s.HTTP
			for attempt := 0; attempt < automationAttempts(h); attempt++ {
				failed := false
				if attempt == 0 {
					failed = automationUnitDraw(r.SpanID, i, attempt, "first") < h.FailureProbability
				} else {
					failed = automationUnitDraw(r.SpanID, i, attempt, "retry") >= h.RetrySuccessProbability
				}
				step.attempts = append(step.attempts, failed)
				step.duration += time.Duration(h.DurationMS) * time.Millisecond
				if !failed {
					break
				}
			}
			step.failed = step.attempts[len(step.attempts)-1]
			if step.failed {
				p.errorType = "503"
			}
		}
		p.steps = append(p.steps, step)
		p.duration += step.duration
		if step.failed {
			break
		}
	}
	p.duration += time.Millisecond
	return p
}

// automationSpans uses standard HTTP attrs and native span names/status/timing only.
// Sources: OTel semconv v1.44.0 HTTP spans; tracing API v1.61.0. Root lands signals provenance.
func automationSpans(f *AutomationFlow, r *ledger.Request) []otlp.Span {
	p := planAutomation(f, r)
	base := r.RenderStart()
	root := otlp.Span{Name: f.Name, TraceID: r.TraceID, SpanID: r.SpanID, Kind: otlp.KindInternal, Start: base, End: base.Add(p.duration), Attrs: universalAttrs(r)}
	if p.errorType != "" {
		root.Status = otlp.StatusError
		root.Attrs["error.type"] = p.errorType
	}
	spans := []otlp.Span{root}
	for _, step := range p.steps {
		s := f.Steps[step.ordinal]
		id := ledger.SpanIDFromSeed(r.SpanID, fmt.Sprintf("automation/step/%d", step.ordinal))
		start := base.Add(step.start)
		local := otlp.Span{Name: s.Name, TraceID: r.TraceID, SpanID: id, ParentID: r.SpanID, Kind: otlp.KindInternal, Start: start, End: start.Add(step.duration), Attrs: universalAttrs(r)}
		if s.Approval != nil {
			outcome := "approved"
			if !step.approved {
				outcome = "rejected"
			}
			local.Name = "approval " + outcome + " " + s.Name
			spans = append(spans, local)
			continue
		}
		if step.failed {
			local.Status = otlp.StatusError
			local.Attrs["error.type"] = "503"
		}
		spans = append(spans, local)
		h := s.HTTP
		u, _ := url.Parse(h.URL) // validated at Build; this declaration is immutable
		port := 80
		if u.Scheme == "https" {
			port = 443
		}
		if u.Port() != "" {
			port, _ = strconv.Atoi(u.Port())
		}
		duration := time.Duration(h.DurationMS) * time.Millisecond
		for ordinal, failed := range step.attempts {
			attrs := universalAttrs(r)
			attrs["http.request.method"] = h.Method
			attrs["http.response.status_code"] = 200
			attrs["server.address"] = u.Hostname()
			attrs["server.port"] = port
			attrs["url.full"] = h.URL
			if ordinal > 0 {
				attrs["http.request.resend_count"] = ordinal
			}
			status := otlp.StatusUnset
			if failed {
				status = otlp.StatusError
				attrs["http.response.status_code"] = 503
				attrs["error.type"] = "503"
			}
			attemptStart := start.Add(time.Duration(ordinal) * duration)
			spans = append(spans, otlp.Span{Name: h.Method, TraceID: r.TraceID, SpanID: ledger.SpanIDFromSeed(r.SpanID, fmt.Sprintf("automation/step/%d/attempt/%d", step.ordinal, ordinal)), ParentID: id, Kind: otlp.KindClient, Start: attemptStart, End: attemptStart.Add(duration), Status: status, Attrs: attrs})
		}
	}
	return spans
}

func (w *Workload) projectAutomationTraces(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	resource := otlp.Resource{Attrs: w.identity(w.graph.entry).resourceAttrs()}
	for _, r := range batch {
		resource.Spans = append(resource.Spans, automationSpans(w.cfg.Automation, r)...)
	}
	return world.Traces.Write(ctx, []otlp.Resource{resource})
}

func (w *Workload) tickAutomationSpanMetrics(now time.Time, world *core.World) {
	if world.Ledger == nil || (!w.automationMetricThrough.IsZero() && !now.After(w.automationMetricThrough)) {
		return
	}
	from := now.Add(-interval)
	if w.automationMetricThrough.After(from) {
		from = w.automationMetricThrough
	}
	id := w.identity(w.graph.entry)
	base := id.spanMetricBase()
	for _, r := range world.Ledger.ActiveFor(w.Name(), now, now.Sub(from)) {
		for _, s := range automationSpans(w.cfg.Automation, r) {
			status := statusCodeUnset
			if s.Status == otlp.StatusError {
				status = statusCodeError
			}
			kind := protoSpanKind(s.Kind)
			w.observeSpanCallsRow(base, kind, s.Name, status, 1, id.sdkLang())
			latency := s.End.Sub(s.Start).Seconds()
			w.observeSpanLatency(base, kind, s.Name, status, 1, func() float64 { return latency })
		}
	}
	w.automationMetricThrough = now
}
