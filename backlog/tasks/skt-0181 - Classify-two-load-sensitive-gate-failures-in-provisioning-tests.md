---
id: SKT-0181
title: Classify two load-sensitive gate failures in provisioning tests
status: Done
assignee: []
created_date: '2026-10-24T19:11:00Z'
updated_date: '2026-10-25T00:26:00Z'
labels: []
dependencies: []
priority: medium
ordinal: 355000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Full just check on gate-1 (4 busy slots) failed TestKubernetesProvisionLeaseSerializesAndLossClosesAdmission/loss in cmd/sm-provision ('no real mutation receipt before loss') and TestEngineSuiteContentAndCompletenessNotJustCountTLS/case-list-partial in internal/agento11yprovision ('ended by a deadline or cancellation, not the behaviour under test'). Both pass -count=3 in isolation on base and candidate. Diagnose without weakening bounds.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop57 land6 gate-1 slot 9: also TestHAExpiredGlobalDrainDoesNotRelease (cmd/synthkit, 'acquisition retained stale topology: 0') and agento11yprovision TestEngineSourceNormalizationAdmissionTLS/TestEngineOwnedEvaluatorInventoryCannotHideOtherVersionTLS/TestEngineIndependentNoncommitOnlyNeverAttemptedSuffixTLS (deadline overrun, custody durability outcome unknown); all pass isolated on gate-2 on base and candidate.

loop57 land9 gate-1: TestFleetUnregisterOwnedHAAuthorityInterruptionAndRecovery (fleet_owned_test.go:553 leader never reached deletion) failed once; -race -count=10 passes on base and candidate on gate-2.

Owner 2026-10-25 (loop57 Q04): fix gate capacity by capping concurrent heavy slots per gate host in m7kni/gate-runner (option D); do not raise the agento11yprovision budgets. gate-runner has no board and ~/.agents is fenced for this loop, so the gate-runner task is for the owner to file.

loop57: landed on main (see codex/loop57/BATCH*.md).
<!-- SECTION:NOTES:END -->
