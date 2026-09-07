---
id: SKT-0055
title: Model the Datadog agent receiver path from independent post-gateway captures
status: To Do
assignee: []
created_date: '2026-09-07 11:03'
updated_date: '2026-09-07 12:29'
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

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Authorized live validation environment (Rob, 2026-09-07): stand up an instrumented application that ships to a Datadog agent, and have that agent forward to Alloy through the Alloy Datadog receiver, then on to Grafana Cloud. That chain is what an end user on this path actually gets, so it is the shape to observe. Either an EC2 instance or a deployment into the existing rkps-awsinfra cluster is acceptable; use a namespace named datadog-receiver-validation. Deploy Alloy from its Helm chart. Use the otel-demo application or an equivalent, carrying Datadog-native instrumentation, and include some statsd so metrics, logs and traces are all represented in Datadog-native format. A new construct, workload or blueprint may be created for this purpose. Leave the environment standing at the end of the run for Rob to inspect; he confirms destruction himself. This relates directly to the Portina use case, which stays out of the catalog per the customer-specific identifier rule in AGENTS.md.
<!-- SECTION:NOTES:END -->
