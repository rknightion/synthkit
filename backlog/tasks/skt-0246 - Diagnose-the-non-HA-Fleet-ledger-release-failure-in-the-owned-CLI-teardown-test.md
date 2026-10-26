---
id: SKT-0246
title: >-
  Diagnose the non-HA Fleet ledger release failure in the owned CLI teardown
  test
status: To Do
assignee: []
created_date: '2026-10-26T18:46:00Z'
labels:
  - flaky
dependencies: []
priority: low
ordinal: 452000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestFleetUnregisterOwnedCLIRetiresHistoricalLedger once reported ledger_release_failed (loop58 gate log) in a non-HA path; unreproduced in loop61. Suspected cause, unproven: the 2 s ledger-write budget hit by a filesystem stall. Reproduce with injected ledger-write latency first; consider surfacing the persist error in the receipt entry.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
