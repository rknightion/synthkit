---
id: SKT-0038
title: Make marketplace installation instructions executable from a clean host
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
updated_date: '2026-08-29 19:22'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: medium
type: docs
ordinal: 129000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenario H4 found that the documented marketplace path consists of slash commands but does not state the required Codex or Claude plugin host. In the clean container there was no plugin host or codex executable, so the commands were not executable and relocation could not be verified.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Marketplace instructions state the supported host, installation prerequisite, and where slash commands must be entered
- [ ] #2 A clean supported host can add the marketplace, install the plugin, and invoke each bundled skill
- [ ] #3 The installed plugin remains functional after relocation
- [ ] #4 Shell examples are separated from host UI or slash-command instructions so they cannot be mistaken for executable shell commands
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check </dev/null (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen </dev/null (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump </dev/null — inventory diffed against signals/
<!-- DOD:END -->
