---
id: SKT-0006
title: >-
  Signal fidelity audit: validate emitted telemetry shapes against real captured
  telemetry
status: To Do
assignee: []
created_date: '2026-08-24 11:32'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 25000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
synthkit emits ~9,830 lines worth of declared signal contract across signals/*.md, but the only comparison against reality is a human reading prose against `synthkit -once -dump` text output. That structurally cannot catch label-VALUE drift (`-dump` prints label keys only), instrument-type drift, or bucket-bound drift — which is where nearly all real divergence lives (CloudWatch stat suffixes, `mode` enums, `condition`/`status` pairs, `_sum` gauge-vs-rate).

The outcome: a machine-readable inventory on both sides (what synthkit emits, and what real Grafana Cloud collectors actually ship), a committed provenance-stamped reality corpus, and a CI leg that reports divergence on every PR. Two corpus producers: a k3d lab that runs locally and nightly (covers the generic k8s-monitoring + application-observability surface that the "base" blueprints model and that most users will exercise), and a `gcx` read-back against the live robk EKS stack (covers the AWS/EKS-specific identity a k3d cluster cannot produce).

Scope note: no Terraform/EKS lab. A real EKS cluster already runs the exact chart under audit (m7kni/rkps-awsinfra, k8s-monitoring 4.4.0); capture from it via gcx rather than rebuilding it.

Existing seams to build on, not replace: `e2e/receiver/` already decodes RW2 / OTLP metrics / OTLP traces / Loki push and exposes `/__inventory`; `cmd/synthkit` `printInventory` already walks every sink; `internal/capture` is the precedent for a capture binary with zero synthkit imports.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both sides (synthkit emission and real captured collector egress) produce the same machine-readable inventory schema
- [ ] #2 A committed reality corpus records real captured shapes with provenance (substrate, chart version, capture date) and a documented cardinality-elision policy
- [ ] #3 Every PR runs an inventory diff against the corpus and reports findings; report-only on first landing (does not fail the build)
- [ ] #4 A k3d capture lab runs both as a local one-shot make target and as a nightly CI job
- [ ] #5 A gcx read-back path merges real EKS-specific label names and values into the corpus
- [ ] #6 Coverage gaps found by the audit are routed to cantfind.md PENDING items, not silently dropped
- [ ] #7 docs/ documents how to run each capture path and how to refresh the corpus
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
