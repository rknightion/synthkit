---
id: SKT-0081
title: Prune change-detector unit tests
status: In Progress
assignee: []
created_date: '2026-09-25 08:05'
updated_date: '2026-09-28 15:12'
labels:
  - testing
dependencies: []
ordinal: 177000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
From the 2026-09-25 fleet test-signal audit (sampled read-only). Delete or consolidate tautological tests (restating the implementation) and change-detector tests (pinning incidental text, markup, counts or internals). Keep parsing, state-machine, retry, security/PII, wire-contract and incident regression tests. Re-verify each candidate before deleting it; the list below comes from a sample and is not exhaustive. Candidates: internal/control/ui/src/utils/fmt.test.ts:1-8 (its header says it pins exact pre-refactor formatter output); internal/control/ui/src/utils/config.test.ts:24-26 (trivial default fallback); scripts/test_docs_validation.py:29 test_valid_fixture (known-good fixture yields zero errors only).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each listed candidate is deleted, consolidated or kept with a one-line reason in the notes
- [ ] #2 Other tests in the same pattern found during the work are handled the same way
- [ ] #3 The repo's check recipe passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Sweep owned UI/Python tests; document candidate decisions and mutation evidence; run focused gates and just check.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop45: admitted
<!-- SECTION:NOTES:END -->
