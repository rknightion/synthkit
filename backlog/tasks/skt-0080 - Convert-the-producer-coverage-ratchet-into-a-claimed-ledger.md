---
id: SKT-0080
title: Convert the producer-coverage ratchet into a claimed ledger
status: Done
assignee:
  - '@codex'
created_date: '2026-09-11 19:04'
updated_date: '2026-09-11 20:23'
labels: []
dependencies: []
ordinal: 176000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A zero-headroom cap blocks legitimate new emission unless every new missing producer pair is explicitly justified.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Each expected_count raise follows a recorded measurement and adds exactly one specific reason per new signal and producer pair, with no blanket claim.
- [x] #2 At final validation observed equals expected_count equals the claim count, with zero stale and untriaged claims.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Execute the frozen campaign lane contract; root integrates and measures before any claim adjustment, then validates the exact tree and reconciles acceptance.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Settled policy: expected_count may rise only at a root checkpoint after recording the exact observed count. Every new signal/producer pair needs its own specific reason for lacking comparable reality evidence; blanket reasons are forbidden. No comparator, exemption, capture or permutation relaxation is authorized. Baseline at 65e93ecdb9d59f6393e3f9c1ba1ae3c243c77c7d: observed=189 expected_count=189 claims=189 stale=0 untriaged=0. Measurement uses full CompareCorpus findings, not grouped report rows.

Root integration: storage 296d83a91c267c9004ffa79e4cd41825a0b0d4e4 CI 34639635537 success; swap c6e9f9ff9926f7171b76952395dd5aacb2909f71 CI 34641719112 success; profile c500ec61618b13d83218d9b1a4292225e46aeac1 CI34642964976 success. Ordered gen-check, SPDX 736 and full just check passed. just gen regenerated the profile field and earlier storage doc index. Dump comparison proves 36 added host pairs; sample-time Kubernetes termination and topology-conflict changes are separately identified. No cloud, capture, cluster, lab run, second nightly dispatch, release retry or PR mutation occurred.
Settled policy: expected_count may rise only at a root checkpoint after recording measurement, by exactly the observed count, with one specific reason per new (signal,producer) pair. No headroom, blanket claim, comparator relaxation, capture rewrite or new exemption.
Ledger(observed,bound,claims,stale,untriaged):baseline(189,189,189,0,0);storage beforeclaims(218,189,189,0,29);storage claimed(218,218,218,0,0);swap beforeclaims(225,218,218,0,7);swap claimed and final(225,225,225,0,0). All36 new claims name their family and datadog-receiver-host producer, and the separately attributed Kubernetes datadog-receiver observation that is not comparable. Final unexempted contradictions0. One further pair would be226>225 and fail until independently measured and specifically claimed. No comparator code changed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Claimed ledger reconciled225=225=225,stale0,untriaged0. Both raises followed recorded measurements and per-pair claims. Done2/2; zero spare headroom.
<!-- SECTION:FINAL_SUMMARY:END -->
