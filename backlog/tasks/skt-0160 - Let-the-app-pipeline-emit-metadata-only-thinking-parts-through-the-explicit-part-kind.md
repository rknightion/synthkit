---
id: SKT-0160
title: >-
  Let the app pipeline emit metadata-only thinking parts through the explicit
  part kind
status: To Do
assignee: []
created_date: '2026-10-22T03:08:00Z'
updated_date: '2026-10-22T03:15:00Z'
labels:
  - sigil
dependencies: []
priority: medium
ordinal: 303000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0157 (loop52) added sigil.PartKindThinking so the native encoder emits Part_Thinking with empty content. The app pipeline still rejects selected thinking at Build (internal/workload/app/agent_pipeline.go:472) and carried thinking at capture (agent_pipeline_records.go:81/100) under metadata_only capture. Set Kind on the stripped part and lift the rejection, proven through the actual encoder; signals/sigil.md updated to match.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Metadata-only capture of a thinking part encodes as Part_Thinking with empty content through the real app pipeline and sink
- [ ] #2 Non-metadata-only payloads stay byte-identical; no placeholder content
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
From SKT-0157 review (loop52 rv157): also reject a part with Kind=thinking and nonempty Text (mapping.go:313 silently drops text) and unknown Kind values; refresh the stale comment at internal/workload/app/agent_pipeline.go:469-471.
<!-- SECTION:NOTES:END -->
