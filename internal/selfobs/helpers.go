// SPDX-License-Identifier: AGPL-3.0-only

package selfobs

import (
	"context"
	"encoding/base64"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// buildResource builds the self-obs resource identity: the REAL generator binary
// (service.name=synthkit), never any synthetic service name a blueprint emits. Caller tags are
// merged over the built-ins.
func buildResource(opts Options) *resource.Resource {
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	host, _ := os.Hostname()
	runMode := "live"
	if opts.DryRun {
		runMode = "dry_run"
	}
	attrs := []attribute.KeyValue{
		attribute.String("service.name", serviceName),
		attribute.String("service.version", version),
		// host + pid: hostname alone is not unique when several synthkit processes share a host
		// (containers/local dev), which collapsed their per-instance series. pid disambiguates.
		attribute.String("service.instance.id", host+"-"+strconv.Itoa(os.Getpid())),
		attribute.String("telemetry.kind", "self-observability"),
		// run_mode: identity/audit. main gates selfobs OFF under DRY_RUN, so emitted telemetry is
		// "live"; a dry_run value means a process bypassed that gate (e.g. a stale pre-gate build) —
		// dashboards filter on run_mode="live" to drop such dev-box noise.
		attribute.String("run_mode", runMode),
	}
	for k, v := range opts.Tags {
		attrs = append(attrs, attribute.String(k, v))
	}
	return resource.NewSchemaless(attrs...)
}

// logBridge turns each std-log line (one Write per log.Printf call) into an OTLP LogRecord. The
// std log package's timestamp prefix (if any) is left in the body; OTLP carries its own timestamp.
type logBridge struct{ logger otellog.Logger }

func (b *logBridge) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	var rec otellog.Record
	now := time.Now()
	rec.SetTimestamp(now)
	rec.SetObservedTimestamp(now)
	rec.SetBody(otellog.StringValue(line))
	sev, txt := severityOf(line)
	rec.SetSeverity(sev)
	rec.SetSeverityText(txt)
	b.logger.Emit(context.Background(), rec)
	return len(p), nil
}

// errSignalRe matches operational failure signals only at ASCII word boundaries. Beyond the
// explicit error/failed/panic words it also catches the common Go failure PHRASES that carry none
// of those words — "context deadline exceeded", "connection refused", "i/o timeout", "timed out",
// "503 Service Unavailable" — which the runner and sinks log verbatim and which were previously
// misread as INFO (so they never reached the ERROR panels). Because '_' is a word char, the word
// boundaries mean this never fires on a metric NAME substring such as aws_rds_..._sample_count,
// otelcol_exporter_send_failed_spans_total, or http_client_request_timeout_total — and the
// [dry-run guard in severityOf still wins, so telemetry PAYLOADS embedding these words stay INFO.
var errSignalRe = regexp.MustCompile(`(?i)\b(errors?|errored|failed|failure|panic|timeout|timed out|deadline|exceeded|refused|unavailable)\b`)

// severityOf infers a log severity from the line content (the generator logs are printf strings,
// not structured). WARN/error prefixes are the only ones worth distinguishing for operability.
func severityOf(line string) (otellog.Severity, string) {
	// Dry-run inventory/summary lines embed telemetry PAYLOADS (metric names, log bodies with
	// level:error, etc.) where "error"/"failed" are DATA, not an operational signal — always INFO.
	if strings.Contains(line, "[dry-run ") {
		return otellog.SeverityInfo, "INFO"
	}
	switch {
	case strings.Contains(line, "WARN"):
		return otellog.SeverityWarn, "WARN"
	case errSignalRe.MatchString(line):
		return otellog.SeverityError, "ERROR"
	default:
		return otellog.SeverityInfo, "INFO"
	}
}

// ParseTags parses a CSV of k=v pairs into a tag map. Entries without '=' or with an empty key are
// skipped; returns nil for empty input.
func ParseTags(csv string) map[string]string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	out := map[string]string{}
	for pair := range strings.SplitSeq(csv, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
