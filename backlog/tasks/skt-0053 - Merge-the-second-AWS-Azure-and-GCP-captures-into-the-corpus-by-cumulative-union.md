---
id: SKT-0053
title: >-
  Merge the second AWS, Azure and GCP captures into the corpus by cumulative
  union
status: Parked
assignee:
  - '@codex'
created_date: '2026-09-07 08:10'
updated_date: '2026-09-07 14:04'
labels:
  - corpus
  - reality-corpus
dependencies:
  - SKT-0059
priority: medium
type: feature
ordinal: 149000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The 2026-09-08 cloud lifecycles produced a second reviewed capture for each managed substrate (aws, azure, gcp, raw SHA-256 values frozen in the wave goal). They have the same corpus identity as the 2026-08-31 documents in reality-corpus/00-canon/: same source kind, substrate, collector and collector version 4.5.0, and near-identical direct producer sets, so the loader rightly rejects a second document per identity and a permutation named after a hash or date would be a fabricated configuration. docs/reality-corpus.md steps 5 and 6 already prescribe the answer for repeated measurements of one source configuration: project the new capture, cumulatively merge it into the matching document with CanonicalMerge (structural union, sticky keys, no removal by absence, captured_on and capture provenance move to the newer capture), and keep one route per raw hash in the routing manifest so both hashes stay reviewable. Rename the merged document file to the newest hash so the file name matches source.capture_sha256. No schema change; no exemption; per-substrate divergences the merge surfaces are recorded, not reconciled.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each of the three captures has its own exhaustive route in reality-corpus/manifests/capture-v2-routing.json, every family either directly routed to 00-canon with its exact producer set or unrouted with one exact reason
- [ ] #2 Each existing managed-substrate document is replaced by its CanonicalMerge with the projected new capture, committed as its own commit with the file renamed to the newest hash, and the corpus loader and capture_v2 tests pass
- [ ] #3 just signal-fidelity is green at the integrated SHA with zero exemptions added, and the divergences each merge surfaced are pasted into the task
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2026-09-10: apply the three reviewed routes in AWS, Azure, GCP order; project and CanonicalMerge each same-identity document; rename only when structural evidence moves provenance; validate and commit each substrate separately.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-10 projected and cumulatively merged the reviewed AWS, Azure, and GCP candidates in scratch without changing the immutable raw captures. The frozen direct routes conflict with existing ambiguous_direct_producer corpus evidence and produce 27 unexempted signal-fidelity contradictions. The run allowed no route alteration, additional schema change, or new exemption, so none of the three routes or merged documents was committed and every acceptance criterion remains open. Resume by reconciling the direct-route semantics with the existing ambiguity evidence, then apply each reviewed route and CanonicalMerge in order, rename to the newest capture hash, record divergences, and require green fidelity.

Correction to the previous wave note: only AWS had been projected and merged then; Azure and GCP had not. This wave projected Azure and GCP from their frozen raw captures and cumulatively merged all three in scratch. AWS families 1432 -> 1459, +27 names/+355 label-key pairs; Azure 2110 -> 2185, +75/+994; GCP 1022 -> 1049, +27/+344. All three candidate envelopes validate, all label values are elided and tag_ labels absent. Literal signal-fidelity recipes remain red: AWS 27, Azure 64, GCP 21 unexempted contradictions. No-comparable-producer counts are 7, 7 and 6 against the initial bound 6; AWS and Azure also fail the ratchet. Broad promrw identity is shared across jobs; no label/prefix-derived producer identity was invented. Exact new-family and label-pair lists are retained in the wave scratch structural-delta JSON documents. No corpus document, route or exemption was committed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked before corpus writes. All three cumulative-merge candidates are preserved, but the frozen routes produce 27 unexempted contradictions; no acceptance criterion is claimed.

All three cumulative projections were actually attempted this wave. Promotion remains parked on their measured same-producer contradictions and, for two candidates, ratchet growth. Resume with reviewed job-level shape provenance, not new exemptions.
<!-- SECTION:FINAL_SUMMARY:END -->
