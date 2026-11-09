// SPDX-License-Identifier: AGPL-3.0-only

// Package otlp_native builds HTTP operations views from the native OTLP reference workload.
// The query projection is the explicit-histogram gateway contract in signals/otlp-metrics.md,
// not a claim that every OTLP input becomes a Prometheus native histogram.
package otlp_native

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rknightion/synthkit/dashboard"
)

// Templates supplies the native HTTP application view alongside the generated inventory views.
func Templates() []dashboard.Template {
	return []dashboard.Template{HTTP}
}

// HTTP compares the enriched and SDK-only services using labels promoted for both by the
// captured gateway. Unsupported infrastructure, logs, traces and spanmetrics are left to
// their own views; no availability is inferred from an absent native metric.
func HTTP(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	services := make([]string, 0)
	for _, workload := range m.Workloads {
		if workload.Kind != "web_service" {
			continue
		}
		found := false
		for _, source := range m.OTLPMetrics {
			if source.Service != workload.Name {
				continue
			}
			found = slices.Contains(source.MetricNames, "http.server.request.duration") &&
				slices.Contains(source.MetricNames, "http.server.active_requests") &&
				slices.Contains(source.ResourceKeys, "service.name") &&
				slices.Contains(source.ResourceKeys, "service.namespace") &&
				slices.Contains(source.ResourceKeys, "service.instance.id") &&
				slices.Contains(source.ResourceKeys, "deployment.environment.name")
			break
		}
		if !found {
			return dashboard.Dashboard{}, fmt.Errorf("otlp-native HTTP dashboard: service %q did not emit the required native HTTP instruments and resource identity", workload.Name)
		}
		services = append(services, workload.Name)
	}
	if len(services) == 0 || len(m.Environments) == 0 {
		return dashboard.Dashboard{}, fmt.Errorf("otlp-native HTTP dashboard: no native HTTP service/environment identity")
	}
	var environments []string
	for _, environment := range m.Environments {
		environments = append(environments, environment.Name)
	}
	selector := `{service_name=~` + quotedAlternation(services) + `}`
	errors := strings.TrimSuffix(selector, "}") + `,http_response_status_code=~"5.."}`
	// Environment is retained on target_info, not promoted to ordinary native HTTP
	// series (same-source dual-ingest capture, 2026-08-31). Filter each instance before
	// aggregation. A set intersection tolerates duplicate target_info from another
	// ingestion lane without multiplying samples, and absent identity stays absent.
	identity := `target_info{service_name=~` + quotedAlternation(services) + `,deployment_environment_name=~` + quotedAlternation(environments) + `}`
	requests := `rate(http_server_request_duration_seconds_count` + selector + `[5m]) and on (job, instance) ` + identity
	failed := `rate(http_server_request_duration_seconds_count` + errors + `[5m]) and on (job, instance) ` + identity
	buckets := `rate(http_server_request_duration_seconds_bucket` + selector + `[5m]) and on (job, instance) ` + identity
	active := `http_server_active_requests` + selector + ` and on (job, instance) ` + identity

	d, err := dashboard.NewDashboard(m.Blueprint+"-http", "Native OTLP HTTP services")
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	dashboard.AddPanel(&d, "http-header", dashboard.TextPanel("Native HTTP application metrics",
		"# Native HTTP application metrics\n\nCompare the Kubernetes-enriched and SDK-only services using service identity and target_info's deployment environment. Requests are filtered by job and instance before aggregation. Five-minute rate windows cover this reference's one-minute metric cadence. Request duration uses the captured gateway's classic bucket projection in seconds; active requests is a gauge. Missing samples or target_info mean data is unavailable, not healthy zero.\n\n[Emitted remote-write metric inventory](/d/"+m.Blueprint+"-metrics) is a separate view. Native addon metrics, logs, traces and backend-derived spanmetrics are not covered here."))
	dashboard.AddPanel(&d, "request-rate", dashboard.TimeseriesPanel("Request rate by service", "reqps",
		dashboard.PromTarget("sum by (service_name) ("+requests+")", "{{service_name}}")))
	dashboard.AddPanel(&d, "error-ratio", dashboard.TimeseriesPanel("Server error share by service", "percentunit",
		dashboard.PromTarget("sum by (service_name) ("+failed+") / sum by (service_name) ("+requests+")", "{{service_name}}")))
	dashboard.AddPanel(&d, "latency", dashboard.TimeseriesPanel("Request duration by service", "s",
		dashboard.PromTarget("histogram_quantile(0.50, sum by (service_name, le) ("+buckets+"))", "{{service_name}} p50").RefId("A"),
		dashboard.PromTarget("histogram_quantile(0.95, sum by (service_name, le) ("+buckets+"))", "{{service_name}} p95").RefId("B"),
		dashboard.PromTarget("histogram_quantile(0.99, sum by (service_name, le) ("+buckets+"))", "{{service_name}} p99").RefId("C")))
	dashboard.AddPanel(&d, "active-requests", dashboard.TimeseriesPanel("Active requests by service", "short",
		dashboard.PromTarget("sum by (service_name) ("+active+")", "{{service_name}}")))
	dashboard.WithRows(&d,
		dashboard.Section("", dashboard.At("http-header", 24, 5)),
		dashboard.Section("Traffic and failures", dashboard.Half("request-rate"), dashboard.Half("error-ratio")),
		dashboard.Section("Latency and concurrency", dashboard.Half("latency"), dashboard.Half("active-requests")))
	return d, nil
}

func quotedAlternation(values []string) string {
	values = slices.Clone(values)
	sort.Strings(values)
	for i := range values {
		values[i] = regexp.QuoteMeta(values[i])
	}
	return strconv.Quote(strings.Join(slices.Compact(values), "|"))
}
