// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// eval_log: LangSmith run-index log profile — source=langsmith-runs.
//
// Body fields extracted verbatim from internal/workload/webservice/logs.go langsmithRunBody:
//   - msg    (string)  — "run indexed"
//   - aws_env (string) — the blueprint env value
//
// Signals source: signals/langsmith.md [slug: langsmith-runs].
//
// High-card correlation fields from langsmithRunMeta (logs.go) ride as Loki structured
// metadata via Ref (capability matrix routes IsHighCardRef keys to metadata, never body):
//   - trace_id, span_id, run_id, portkey_trace_id, correlation_id, request_id, session_id.
//
// Stream labels are low-cardinality only (I14): source="langsmith-runs" is the identifying
// label that distinguishes this stream from portkey/bedrock_invocation. level varies with
// request outcome (info on success, warn on client-error, error on server-error/throttled) —
// modelled as a weighted enum matching the realistic outcome distribution.

import "github.com/rknightion/synthkit/internal/telemetryspec"

// elStr is a file-local helper that returns a *string (avoids collisions with sibling files).
func elStr(s string) *string { return &s }

func init() {
	register(telemetryspec.Profile{
		Name: "eval_log",
		Logs: []telemetryspec.LogSpec{
			{
				// source=langsmith-runs — the LangSmith run-index log.
				// signals/langsmith.md [slug: langsmith-runs]: stream labels {env, service_name,
				// level, source, cluster, job}; source="langsmith-runs" is the unique discriminator.
				Source: "langsmith-runs",
				// Stream labels (env/service_name/cluster/namespace/job/source/level) are auto-stamped
				// by the app workload from the node identity + request outcome — not author-declared.
				Body: map[string]telemetryspec.ValueModel{
					// --- high-card correlation refs (→ Loki structured metadata via IsHighCardRef) ---
					// All keys below are in internal/highcard.fields, so the capability matrix routes
					// them to structured metadata rather than the JSON body line (I14/I15).

					// trace_id: the request trace ID.
					// signals/langsmith.md [slug: langsmith-runs]: "trace_id" structured-metadata key.
					"trace_id": {Ref: "trace_id"},

					// span_id: the root-span span ID.
					// signals/langsmith.md [slug: langsmith-runs]: "span_id" structured-metadata key.
					"span_id": {Ref: "span_id"},

					// run_id: the LangSmith run identifier — the primary log→trace join key.
					// signals/langsmith.md [slug: langsmith-runs]: "run_id" structured-metadata key.
					"run_id": {Ref: "run_id"},

					// portkey_trace_id: Portkey golden-thread join key (log→gateway-span join).
					// signals/langsmith.md [slug: langsmith-runs]: "portkey_trace_id" structured-metadata key.
					"portkey_trace_id": {Ref: "portkey_trace_id"},

					// correlation_id: the cross-system correlation ID.
					// signals/langsmith.md [slug: langsmith-runs]: "correlation_id" structured-metadata key.
					"correlation_id": {Ref: "correlation_id"},

					// request_id: the per-request ID.
					// signals/langsmith.md [slug: langsmith-runs]: "request_id" structured-metadata key.
					"request_id": {Ref: "request_id"},

					// session_id: the browser/API session ID. ALWAYS body, never stream-label (T12).
					// signals/langsmith.md [slug: langsmith-runs]: "session_id" structured-metadata key.
					"session_id": {Ref: "session_id"},

					// --- body fields from langsmithRunBody (logs.go) ---

					// msg: fixed run-index event message.
					// internal/workload/webservice/logs.go langsmithRunBody.Msg → "run indexed".
					"msg": {ConstStr: elStr("run indexed")},

					// aws_env: the blueprint/deployment environment identifier.
					// internal/workload/webservice/logs.go langsmithRunBody.AWSEnv = w.env.
					// signals/langsmith.md [slug: langsmith-runs]: "aws_env" body field.
					// Modelled as a low-card enum (the workload uses the blueprint env value; we
					// cover the common values). In real emission w.env is a blueprint string passed
					// at construction — here we provide realistic representative values.
					"aws_env": {Enum: []telemetryspec.EnumEntry{
						{Value: "production", Weight: 0.50},
						{Value: "staging", Weight: 0.30},
						{Value: "development", Weight: 0.20},
					}},
				},
			},
		},
	})
}
