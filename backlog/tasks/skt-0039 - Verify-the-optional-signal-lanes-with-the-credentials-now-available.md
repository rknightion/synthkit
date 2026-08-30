---
id: SKT-0039
title: Verify the optional signal lanes with the credentials now available
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-29 23:15'
updated_date: '2026-08-30 01:39'
labels: []
dependencies: []
references:
  - e2e/acceptance/SCENARIOS.md
  - codex/goal-2026-08-30-acceptance-closeout.md
priority: high
type: feature
ordinal: 130000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Re-run acceptance scenarios B4 through B10 against the designated acceptance stack now that the complete optional credential surface is available. This task records live product behavior that the original acceptance round could not exercise; the earlier task remains closed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 B4: synthetic profiles are visible for each selected language and profile type
- [x] #2 B5: Faro sessions and page views arrive, and the unconfigured-lane diagnostic remains discoverable
- [ ] #3 B6: Synthetic Monitoring checks are registered, reporting, and the provisioner behavior is observed
- [x] #4 B7: Fleet Management collectors are registered and show fresh heartbeats
- [ ] #5 B8: sigil generations, workflow steps, and scores arrive
- [x] #6 B9: process self-observability reaches the separate self-observability stack and does not enter the synthetic-data stack
- [ ] #7 B10: all optional lanes run together without sustained delivery loss or starvation
- [x] #8 Every scenario is recorded as pass or fail with its observable assertion; unsupported behavior is a failure with a reason, not a skip
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Root runs B4 through B10 after integration against the designated synthetic and separate self-observability destinations, recording observable pass or fail evidence for every row.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: B4 profiles pass, B5 Faro pass, B6 SM fail on missing check/probe write authority, B7 Fleet pass, B8 sigil partial/fail because scores return HTTP 400, B9 separate self-observability pass, B10 fail because the sigil queue has sustained loss. Every row has an observable verdict in the rerun register.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked at B6, B8, and B10. Resume with an SM token carrying check/probe write authority and a product-accepted sigil score body; then require all optional queues to show no current loss over a full delivery window.
<!-- SECTION:FINAL_SUMMARY:END -->
