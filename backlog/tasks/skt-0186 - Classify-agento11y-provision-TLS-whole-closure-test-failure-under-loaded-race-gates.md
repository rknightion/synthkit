---
id: SKT-0186
title: >-
  Classify agento11y-provision TLS whole-closure test failure under loaded race
  gates
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T06:33:00Z'
updated_date: '2026-10-25T10:03:00Z'
labels: []
dependencies: []
priority: medium
ordinal: 380000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestPublicCommandTLSWholeClosure in cmd/agento11y-provision failed once in a full gaterun just check on gate-1 (command exit=-1, TLS handshake 'bad certificate') and passed -race -count=3 in isolation on the same tree. Two agento11yprovision engine TLS tests also hit deadlines under the race tier in the same session. Find the attributable cause (subprocess deadline under load vs certificate timing) without weakening the proof.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The failure is attributed with evidence and either fixed or bounded without relaxing assertions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop58: landed (landings 5-7), gates gate-2 pass, hosted CI green 0cb0310.
<!-- SECTION:NOTES:END -->
