---
id: SKT-0124
title: Investigate missing Sigil aggregate observation on between-stage cancellation
status: To Do
assignee: []
created_date: '2026-10-11 15:20'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 246000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Read-only review of current Sigil delivery finds bare context-error returns between confirmed stages in internal/sink/sigil/sigil.go134-136 and147-149, before aggregate observation is emitted. Neither the original nor corrected three-stage deadline test exercises that window. This is source-visible potential missing operational accounting, not the established cause of the historical zero-confirmation test failure or a demonstrated live incident. The current proof-only correction intentionally leaves production behavior untouched.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bounded fake HTTP and gate reproduction establishes whether cancellation after a confirmed stage emits exactly one truthful aggregate event with confirmed count and terminal error.
- [ ] #2 Any separately authorized production repair preserves one absolute whole-write deadline, no later-stage admission after cancellation, positively joined work and unchanged emission behavior.
- [ ] #3 Source and test evidence classify observed behavior without presenting the historical scheduling failure as a proven runtime cause.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
