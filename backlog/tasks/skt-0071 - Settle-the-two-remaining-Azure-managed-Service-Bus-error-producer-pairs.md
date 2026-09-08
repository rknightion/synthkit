---
id: SKT-0071
title: Settle the two remaining Azure managed Service Bus error producer pairs
status: To Do
assignee: []
created_date: '2026-09-08 22:07'
updated_date: '2026-09-08 22:07'
labels:
  - corpus
  - needs-triage
dependencies: []
references:
  - reality-corpus/verdicts/producer-coverage.json
priority: medium
ordinal: 167000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The independent completed-soak Azure capture settled 23 of 25 managed producer claims. The exact servererrors_total_count and usererrors_total_count signal/producer selectors remained empty while eight sibling claim families were observed. A separate six-message signed send/receive exercise succeeded and a malformed-header request returned 400; neither proves an exported error family. No server fault was induced. Absent metadata remains unknown and absence is not unsupportedness. The immutable final capture hash and pair-level reasons are recorded in reality-corpus/cspazure/managed-paired.json and reality-corpus/verdicts/producer-resolution-2026-09-08.json. This is a follow-up to genuinely unsettled campaign acceptance, not permission to manufacture an error or reopen the one-environment lifecycle.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Independently observe or source a precise current limitation for each exact error-family producer pair, retaining per-series route/job evidence and load/fault conditions without inventing type from a suffix
- [ ] #2 Reconcile the corpus and pair-level reasons without weaker matching, label ignores, capture rewriting or a lower configured producer bound
- [ ] #3 Any future authorized capture environment is fully torn down with verified all-cloud absence; do not induce a provider server fault without an explicit safe method and authority
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
