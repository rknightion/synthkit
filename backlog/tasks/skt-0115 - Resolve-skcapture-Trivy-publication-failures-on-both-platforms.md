---
id: SKT-0115
title: Resolve skcapture Trivy publication failures on both platforms
status: Done
assignee:
  - '@loop8'
created_date: '2026-10-09 14:36'
updated_date: '2026-10-09 17:23'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 237000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
At board-only main edf7243dc6742373da1c3feb8b3e72407e5c5df2, publish run37943032397 built both skcapture images then failed Enforce Trivy security gate. The gate reported scanner error or unaccepted HIGH/CRITICAL findings but the inspected reduction did not distinguish them. This is independent of the HA lifecycle CI failure and no application code changed in that push.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Exact failing run scanner/SARIF evidence distinguishes scanner errors from specific unaccepted findings for amd64 and arm64.
- [x] #2 Safe repair yields both skcapture publication legs green at an exact source SHA without ignored HIGH/CRITICAL findings, lowered severity or weakened security gate.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop8: build both local architectures, prove exact failing Trivy findings/scanner errors; bump safe fixed base/modules without gate changes; root reviewed land and exact-SHA CI both skcapture legs. Docker mutex released by hosted repair.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop8 initial read-only pass used staleOct8DB and not accepted as reproduction. Resumed original lane with pinnedCITrivy0.70/currentOct9DB: botharch kubectlstdlib1.26.6 HIGH78667/97031 fail1. One implementation cycle Dockerfile.skcapture only checksum-pinned officialkubectlsource rebuilt with existingGo1.27.2 unchangedvendor; identicalfreshscans0 botharch and networkdisabledCLIsmoke0, localcandidatejustcheck0, completeCodeRabbit0/securityPASS. Root integration holds until in-flight cf8 composedgate completes; exactSHA publicationCI bothlegs pending.

loop8 attempts1 implementation; staleDB pass explicitly excluded. FreshpinnedTrivy0.70 Oct9DB botharch kubectlstdlibHIGH78667/97031 fail1; checksum-pinned officialsourcebuilt withGo1.27.2 unchangedvendor scans0, realCLI smoke0. CompleteCodeRabbit0/securityPASS. Rootland2f38dbbba291b794213639a2428b69d3023e58cd; composedgate0; publication37962316081 bothskcapturebuild/scanner/publisheddigest/merge+sign+sbom success, exactCI37962316276 allrequiredjobs success. Sharedfail-onlyEnforcestep intentionallyskippedafter actualscanner success, not countedaspass; no reusable/securitygate changes.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Repaired botharchitecture skcapture publication by rebuilding checksum-pinned official kubectl source with patched compiler; no dependency/vendor/gate weakening. Freshfail-before/pass-after scans, realimageCLI smoke, guarded reviews, composed0 and exactSHA bothpublicationlegs green.
<!-- SECTION:FINAL_SUMMARY:END -->
