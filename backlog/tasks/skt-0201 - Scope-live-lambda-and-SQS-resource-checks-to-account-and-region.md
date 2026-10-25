---
id: SKT-0201
title: Scope live lambda and SQS resource checks to account and region
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T12:09:00Z'
updated_date: '2026-10-25T13:22:00Z'
labels:
  - causal
dependencies: []
ordinal: 405000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Follow-up to SKT-0198. The scoped live queries in internal/failuremode/assertions.go (around lines 120 and 124) filter lambda and SQS series by function or queue name only, so a same-named function or queue in another account or region still matches a live check. The bound-consumer identities now carry account_id and region; the query templates should use them. Evidence: /Users/rob/repos/synthkit/codex/loop58/cid/return.md.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Live scoped checks for lambda and SQS resources match only series with the bound consumer's account_id and region when those are known
- [ ] #2 Scope tests and acceptance scenario tests updated; queries unchanged where account or region is absent
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
