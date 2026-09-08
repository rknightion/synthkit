---
id: SKT-0055
title: Model the Datadog agent receiver path from independent post-gateway captures
status: Parked
assignee:
  - '@codex'
created_date: '2026-09-07 11:03'
updated_date: '2026-09-08 17:23'
labels:
  - integration
dependencies: []
references:
  - AGENTS.md
  - SIGNALS.md
  - internal/inventory/schema.go
  - docs/reality-corpus.md
priority: medium
type: feature
ordinal: 151000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Migration rehearsal needs a separate telemetry contract for estates temporarily retaining the Datadog agent behind an Alloy Datadog receiver. The current native integration catalog does not represent this path. This is an independent later capability: native integration rehearsal remains usable without it. An external consumer can supply expected mappings, but observed wire/readback evidence is authoritative and must not be rewritten to match those expectations. Public tasks, fixtures and blueprints describe only generic host/Kubernetes shapes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An independently captured host and Kubernetes agent path records Datadog agent, Alloy receiver and gateway versions/settings with metric names, instrument/histogram semantics and promoted label keys; runtime credentials and stack choice require explicit authorization and are excluded from published evidence.
- [ ] #2 A reviewed signals contract and sanitized reality-corpus evidence distinguish receiver-specific counter suffixes, attribute promotion, host-label absence and unsupported families from native exporter contracts; unknown evidence remains explicit.
- [ ] #3 Selectable host and Kubernetes blueprint declarations emit the supported observed receiver surface through the existing isolated construct and runner architecture; no private consumer code or customer-shaped blueprint is imported.
- [ ] #4 Inventory and fidelity comparisons verify the receiver selection against capture, including divergence and absent-evidence cases; generated blueprint documentation and an explicitly synthetic example expose support and limits. Native defaults remain unchanged.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Build only the authorized isolated environment through ArgoCD and pinned Helm resources. Keep identity and credential values runtime-only. Verify receiver capabilities first; current Alloy documentation supports metrics and traces, not logs. Retain every created resource and label unsupported log evidence explicitly. Capture pre-ingest and use the single reserved post-ingest read-back only after positive upstream evidence.

Wave 2026-09-13 implements the receiver envelope-preservation prerequisite only, keeping flattened callers compatible; broader Datadog construct and capture acceptance stays open.

Wave 2026-09-15: inspect standing sync policy; temporarily route the existing capture Service to an independent raw OTLP recorder, preserve metric and trace envelopes, restore the selector and remove the temporary Pod. Compare against retained post-gateway evidence and spend the dedicated post-ingest query only after observed capture. No source Application edit, logs, or selectable-construct completion is implied.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Authorized live validation environment (Rob, 2026-09-07): stand up an instrumented application that ships to a Datadog agent, and have that agent forward to Alloy through the Alloy Datadog receiver, then on to Grafana Cloud. That chain is what an end user on this path actually gets, so it is the shape to observe. Either an EC2 instance or a deployment into the existing rkps-awsinfra cluster is acceptable; use a namespace named datadog-receiver-validation. Deploy Alloy from its Helm chart. Use the otel-demo application or an equivalent, carrying Datadog-native instrumentation, and include some statsd so metrics, logs and traces are all represented in Datadog-native format. A new construct, workload or blueprint may be created for this purpose. Leave the environment standing at the end of the run for Rob to inspect; he confirms destruction himself. This relates directly to the Portina use case, which stays out of the catalog per the customer-specific identifier rule in AGENTS.md.

Wave 2026-09-11 built the authorized isolated native Agent -> experimental Alloy Datadog receiver -> delta-to-cumulative -> batch -> cloud and capture exporter paths. The environment is intentionally left standing, including failed or rebuilding resources, for operator inspection; these are not orphans. Independent pre-ingest evidence contains 202 gauge families, one trace shape, 7441 metric datapoints and 11 spans, zero logs. Those receipt counters are not HTTP request counts. One authorized post-ingest series-only read-back returned one explicit metric family. Both privacy-elided derived evidence routes are retained separately; job is declared as observed read-path enrichment, while missing host/source keys remain differences. Correction to the earlier plan: logs are unproven, not unsupported; pinned upstream source supports logs, but the Agent path was disabled. Native-envelope reconstruction is parked because the capture receiver flattens resource and datapoint attributes and does not retain scope/schema/unit/temporality. A delivery path alone is not an independent construct boundary. No construct, blueprint or corpus promotion is claimed.

