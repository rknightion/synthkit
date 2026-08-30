---
id: SKT-0031
title: >-
  Make all shipped blueprints individually deployable and verifiable by runtime
  identity
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-08-30 14:17'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: bug
ordinal: 122000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios C1, C4, and C5 selected every shipped blueprint individually. Only 18 of 26 became healthy within the bounded wait; some timed out after logging that synthkit was up, filenames did not always match selectable runtime names, immediate live read-back returned no frames, and the control inventory did not expose per-blueprint identity evidence needed to compare dump families with landed data.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The canonical selectable runtime name for every shipped blueprint is discoverable without inferring it from the filename
- [x] #2 Every shipped blueprint reaches a truthful healthy or setup state individually within a documented bound, or reports the lane preventing readiness
- [x] #3 The verification path waits for two tick intervals and proves each declared signal class arrived
- [x] #4 Dump families can be compared to live queryability per blueprint without a hand-built mapping
- [x] #5 Representative substrate-scoped and blueprint-scoped families expose enough evidence to verify label separation
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane B exposes canonical runtime identity and declared signal verification; root proves all shipped blueprints live after integration.

2026-08-31 Lane C: extend the identity/readiness verifier to all 26 canonical runtime names with bounded truthful healthy/setup/lane-failure verdicts; root runs it live after integration.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: runtime identity, declared-signal verification, live query mapping, and identity projections landed and passed the final gates. Live verification covered the eight-blueprint acceptance deployment, not every one of the 26 shipped blueprints individually.

2026-08-31 verification: the canonical verifier enumerated all 28 runtime identities with bounded concurrency. Twenty completed every declared live signal assertion; eight returned explicit named lane failures: missing traces_host_info in two identities, missing gen-AI bucket in one, stale Loki success in four, and a Sigil error in one. The verifier did not hide or infer any identity.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked at AC#2. Resume by running the executable identity/readiness check for all 26 canonical runtime names, retaining each truthful healthy/setup result or named lane failure within the documented bound.

2026-08-31: Parked after proving the verifier across all 28 canonical identities. Resume from the eight retained live lane failures and rerun just e2e-identity until each identity reaches healthy or an intentional setup state with every declared signal class landed.
<!-- SECTION:FINAL_SUMMARY:END -->
