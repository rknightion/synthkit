---
id: SKT-0111
title: Archive local Go reference sources outside module traversal
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-08 22:35'
updated_date: '2026-10-08 22:48'
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
- [x] #1 Ignored reference sources causing module traversal are preserved outside the module with verified content hashes
- [x] #2 Integrated just check passes at an exact named source SHA after relocation
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Identify only ignored Go references outside nested modules; hash and archive preserving relative paths; verify bytes and rerun composed gate under changed local environment.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop6 attempts1: three ignored loose reference files archived byte-exact outside module, preserving relative paths and hashes; composed just check0 at14719af3aa2714e4823b4c6229e68a6f377aa4f2. No product/test change and no CodeRabbit needed for local reference relocation. Eight external troubleshooting prerequisites remain blocked, not passes.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Preserved module-polluting reference Go sources outside traversal; verified hashes and integrated gate at14719af3aa2714e4823b4c6229e68a6f377aa4f2.
<!-- SECTION:FINAL_SUMMARY:END -->
