// SPDX-License-Identifier: AGPL-3.0-only

// Package sigil is the Grafana AI-Observability ("sigil") VOCABULARY + content mechanic lib —
// a peer to internal/cw and internal/genai (a mechanic lib, not a construct). It holds the
// sigil.* attribute keys, generation/workflow-step/score field-name constants, operation names,
// content-capture modes, token-type values, the sigil_eval_* metric names + advisory buckets,
// the deterministic effective-version digest, the embedded content corpus + its deterministic
// assembler, and the sink-input seam types (Export and friends).
//
// It emits NOTHING itself and imports stdlib + the protobuf-encoded sink types only — it is NOT
// an OTel SDK and pulls in no grpc (the sigil sink ships protojson over HTTP; ARCHITECTURE I2).
// The aiagent workload builds gen_ai/sigil spans + gen_ai_client_*/sigil_eval_* metrics + the
// native Generation/WorkflowStep/Score ingest FROM these constants and seam types.
//
// Names are LAW: every attribute key, operation value, metric name, field name, and enum value is
// sourced VERBATIM from the live sigil contract (signals/sigil.md) — captured from the grafana
// sigil backend (~/repos/sigil), the sigil-sdk (~/repos/sigil-sdk), and live stacks (m7kni coding
// agents + emea-cloud-demokit general agents, 2026-06-30). Never invent a name.
//
// This is the SANCTIONED exception to genai.ContentStripList: the sigil generation lane carries
// request/response CONTENT (the only source of AI-Observability conversations). The strip-list
// guard stays default-on for every other construct/workload.
package sigil
