---
id: SKT-0195
title: Count caller resends of a callee's subtree failures as demand
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T11:41:00Z'
updated_date: '2026-10-25T14:00:00Z'
labels:
  - causal
dependencies: []
ordinal: 399000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Follow-up to SKT-0168.09.33. Callers that retry 5xx resend whole subtrees, but the causal model adds no demand for those resends (v1 rule): with a 5xx-retrying caller the function's attempt count stays at 3600 against 10800 app attempts. Enabling it naively causes a throttle storm because a fast-failing call is held for its full healthy duration; it needs the failed call's real hold time first. Evidence: /Users/rob/repos/synthkit/codex/loop58/dmd/return.md (D2).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Resends of subtree failures add demand at the callee in proportion to caller retries
- [ ] #2 Fast-failing calls hold the resource only for their failure duration, and the certificate stories keep their current outcome
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
CodeRabbit on landing 10 (internal/causal/flow.go serve): the outcome mapped through CallerStatus keeps rule: f.key.rule, so callers above the rule's direct caller never resend a mapped rule rejection, though docs/workloads.md says only the direct caller suppresses it. Fix together with this task: drop the rule flag on mapped outcomes.
<!-- SECTION:NOTES:END -->
