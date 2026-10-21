---
id: SKT-0153
title: Model pharma regulatory document authoring on current Agent Observability
status: To Do
assignee: []
created_date: '2026-10-20T22:20:00Z'
updated_date: '2026-10-21T01:11:00Z'
labels: []
dependencies: []
references:
  - >-
    backlog/docs/doc-0004 -
    Pharma-regulatory-authoring-and-current-Agent-Observability-contract-design.md
priority: high
type: feature
ordinal: 276000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Ship a fictional pharma regulatory-authoring blueprint (AWS, EKS with k8s-monitoring, Bedrock team profiles, five-agent pipeline) and bring synthkit's Agent Observability lanes to the current SDK and backend contract. Design, decisions and producer table: doc-0004 (read first; do not re-open its decisions).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The pharma-regulatory-authoring blueprint loads under BLUEPRINT_NAMES=* and a live customer-stack push shows conversations, guard steps, online scores, ratings and experiment runs produced by the real backend
- [ ] #2 synthkit emits no backend-derived or backend-only family (doc-0004 producer table), proven by read-back
- [ ] #3 No forbidden-word hit anywhere, including decompressed corpus files
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
