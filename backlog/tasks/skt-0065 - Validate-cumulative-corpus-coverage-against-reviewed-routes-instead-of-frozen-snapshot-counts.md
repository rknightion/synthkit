---
id: SKT-0065
title: >-
  Validate cumulative corpus coverage against reviewed routes instead of frozen
  snapshot counts
status: Done
assignee: []
created_date: '2026-09-08 00:47'
updated_date: '2026-09-08 01:27'
labels: []
dependencies: []
ordinal: 161000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The checked-in capture gate assumes seven single-capture documents and exact one-hash metric sets. Authorized CanonicalMerge promotions require cumulative documents under existing identities. Replace snapshot totals with relational route coverage and reject unknown capture provenance or missing routed families.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 All reviewed routes are covered by the cumulative document for their existing identity
- [x] #2 Unknown provenance and missing routed families remain test failures
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Committed 99517265782c46a42d19d2238912a622fb9228a3. Cumulative coverage validation replaces fixed seven-document snapshot assertions while preserving unknown-provenance and missing-route failures. CodeRabbit zero findings and full isolated gate passed. AWS, Azure and GCP cumulative promotions then passed independently under stable corpus paths. No blueprint field changed.
<!-- SECTION:FINAL_SUMMARY:END -->
