---
id: SKT-0204
title: >-
  Give background and causal failures their error fields without empty values on
  healthy lines
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T12:55:00Z'
updated_date: '2026-10-25T14:00:00Z'
labels:
  - causal
dependencies: []
ordinal: 408000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Log workloads write errorType, errorMessage and error_code as empty strings on every line without a cause, and background failures on bound functions carry no span statusMessage or error.type; causal failures do not take a code from the declared error_mix. Omit absent fields (never empty strings, per AGENTS.md), set error.type/status on background failures, and draw causal failure codes from error_mix. Found while validating a private demo blueprint (codex/loop58/pbp3/return.md).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Healthy log lines omit absent error fields
- [ ] #2 Background failures carry span error status and error.type, and causal failures take a code from the declared error_mix
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
