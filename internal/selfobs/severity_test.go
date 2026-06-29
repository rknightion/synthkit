// SPDX-License-Identifier: AGPL-3.0-only

package selfobs

import "testing"

// TestSeverityOf guards against the false-positive ERROR classification that would otherwise light
// up red self-obs lines: dry-run inventory summaries embed telemetry PAYLOADS (metric names like
// aws_rds_..._client_errors_sample_count, log bodies with level:error) that contain "error"/
// "failed" as DATA, not as an operational signal. Only genuine generator log lines should be ERROR.
func TestSeverityOf(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		// FALSE POSITIVES that previously classified ERROR — must be INFO.
		{"metric-name-client-errors", `2026/06/13 18:05:47 [dry-run promrw] 140 series e.g. aws_alb_httpcode_target_5xx_count_sum[blueprint=initech]=0.313`, "INFO"},
		{"metric-name-server-errors", `[dry-run promrw] 130 series e.g. aws_rds_database_connections_sample_count[]=0.001`, "INFO"},
		{"dry-run-loki-embeds-level-error", `[dry-run loki] 18 streams e.g. map[level:error blueprint:initech] "{\"outcome\":\"server_error\"}"`, "INFO"},
		{"metric-name-send-failed", `[dry-run promrw] 100 series e.g. otelcol_exporter_send_failed_spans_total[]=2.0`, "INFO"},

		// GENUINE operational lines — must keep their level.
		{"real-error", `runner: master tick: connection refused error`, "ERROR"},
		{"real-tick-error", `runner: blueprint "initech" construct "ec2": tick error: timeout`, "ERROR"},
		{"real-failed", `selfobs: failed to start: bad endpoint`, "ERROR"},
		{"real-warn", `[promrw] WARN series-cap hit: batch=5000 > SERIES_CAP=4000`, "WARN"},
		{"plain-info", `selfobs: heartbeat ledger=42`, "INFO"},

		// Failure PHRASES that carry no error/failed/panic word — these are real failures the runner
		// and sinks log (context deadline exceeded, connection refused, …) that were previously misread
		// as INFO and so never reached the ERROR panels. They must classify ERROR.
		{"context-deadline-exceeded", `2026/06/13 12:36:24 runner: blueprint "initech" workload "initech-api": remote_write: context deadline exceeded`, "ERROR"},
		{"connection-refused", `runner: blueprint "newco" construct "prod": dial tcp: connection refused`, "ERROR"},
		{"io-timeout", `[otlp] export: Post "https://x/v1/traces": i/o timeout`, "ERROR"},
		{"timed-out", `selfobs: shutdown timed out`, "ERROR"},
		{"service-unavailable", `[loki] push: 503 Service Unavailable`, "ERROR"},
		{"shutdown-deadline-exceeded", `synthkit: shutdown deadline (10s) exceeded — forcing exit`, "ERROR"},

		// Ordering contracts: WARN wins over an error-word; the [dry-run guard wins over the regex.
		{"warn-beats-error-word", `[promrw] WARN export failed, retrying`, "WARN"},
		{"dryrun-guard-beats-panic", `[dry-run promrw] 1 series e.g. some_panic_total[]=1`, "INFO"},
		{"server-error-bare-word-not-error", `request completed outcome=server_error`, "INFO"},
		// The dry-run guard must also win over the new failure-phrase words (metric names / payloads
		// embedding them as DATA stay INFO).
		{"dryrun-guard-beats-timeout-word", `[dry-run promrw] 1 series e.g. http_client_request_timeout_total[]=1`, "INFO"},
		{"dryrun-guard-beats-exceeded", `[dry-run loki] 2 streams e.g. map[msg:rate_limit exceeded]`, "INFO"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got := severityOf(c.line)
			if got != c.want {
				t.Errorf("severityOf(%q) = %s, want %s", c.line, got, c.want)
			}
		})
	}
}
