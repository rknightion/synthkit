---
id: SKT-0111
title: Archive local Go reference sources outside module traversal
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-08 22:35'
labels: []
dependencies: []
type: chore
ordinal: 233000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Integrated gate stops in go vet on ignored reference source under local scratch rather than product packages. Preserve the references byte-exact outside module traversal; do not add their dependencies or weaken checks.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Ignored reference sources causing module traversal are preserved outside the module with verified content hashes
- [ ] #2 Integrated just check passes at an exact named source SHA after relocation
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Identify only ignored Go references outside nested modules; hash and archive preserving relative paths; verify bytes and rerun composed gate under changed local environment.
<!-- SECTION:PLAN:END -->
