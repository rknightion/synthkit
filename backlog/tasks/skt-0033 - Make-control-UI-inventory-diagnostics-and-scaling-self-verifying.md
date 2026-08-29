---
id: SKT-0033
title: 'Make control UI, inventory, diagnostics, and scaling self-verifying'
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: medium
type: feature
ordinal: 124000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios C5, D5, D6, and D8 could reach the control surfaces, but the UI path yielded only a redirect in the clean-container check, inventory exposed no per-blueprint entries, diagnostics did not establish shell-free sufficiency, and the full catalogue exposed zero scalable targets. The round could not verify every UI view, identity separation, or a real scaling mutation from the supplied control data.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The admin UI has an executable check that enumerates every shipped view and confirms it reflects live state
- [ ] #2 Control inventory exposes per-blueprint and substrate identity needed for label-separation diagnosis
- [ ] #3 Diagnostics identify a failing blueprint and lane without container shell access
- [ ] #4 At least one shipped acceptance configuration exposes a valid scalable target, or the API clearly reports why none exist
- [ ] #5 Reset and scaling acceptance checks assert an observable emitted-data change
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
