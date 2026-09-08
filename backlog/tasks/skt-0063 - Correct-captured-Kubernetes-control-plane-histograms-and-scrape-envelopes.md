---
id: SKT-0063
title: Correct captured Kubernetes control-plane histograms and scrape envelopes
status: Done
assignee: []
created_date: '2026-09-08 00:39'
updated_date: '2026-09-08 01:27'
labels: []
dependencies: []
ordinal: 159000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Preserved independent captures show generic histogram defaults and universal scrape labels disagree with observed control-plane and DNS producers. Correct emitter shapes to the observed contracts and retain old capture evidence.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Explicit-selection inventory matches observed histogram bounds and scrape label shapes
- [x] #2 Existing construct checks and repository gate pass with reviewed source
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Committed a98aba0dcdf0464f2c85ae1ce7b976b3cdffdc6c. Direct capture corrections remove unsupported CoreDNS service/source keys and fix proxy/API-server/workqueue histogram bounds. Explicit-selection corrected inventory yields zero unexempted contradictions for both Rancher and control-plane candidates. RKE2 10-bucket workqueue profile is selected; managed five additional bounds remain retained coverage evidence. CodeRabbit minor test-coverage finding fixed; major profile objection contradicted by the retained capture. Final isolated just check exit 0.
<!-- SECTION:FINAL_SUMMARY:END -->
