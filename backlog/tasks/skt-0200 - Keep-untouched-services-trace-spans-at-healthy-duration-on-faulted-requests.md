---
id: SKT-0200
title: Keep untouched services' trace spans at healthy duration on faulted requests
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T11:57:00Z'
updated_date: '2026-10-25T14:00:00Z'
labels:
  - causal
dependencies: []
ordinal: 404000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
In the acme-ai-platform causal stories, on faulted requests the trace spans of services the fault never touches (auth, collab-editor-cs) stretch x1.22 to x1.33, while their span-metric rows correctly stay x1.000 (rowg). Traces and metrics then disagree about which service slowed. Evidence: /Users/rob/repos/synthkit/codex/loop58/fmig/return.md. Check whether SKT-0168.09.36 or SKT-0168.09.37 already address part of it before changing span positioning.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 On a faulted request, spans of services the fault does not reach keep their healthy duration distribution
- [ ] #2 Span-metric rows and trace spans agree on which services slowed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
