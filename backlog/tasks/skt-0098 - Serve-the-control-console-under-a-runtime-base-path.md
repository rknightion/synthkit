---
id: SKT-0098
title: Serve the control console under a runtime base path
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
labels:
  - feature
  - ui
dependencies: []
ordinal: 220000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Running the console behind a reverse proxy under a prefix is impossible today: Vite bakes base /control/ui/ (internal/control/ui/vite.config.ts), the router base is compile-time BASE_URL (src/App.tsx), every API call is an absolute /control/ path (src/api/client.ts) and the server redirect /control/ui -> /control/ui/ is absolute (internal/control/http.go). One image must serve any prefix: build with a relative base, have the server inject <base href> and an API prefix into index.html from CONTROL_BASE_PATH (or X-Forwarded-Prefix from a trusted proxy), and make redirects prefix-aware. Default behaviour unchanged.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Console works end to end under an arbitrary prefix with one build
- [ ] #2 Default (no prefix) unchanged; Infinity GET shapes unchanged
- [ ] #3 UI tests cover prefixed API URL construction
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
