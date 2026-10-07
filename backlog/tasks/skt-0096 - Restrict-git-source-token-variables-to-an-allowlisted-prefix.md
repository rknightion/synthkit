---
id: SKT-0096
title: Restrict git source token variables to an allowlisted prefix
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-07 22:54'
labels:
  - security
  - hardening
dependencies: []
priority: high
ordinal: 218000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
POST /control/blueprints/sources accepts any HTTPS URL and any TokenEnvVar name (internal/bpsource/source_validation.go), and the fetch sends that variable's value as Basic auth (nanogit client via os.Getenv in cmd/synthkit/main.go). A control-plane user can therefore name any process env var (stack tokens, self-obs credential, CONTROL_TOKEN) and send it to a host of their choosing. Today the control-plane token holder is usually also the env owner, so the impact is limited, but any deployment where control access is delegated is exposed. Restrict TokenEnvVar to GIT_TOKEN_* (empty keeps the GIT_TOKEN default) and add an optional GIT_SOURCE_HOST_ALLOWLIST.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 TokenEnvVar outside GIT_TOKEN_* is rejected at validation with a clear message
- [x] #2 Optional host allowlist enforced at validation and at fetch
- [x] #3 Existing sources using GIT_TOKEN keep working; docs updated; .env.example updated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Isolated candidate with frozen ownership; acceptance reproduction, focused checks and just check; independent review before root landing. HA seam design is independently challenged and frozen first.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 1, review-repair rounds 1 (Compose policy override). Land7232114bf410f16dc45f8e58449fcf151345a44c; composed just check exit0 exactSHA. Integrated CI run37696196429 atf3d701a1a4500eb2944a0a354c53a9c641fed371 green; earlier723 run37695831947 cancelled, not pass. Complete final CodeRabbit13/13 and security ACCEPT. One minor custom Go settings in isolated CLI test retained; empty allowlist intentionally allows hosts, DNS rebinding out of policy scope. Generation not applicable; no renderer changes, composed inventory/fidelity checks unchanged.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Forbid non-GIT_TOKEN secret-variable references before lookup; configured host policy applies at validation and real local-TLS fetch, redirects refused. Resolved env-file configuration wired at composition root; empty/default/explicit GIT_TOKEN compatible. Evidence /tmp/skt-0096-D96/acceptance-report.json and /tmp/skt-0096-D96/security-review.log; guarded review and exact integrated gate/CI green.
<!-- SECTION:FINAL_SUMMARY:END -->
