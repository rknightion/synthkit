---
id: SKT-0081
title: Prune change-detector unit tests
status: Done
assignee: []
created_date: '2026-09-25 08:05'
updated_date: '2026-09-28 17:00'
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
- [x] #1 Each listed candidate is deleted, consolidated or kept with a one-line reason in the notes
- [x] #2 Other tests in the same pattern found during the work are handled the same way
- [x] #3 The repo's check recipe passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
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

loop45: fmt legacy magnitude/NaN pins consolidated (retain boundaries); configValue trivial fallback deleted (secret/URL contracts remain); docs test_valid_fixture deleted (error/parse regressions remain). Schema glyph sort assertion consolidated to row ordering; BpManage and Overview exact empty-state copy consolidated to semantic state; Config CSS/unset assertions deleted (secret canary retained); Status magic count deleted (lane dispositions retained). Other owned tests reviewed and meaningful branch/security/wire/incident cases retained. Five temporary mutations failed and reverted. just gen N/A (no blueprint field, config struct or skill change); just dump N/A (no emission code changed). Attempts SKT-0081-impl-1, infrastructure retries 1 (resource contention timeout on unchanged commit, green retry); root combined just check passed; landing 1d7d430c0c788ecbb7488b9dd80a8ae388c37c1d, ci 36453426042 success.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Pruned incidental UI and Python test assertions while preserving behavior contracts. Focused UI/Python checks, five mutation checks, combined just check and exact-SHA landing CI 36453426042 passed; evidence codex/scratch/loop45/T1/evidence.md.
<!-- SECTION:FINAL_SUMMARY:END -->
