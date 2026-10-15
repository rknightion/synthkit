---
id: SKT-0132
title: >-
  Reconcile legacy HTTP Synthetic Monitoring histogram buckets with the current
  agent
status: Done
assignee:
  - codex
created_date: '2026-10-14 20:28'
updated_date: '2026-10-15 12:34'
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
- [x] #1 The intended HTTP agent profile and exact derived histogram bounds are established from pinned source or capture, with historical differences explicitly classified.
- [x] #2 Any required emitter/catalogue change is proved by a sourced inventory comparison and regression evidence without rewriting captured data.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Pin HTTP derived duration histograms to the same agent v0.66.1 profile as network checks; classify the predecessor subset explicitly, prove the four missing buckets through Tick, reconcile catalogue, and compare explicit blueprint inventory before/after. Root owns final full gate and review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented HTTP duration buckets matching synthetic-monitoring-agent v0.66.1 commit 1046e6eeeb4f5b4ca7becc69a98325346e628c89 scraper updateHistogramFromMetric and its client_golang v1.24.1 DefBuckets. Predecessor seven-bound subset explicitly retained/classified in /Users/rob/repos/synthkit/signals/sm.md; no raw captures edited. Existing TestHistogramLEStyle now crosses public Tick and independently expects the 11 finite upstream bounds plus +Inf. Fail-first log /Users/rob/repos/synthkit/codex/http-fidelity/fail-first.log proves four missing bounds and 8 versus 12 buckets. go test -timeout 2m ./internal/construct/sm passes. Registered local fake API + real provisioner + public just dump exercised through ignored overlay with six explicit checks (HTTP/ping/DNS/TCP/TLS/traceroute); before/after inventories retain identical 32 names and label-key sets. Dump cannot expose label values, so the public Tick regression separately proves exact bounds. Evidence: /Users/rob/repos/synthkit/codex/http-fidelity/registered-before.dump and /Users/rob/repos/synthkit/codex/http-fidelity/registered-after.dump; /Users/rob/repos/synthkit/codex/http-fidelity/inventory-comparison.log. just signals-conformance resolves 32/32, zero parse gaps; just docs-check and bounded-path git diff --check pass. No schema or skill changes required: authoring skill already delegates signal meaning to canonical catalogue. Root owns remaining composed just check and CodeRabbit, then finalization; task remains In Progress.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
HTTP and network derived duration histograms now share the pinned agent v0.66.1 DefBuckets, restoring four sub-100ms HTTP bounds. Registered public dump, fail-first Tick bounds and signal conformance passed; captured data unchanged. Final corrected just check passed; just gen and docs validation passed; initial CodeRabbit and corrective-delta CodeRabbit completed with zero findings. Landed in 0ca33e28187f539f090a423326c63a05e8af501a; exact CI https://github.com/rknightion/synthkit/actions/runs/38732291551 passed every required job. Initial full gate was deliberately interrupted after the chunk-pressure finding and is not claimed green.
<!-- SECTION:FINAL_SUMMARY:END -->
