---
id: SKT-0133
title: 'Align onboarding, contributor workflows and documentation with current runtime'
status: In Progress
assignee:
  - '@codex'
created_date: '2026-10-14 23:52'
updated_date: '2026-10-15 00:13'
labels: []
dependencies: []
ordinal: 256000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The documentation audit found contradictory transport and SM claims, fragmented first-run routes, incomplete contributor setup and insufficient documentation validation. Owner authorized the complete refresh and autonomous choices.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Entry pages share a safe canonical offline-to-live onboarding route with complete prerequisites
- [x] #2 Architecture, operational docs, skills and contributor guidance agree with current runtime and canonical gates
- [x] #3 Focused accessible diagrams explain onboarding, telemetry, architecture, SM activation and contribution checks
- [x] #4 Documentation validation covers maintained root, chart and skill documents with negative controls and final review evidence
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Independent ownership: onboarding entry pages; architecture and operational references; human and agent contribution workflow; documentation validator; five accessible diagrams. Root integrates navigation, skills routing and remaining drift, validates examples and final checks, reviews code and exact-SHA CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented canonical onboarding, contribution guide and skill, portable agent routing, five accessible HTML/SVG diagrams and current transport/SM/GPU/content handling facts. Clean-source UI and binary build, selected offline dump and real local HTTP asset serving passed. Official documentation hub strict build and post-build guards passed for 66 pages after fixing repository-relative links escaping the published docs tree. Validator has 16 passing tests, including fail-first published-tree regression. Full local gate and CodeRabbit review remain running; no live deployment or backend verification claimed.

CodeRabbit first review completed across 37 changed files with one minor spelling allegation. Rejected after checking both blueprint inventory commands already spell SELFOBS_ENABLED correctly; no SELOBS occurrence exists. A subsequent 3-line published-site-boundary validator change and its 14-line regression test are being reviewed separately against the retained reviewed snapshot. Independent review requested historical OTLP-baseline and provenance-claim corrections; both fixed. Final strict hub build passes 66 pages; diagram mobile readability corrected with local scroll wrappers and explicit intrinsic SVG dimensions.

Final delta CodeRabbit review completed with zero findings (both validator files). Independent corrections review passed; final rendered mobile QA confirms 640px diagrams scrolling inside 343px wrappers without page overflow. just gen passed with no schema drift. Selected otlp-native just dump stdout is byte-identical to the clean-source baseline; all 1172 metric names resolve against 179 signal blocks with no parse gaps. Name conformance is not label-envelope or live-delivery proof.

Full local just check passed, including ordinary Go, race (dashboard package 496.093s), chart/schema validation, signal fidelity and all 10 executable troubleshooting rows. Final 16-test documentation suite passed after the published-link guard addition. All implementation acceptance is satisfied; awaiting exact committed-SHA CI before final tracker closeout.
<!-- SECTION:NOTES:END -->
