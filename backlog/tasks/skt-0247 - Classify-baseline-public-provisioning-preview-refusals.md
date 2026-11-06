---
id: SKT-0247
title: Classify baseline public provisioning preview refusals
status: In Progress
assignee: []
created_date: '2026-11-06T20:31:00Z'
updated_date: '2026-11-06T20:31:00Z'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 453000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
On unchanged revision 5f2ee78dc02f685acda4dbb924c5f8f37043203b, TestPublicOriginalModeLostResponses returns preview exit 2 instead of 0 under a group-writable fixture setup. Preserve private-parent admission, existing assertions and command deadlines while classifying the environment cause.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The unchanged-revision reproduction is attributed to a specific environment, fixture or implementation cause with retained evidence.
- [ ] #2 The affected public command tests and required full gate pass on the corrected supported setup without weakening admission checks, assertions or time bounds.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Retain unchanged-source refusal reproduction, isolate process permissions with the actual private-parent admission boundary, document the supported environment, and run the exact full gate on the final reviewer migration candidate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Unchanged public lost-response proofs fail all three subcases under umask 0002 and pass changing only child umask to 0022. A scratch Go overlay invokes the actual unchanged privateParent on actual t.TempDir: mode 0775 is refused under 0002; mode 0755 is accepted under 0022. Go testing.makeTempDir creates children with mode 0777 masked by the process. No application code, test assertion, privacy check or deadline changed. Full gate is pending on a canonical private 0700 temporary root with process umask 0022.
<!-- SECTION:NOTES:END -->
