---
id: SKT-0181
title: Classify two load-sensitive gate failures in provisioning tests
status: To Do
assignee: []
created_date: '2026-10-24T19:11:00Z'
updated_date: '2026-10-24T19:21:00Z'
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
<!-- SECTION:NOTES:END -->
