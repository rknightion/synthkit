// SPDX-License-Identifier: AGPL-3.0-only

// Package datadogreceiver models the observed Kubernetes Datadog Agent -> Alloy
// Datadog-receiver egress path. It is deliberately a receiver-output construct:
// its metric names and envelopes come from the retained native OTLP capture, not
// from a Prometheus spelling or the post-gateway read-back.
package datadogreceiver

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

const (
	// Kind is the registry key used by the composition-root wiring pass.
	Kind = "datadog_receiver"

	interval = time.Minute

	// translatorScope and translatorVersion are the exact scope observed on 233
	// of the 234 retained metric envelopes. They must not be replaced by the
	// sink's synthkit fallback scope.
	translatorScope   = "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/datadogreceiver/internal/translator"
	translatorVersion = "v1.19.2"

	exampleMetricIncrement = "example_metric.increment"
)

// Config is the Kubernetes receiver declaration. HostName, ServiceName, and
// Source are values supplied by the selected synthetic topology; the capture
// establishes their resource placement and value type, while deliberately
// eliding their original values. IncrementsPerMinute has no default because the
// capture deliberately elides sample values and therefore cannot justify one.
// A blueprint selecting this construct must document its real-world basis beside
// the declared rate.
type Config struct {
	HostName            string  `yaml:"host_name"`
	ServiceName         string  `yaml:"service_name"`
	Source              string  `yaml:"source"`
	IncrementsPerMinute float64 `yaml:"increments_per_minute"`
}

// Construct renders the currently source-backed subset of the native receiver
// surface. The retained artifact contains 195 observed metric names. A name is
// not emitted here merely because it was observed: its data-point value mechanics
// must also be sourced, rather than fabricated from a privacy-elided sample.
type Construct struct {
	hostName            string
	serviceName         string
	source              string
	incrementsPerMinute float64
	start               time.Time
	last                time.Time
	value               float64
}

var _ core.Construct = (*Construct)(nil)

// Build validates a Kubernetes-only receiver declaration. The receiver capture
// proves this path in Kubernetes only; accepting a nil cluster fixture would
// silently turn it into unobserved standalone-host support.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("datadog_receiver: Build called with %T, want *Config", cfg)
	}
	if fx == nil || fx.Cluster == nil {
		return nil, fmt.Errorf("datadog_receiver: fixture.Cluster is required for the observed Kubernetes path")
	}
	if c.HostName == "" || c.ServiceName == "" || c.Source == "" {
		return nil, fmt.Errorf("datadog_receiver: host_name, service_name, and source are required resource attributes")
	}
	if c.IncrementsPerMinute <= 0 || math.IsNaN(c.IncrementsPerMinute) || math.IsInf(c.IncrementsPerMinute, 0) {
		return nil, fmt.Errorf("datadog_receiver: increments_per_minute must be finite, positive, and declaration-backed")
	}
	return &Construct{
		hostName:            c.HostName,
		serviceName:         c.ServiceName,
		source:              c.Source,
		incrementsPerMinute: c.IncrementsPerMinute,
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.OTLPMetrics} }
func (c *Construct) Interval() time.Duration     { return interval }

// Tick emits the one receiver envelope whose value mechanism is explicitly
// declaration-backed. It is a non-monotonic cumulative Sum after the observed
// delta-to-cumulative processor; this makes no claim about receiver input
// temporality. The rate is a declaration value, never an inferred capture value.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if w == nil || w.OTLPMetrics == nil {
		return nil
	}
	if c.start.IsZero() {
		c.start = now
		c.last = now
	}
	// Accrue from actual elapsed time: scheduling cadence must not multiply the rate.
	if now.After(c.last) {
		c.value += c.incrementsPerMinute * now.Sub(c.last).Minutes()
		c.last = now
	}
	return w.OTLPMetrics.Write(ctx, []otlp.MetricResource{{
		Attrs: map[string]any{
			"host.name":    c.hostName,
			"service.name": c.serviceName,
			"source":       c.source,
		},
		Scope: otlp.Scope{Name: translatorScope, Version: translatorVersion},
		Metrics: []otlp.Metric{{
			Name:        exampleMetricIncrement,
			Unit:        "",
			Kind:        otlp.MetricSum,
			Monotonic:   false,
			Temporality: otlp.TemporalityCumulative,
			Numbers: []otlp.NumberPoint{{
				Attrs: map[string]any{}, Start: c.start, Time: now, Value: c.value,
			}},
		}},
	}})
}
