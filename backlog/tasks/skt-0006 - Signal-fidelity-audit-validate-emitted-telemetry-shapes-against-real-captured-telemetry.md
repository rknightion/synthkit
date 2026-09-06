---
id: SKT-0006
title: >-
  Signal fidelity audit: validate emitted telemetry shapes against real captured
  telemetry
status: Done
assignee: []
created_date: '2026-08-24 11:32'
updated_date: '2026-09-06 09:28'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 25000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
synthkit emits ~9,830 lines worth of declared signal contract across signals/*.md, but the only comparison against reality is a human reading prose against `synthkit -once -dump` text output. That structurally cannot catch label-VALUE drift (`-dump` prints label keys only), instrument-type drift, or bucket-bound drift — which is where nearly all real divergence lives (CloudWatch stat suffixes, `mode` enums, `condition`/`status` pairs, `_sum` gauge-vs-rate).

The outcome: a machine-readable inventory on both sides (what synthkit emits, and what real Grafana Cloud collectors actually ship), a committed provenance-stamped reality corpus, and a CI leg that reports divergence on every PR. Two corpus producers: a k3d lab that runs locally and nightly (covers the generic k8s-monitoring + application-observability surface that the "base" blueprints model and that most users will exercise), and a `gcx` read-back against an operator-selected live EKS stack (covers the AWS/EKS-specific identity a k3d cluster cannot produce).

Scope note: no Terraform/EKS lab. A real EKS cluster already runs the exact chart under audit with k8s-monitoring 4.4.0; capture from it via gcx rather than rebuilding it.

Existing seams to build on, not replace: `e2e/receiver/` already decodes RW2 / OTLP metrics / OTLP traces / Loki push and exposes `/__inventory`; `cmd/synthkit` `printInventory` already walks every sink; `internal/capture` is the precedent for a capture binary with zero synthkit imports.

Naming constraint: this repository is public and carries a forbidden-words guard in `make hygiene`. Never name a live Grafana Cloud stack, account, or tenant in tracker text, code, docs, or commit messages — say "an operator-selected stack". A term committed here fails CI on every subsequent push until removed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Both sides (synthkit emission and real captured collector egress) produce the same machine-readable inventory schema
- [x] #2 A committed reality corpus records real captured shapes with provenance (substrate, chart version, capture date) and a documented cardinality-elision policy
- [x] #3 Every PR runs an inventory diff against the corpus and reports findings; report-only on first landing (does not fail the build)
- [x] #4 A k3d capture lab runs both as a local one-shot make target and as a nightly CI job
- [x] #5 A gcx read-back path merges real EKS-specific label names and values into the corpus
- [x] #6 Coverage gaps found by the audit are routed to cantfind.md PENDING items, not silently dropped
- [x] #7 docs/ documents how to run each capture path and how to refresh the corpus
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [x] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-06 parent reconciliation against the five Done subtasks. AC1: SKT-0006.01 landed the shared canonical inventory schema, synth JSON export and canonical e2e receiver output. AC2: SKT-0006.02 landed the frozen v1alpha1 substrate-scoped corpus with provenance (substrate, chart version, capture date) and the values_elided cardinality policy documented in docs/reality-corpus.md. AC3: SKT-0006.02 landed the report-only PR gate; SKT-0010.05 later gave contradictions teeth, coverage gaps stay report-only. AC4: SKT-0006.03 landed the k3d lab as a local recipe (now just lab) and the nightly signal-fidelity-k3d workflow (run 32739962451 published an artifact). AC5: SKT-0006.04 merged 588 CloudWatch and 31 EKS Kubernetes contracts from the operator-selected live read-back with EKS label names and values. AC6: coverage gaps route to cantfind.md PENDING stubs (SKT-0006.02 produced 97 stubs; SKT-0010.04 gave every gap a recorded verdict). AC7: docs/reality-corpus.md documents both producers (k3d lab, gcx read-back with just corpus-gcx) and the safe refresh sequence. Nothing in this parent was checked from a child's status alone; each line above names the child evidence.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-09-06: Parent reconciled Done at 7/7 from the five Done subtasks' recorded evidence (inventory schema, frozen corpus with provenance, report-only then enforced gate, k3d lab local plus nightly, gcx EKS read-back, PENDING routing, refresh docs). DoD: just check green on main at this reconciliation, no blueprint field changed, explicit safe dump 2917 Prometheus and 430 OTLP names recorded by the 2026-09-06 wave report.
<!-- SECTION:FINAL_SUMMARY:END -->
