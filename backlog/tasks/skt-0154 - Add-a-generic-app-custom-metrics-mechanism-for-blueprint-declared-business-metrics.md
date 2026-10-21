---
id: SKT-0154
title: >-
  Add a generic app custom-metrics mechanism for blueprint-declared business
  metrics
status: Done
assignee:
  - '@loop51'
created_date: '2026-10-20T22:20:00Z'
updated_date: '2026-10-21T01:46:00Z'
labels: []
dependencies: []
priority: low
type: feature
ordinal: 287000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Blueprints cannot declare app-defined business metrics (counters, histograms) with names they own. Useful for in-app metrics such as review outcomes or grounding ratios when content capture is off. Names live only in the blueprint and cite their source; the catalog invents none.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A blueprint can declare a custom counter or histogram with labels and a source citation
- [x] #2 Load rejects a declared name without a source
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Extend existing app metric DSL minimally with source admission and public-boundary counter/histogram proofs.
<!-- SECTION:PLAN:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Delivered source-citation-required app custom metrics in f9332c028900c3b6b5b37e738d94430c31111711. Public load-run proof verifies cumulative counters and histograms, bounded labels and native mirroring; absent citations reject. Namespace collision red-green prevents custom, profile and legacy family overlap while legacy-only behavior remains compatible. Schema, canonical guide and skill regenerated/reconciled. Independent repair review, aggregate review with zero findings and full composed check including race passed at pushed tip 802deabd549fa00c0c473ccfbaa882a23891adbc. Citation presence is not authenticity validation.
<!-- SECTION:FINAL_SUMMARY:END -->
