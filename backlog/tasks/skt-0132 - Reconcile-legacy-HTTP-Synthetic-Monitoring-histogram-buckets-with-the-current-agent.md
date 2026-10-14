---
id: SKT-0132
title: >-
  Reconcile legacy HTTP Synthetic Monitoring histogram buckets with the current
  agent
status: To Do
assignee: []
created_date: '2026-10-14 20:28'
labels:
  - needs-triage
dependencies: []
references:
  - >-
    https://github.com/grafana/synthetic-monitoring-agent/blob/1046e6eeeb4f5b4ca7becc69a98325346e628c89/internal/scraper/scraper.go
priority: low
ordinal: 255000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Pinned synthetic-monitoring-agent v0.66.1 source uses Prometheus DefBuckets for the derived duration histogram, including sub-100ms buckets absent from the existing HTTP contract. Network check admission preserves existing HTTP output deliberately; assess the historical capture versus current agent before changing HTTP fidelity and its source catalogue.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The intended HTTP agent profile and exact derived histogram bounds are established from pinned source or capture, with historical differences explicitly classified.
- [ ] #2 Any required emitter/catalogue change is proved by a sourced inventory comparison and regression evidence without rewriting captured data.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
