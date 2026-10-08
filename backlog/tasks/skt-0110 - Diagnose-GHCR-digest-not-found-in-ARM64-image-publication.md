---
id: SKT-0110
title: Diagnose GHCR digest-not-found in ARM64 image publication
status: Parked
assignee:
  - '@loop-root'
created_date: '2026-10-08 15:25'
updated_date: '2026-10-08 15:26'
labels: []
dependencies: []
type: bug
ordinal: 232000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Exact source ddee479 CI37797343428 is green but publish37797343606 job113380210790 fails Publish scanned digest: a just-built ARM64 digest is not found during ORAS copy. AMD64 and separate capture-image publication succeed; standard image merge/SBOM/verification skipped. Root cause is not established and no vulnerability finding is shown. Preserve exact run/digest evidence and investigate registry availability/tool race without assuming application defect.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Registry/tool cause established for the exact failed digest and run
- [ ] #2 Exact-source multiarchitecture publication and verification complete successfully
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop4: implementation attempts0; exactSHA ddee479 CI37797343428/ci-success success, publish37797343606 ARM64 ORAS digestnotfound failure, AMD64 and capture-image success; standard merge/sign/SBOM/verify skipped. AutoRC37800397636 creates v1.6.0-rc.141 then remains publishing at last read. Provider/tool defect cause unknown; no code edits or hand release. Resume read-only registry/tool cause and then bounded authorized retry; preserve local composed failure owned by SKT-0109 (late Faro POST oracle) separately.
<!-- SECTION:NOTES:END -->
