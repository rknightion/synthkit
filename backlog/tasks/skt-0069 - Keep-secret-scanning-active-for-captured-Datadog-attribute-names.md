---
id: SKT-0069
title: Keep secret scanning active for captured Datadog attribute names
status: In Progress
assignee: []
created_date: '2026-09-08 17:30'
updated_date: '2026-09-08 17:38'
labels: []
dependencies: []
priority: medium
ordinal: 165000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Exact-SHA CI flagged three generic-api-key matches in the independently sanitized Datadog envelope artifact. All matches are the two observed attribute keys `_sampling_priority_v1` and `_sampling_priority_rate_v1`, not values or credentials. The full-history scanner therefore needs a narrowly anchored value exception without suppressing any file or credential rule.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Only the two exact observed attribute names are suppressed by the generic-api-key rule
- [x] #2 Failing-before and passing-after evidence preserves generic credentials, a near-miss value and dedicated Grafana token detection in the same fixture file
- [ ] #3 Full-history secret scan and exact-SHA CI pass with the original findings retained as evidence
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Extend the existing scanner-config fixture with the two metadata keys and a non-allowlisted neighboring value; record the failure before adding exact anchored values, then run the positive and negative controls and review the scoped config/script diff.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
CodeRabbit completed with one major guard-specificity finding and one minor Markdown finding. The guard now checks finding source lines and includes a generic credential in the same metadata fixture; the two literal metadata lines must be absent while generic line 4, dedicated Grafana line 3 and the neighboring value remain detected. Markdown keys are code spans. All review findings are being addressed.

Baseline scanner-config gate failed on unsuppressed captured keys. A deliberate broad regex/global path mutant failed near-miss and same-file Grafana controls. After CodeRabbit review, a rule-local path mutant failed the new same-file generic line-4 guard. Final exact config passed all nine line-specific assertions using native gitleaks 8.30.1 (same version as CI); the preceding Docker seven-assertion guard and full-history scan also passed. Evidence under codex/scratch/wave-2026-09-15/gitleaks-*. Exact corrected-SHA CI acceptance remains pending.
<!-- SECTION:NOTES:END -->
