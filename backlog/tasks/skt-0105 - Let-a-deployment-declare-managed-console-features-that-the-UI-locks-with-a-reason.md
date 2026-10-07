---
id: SKT-0105
title: >-
  Let a deployment declare managed console features that the UI locks with a
  reason
status: To Do
assignee: []
created_date: '2026-10-07 21:37'
labels:
  - feature
  - ui
dependencies:
  - SKT-0098
priority: medium
ordinal: 227000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
When the console runs behind a reverse proxy or a managed wrapper, some control routes may be owned by the wrapper and blocked at the proxy (for example blueprint sources, custom uploads, reset). Today the console still shows those controls and the operator only learns on click, from a proxy error. Add a generic, optional way for a deployment to declare managed features: a config value (for example CONTROL_MANAGED_FEATURES, a comma-separated list of feature keys with an optional reason) that the server injects into index.html next to the runtime base path from SKT-0098, and that the console reads to render the matching controls as locked, with the reason shown. The server keeps enforcing nothing new: this is presentation only, and the proxy or wrapper stays the enforcement point. Unset keeps today's behaviour exactly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Feature keys are a small documented enum covering blueprint sources, custom blueprint uploads and reset; unknown keys are rejected at startup
- [ ] #2 With the value set, the console renders those controls disabled with the configured reason; with it unset the UI is unchanged
- [ ] #3 UI tests cover the injected list and the locked rendering; docs/control-plane.md and docs/configuration.md describe it; .env.example and compose updated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
