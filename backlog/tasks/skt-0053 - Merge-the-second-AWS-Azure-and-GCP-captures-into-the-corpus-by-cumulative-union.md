---
id: SKT-0053
title: >-
  Merge the second AWS, Azure and GCP captures into the corpus by cumulative
  union
status: To Do
assignee:
  - '@codex'
created_date: '2026-09-07 08:10'
labels:
  - corpus
  - reality-corpus
dependencies: []
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
