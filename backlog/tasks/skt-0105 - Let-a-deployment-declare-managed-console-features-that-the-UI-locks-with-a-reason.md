---
id: SKT-0105
title: >-
  Let a deployment declare managed console features that the UI locks with a
  reason
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-07 21:37'
updated_date: '2026-10-11 08:50'
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
- [x] #1 Feature keys are a small documented enum covering blueprint sources, custom blueprint uploads and reset; unknown keys are rejected at startup
- [x] #2 With the value set, the console renders those controls disabled with the configured reason; with it unset the UI is unchanged
- [x] #3 UI tests cover the injected list and the locked rendering; docs/control-plane.md and docs/configuration.md describe it; .env.example and compose updated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Optional managed-feature enum config with reasons, escaped runtime meta and presentation-only disabled controls. Reject unknown keys, preserve unset index bytes, exercise UI rendering, align env/docs and gate. Isolated append-only changes rebased onto preceding shared-file land before acceptance.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Optional enum JSON feature reasons, startup rejection, escaped runtime metadata, actual Solid DOM disabled controls/reasons and unchanged unset response; presentation only. Original1200cefCIcancelled and composedred600.732s remain nonpasses. Exact integrated descendantdea393d84ee23c9100510c830b0977185c00a20e fullcomposed0 and CI38097611214all10success cover feature. Independent reviewPASS, aggregate25file routineCodeRabbit0findings. No live proof claimed; generator/inventory DoD not applicable.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Deployments can disable blueprint sources, custom uploads and reset in the console with reasons without new API enforcement. Startup, escaped metadata, actual rendered controls and unset bytes proved locally; integrated exact CI/composed green.
<!-- SECTION:FINAL_SUMMARY:END -->
