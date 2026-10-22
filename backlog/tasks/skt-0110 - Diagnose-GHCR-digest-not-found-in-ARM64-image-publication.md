---
id: SKT-0110
title: Diagnose GHCR digest-not-found in ARM64 image publication
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-08 15:25'
updated_date: '2026-10-22T03:05:00Z'
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
- [x] #2 Exact-source multiarchitecture publication and verification complete successfully
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop4: implementation attempts0; exactSHA ddee479 CI37797343428/ci-success success, publish37797343606 ARM64 ORAS digestnotfound failure, AMD64 and capture-image success; standard merge/sign/SBOM/verify skipped. AutoRC37800397636 creates v1.6.0-rc.141 then remains publishing at last read. Provider/tool defect cause unknown; no code edits or hand release. Resume read-only registry/tool cause and then bounded authorized retry; preserve local composed failure owned by SKT-0109 (late Faro POST oracle) separately.

loop5: attempts0 (read-only). AC1 not established: ORAS1.3.3 copy to GHCR failed digest606d154a after build/Trivy success, no HTTP trace/registry diagnostics. AC2 met: run37800937081 at31b616ff1df731d07e5d6a5ceb6abdbbc6cb1f92 ARM64 job113392721197 and merge/sign/SBOM job113394431239 succeed; release37804306097 also succeeds. 1445 non-backlog tracked entries equal ddee479, inventory sha256dbf69277f9bbbf0e547d5f515b73cc2af516846914dc12cca514a6a50faea22f. Successful different digests do not establish failed digest recovery. Resume cause diagnosis if request/registry diagnostics become available; no code fix justified.

loop52 (scout s110, read-only): exact cause not recoverable. Job 113380210790 ran container-publish.yml@c36bf7d; oras 1.3.3 cp copied config and 15 layers, then 'Copying 606d154a3978 manifest' -> 'Error response from registry: sha256:606d154a...: not found' 0.43s later; no --debug/HTTP trace so PUT vs HEAD is unknown. Best hypothesis (unproven): transient GHCR read-after-write lag. Current pin 2c2a377 still runs a bare oras cp without retry; add one in rknightion/.github only if it recurs. 2026-10-21 run 39695726690 failure is a different class (BuildKit image pull unauthorized before build). Closed as unrecoverable per loop52 charter; AC1 left unchecked deliberately. Evidence: codex/loop52/s110/return.md (machine-local).
<!-- SECTION:NOTES:END -->
