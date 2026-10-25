---
id: SKT-0194
title: Attribute TestEngineExactReadClientDeadlineCause failures under race
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T11:01:00Z'
updated_date: '2026-10-25T12:36:00Z'
labels: []
dependencies: []
priority: high
ordinal: 395000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/agento11yprovision TestEngineExactReadClientDeadlineCause (cleanup-before and receipt subtests) failed in hosted CI just race on a16c25b (run 40184805635) and repeatedly in loop58 lane race tiers (brk, bund, bgerr, tctx, landing-8 first gate) while passing in isolation. Same family as SKT-0181/SKT-0186 (deadline margins under load) but now reaching hosted CI. Attribute it with load reproduction and either fix the test's deadline construction without weakening the proof or isolate it with measured justification (scripts/race-isolated.txt precedent).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Cause attributed with failing and passing evidence
- [ ] #2 Hosted CI race leg passes reliably without weakening the assertion
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