Live construction needed trace_id_cache_size = 100 because the pinned Alloy default zero failed construction. The runtime Application carries that override and Alloy-only injection opt-out. A source follow-up commit was rejected fetch first; no reconciliation or second push was attempted. Generic chart source is published, injection follow-up is local, and the cache fix remains uncommitted. Automatic admission injection caused scheduling pressure; no global collector/injector settings changed. dd_internal_stats_payload was rejected by the cloud for timestamp zero; positive evidence for another family does not make that rejection green. Resume with explicit publication authority, envelope-preserving capture, source-specific catalogue ownership and native logs proof. Both read-back allowances are consumed.

Correction: the Alloy Datadog receiver output exposes metrics and traces only. Although its upstream config carries logs fields, this component has no routable logs output; logs are out of scope for this path. The standing environment remains intended retained validation state. Receipt counters are datapoints and spans, not requests.

Receiver envelope-preservation prerequisite committed in 4dbe491: deep-copied resource, scope and datapoint placement, scope metadata, schema URLs, unit, temporality and monotonicity are available separately while Snapshot remains flattened. Focused and race tests passed. The broader Datadog host/Kubernetes capture and selectable construct acceptance remains open; resume with an authorized new capture using the preserved envelopes. No such capture or live receipt is claimed this wave.

Wave 2026-09-14 preflight: AWS SSO session expired or invalid. Per the frozen goal, both the independent envelope capture and deployed read-back stop at authentication; no recovery, cluster mutation, capture or Grafana read-back was attempted. Both earmarked read-backs remain unused. Logs are excluded by the Alloy component output contract, not deferred.

Wave close: AWS SSO failed at preflight. No recovery attempted under the goal auth stop; no namespace operation or Datadog capture occurred, and both read-back allowances remain unused. Metrics/traces remain authorized; logs are excluded as a component limitation. Resume after authentication is restored: inspect Application sync policy read-only, prefer an ephemeral envelope capture, preserve the environment, and stop before any durable infrastructure-source change.

Wave 2026-09-15: independent native envelope capture succeeded. Sanitized evidence e2e/acceptance/datadog-native-envelope-2026-09-08.json preserves placement, scope/schema, units, instrument semantics and attribute value types across 11 raw OTLP requests: 8 metrics, 3 traces; 195 metric names, 3810 datapoints, 3 spans. Named post-ingest metric read-back succeeded. Temporary recorder removed; capture Service selector restored and four standing deployments available. The first selector attempt self-healed before receipts; joining the existing selector plus one authorized collector restart succeeded. No Application/source edit. Logs excluded. Earlier post-gateway evidence remains separately attributed. No complete host path, reviewed corpus producer projection, selectable construct or trace-ingestion claim.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked with partial metrics/traces hop evidence and a deliberately standing environment. Full logs/host-path/envelope/corpus/construct acceptance is unproven; preserve the environment until the operator confirms destruction.

Current resume boundary: establish working AWS authentication outside this run, then inspect the standing Application sync policy read-only before using an ephemeral envelope-preserving metrics/traces capture path. Preserve the standing environment. No current deployment identity, ingestion or resource-count claim is made from this failed preflight.

Wave close: AWS SSO failed at preflight. No recovery attempted under the goal auth stop; no namespace operation or Datadog capture occurred, and both read-back allowances remain unused. Metrics/traces remain authorized; logs are excluded as a component limitation. Resume after authentication is restored: inspect Application sync policy read-only, prefer an ephemeral envelope capture, preserve the environment, and stop before any durable infrastructure-source change.

Capture boundary advanced: native metrics/traces envelope evidence is now retained and reviewed, with the named metric ingested. The earlier no-envelope/no-auth resume statement is historical. Broader 0/4 acceptance remains open: next implement the independently scoped host/Kubernetes receiver surface and its reviewed corpus producer mapping from the preserved evidence; leave the validation environment standing.
<!-- SECTION:FINAL_SUMMARY:END -->
