---
id: SKT-0165
title: Source system.io.rrqm_s and wrqm_s for the Datadog standalone-host lane
status: To Do
assignee: []
created_date: '2026-10-22T04:24:00Z'
labels:
  - datadog
dependencies: []
priority: medium
ordinal: 308000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Owner ruling 2026-10-22 (loop52 Q04): SKT-0077's acceptance set becomes 78; these two merged-request-rate families have no admissible source (nodeexp models no merged counters). Needs a real standalone-host capture or a sourced merge-rate basis.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both families are emitted with the exact verdict envelope from a named source
- [ ] #2 No value is invented; the source is cited in signals/
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
