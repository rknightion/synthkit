---
id: SKT-0066
title: Validate P3 job identity after corpus producer projection
status: Done
assignee: []
created_date: '2026-09-08 01:38'
updated_date: '2026-09-08 01:46'
labels: []
dependencies: []
ordinal: 162000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The P3 envelope test treats job only as a compared label. Reprojection correctly consumes it into producer identity, so the test must verify the observed producer while preserving label-key checks.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The P3 envelope test accepts job only through captured label or matching producer identity and still rejects uncaptured labels.
- [x] #2 Fail-first evidence, focused validation and code review are recorded.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Committed3ee1ef840d63f18a2253942eb4fb21fc909ae042 after fail-first uncaptured-job evidence and passing focused/full isolated checks. Test reads observed producer jobs after projection and preserves legacy label representation until the subsequent corpus commit. CodeRabbit minor request to remove legacy fallback was left: the projected document has no job label, so unknown projected jobs already fail; fallback is needed for the independently gated preceding test commit. No production logic changed.

The test correction is committed at 3ee1ef840d63f18a2253942eb4fb21fc909ae042. It accepts an emitted job through its observed producer identity while preserving compatibility with the older label-only document. The expected failure was observed before the correction; focused tests and the isolated full gate passed. The review suggestion to remove legacy compatibility was left with its concrete reason recorded. Production behavior did not change.
<!-- SECTION:FINAL_SUMMARY:END -->
