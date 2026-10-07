---
id: SKT-0096
title: Restrict git source token variables to an allowlisted prefix
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
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
- [ ] #1 TokenEnvVar outside GIT_TOKEN_* is rejected at validation with a clear message
- [ ] #2 Optional host allowlist enforced at validation and at fetch
- [ ] #3 Existing sources using GIT_TOKEN keep working; docs updated; .env.example updated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
