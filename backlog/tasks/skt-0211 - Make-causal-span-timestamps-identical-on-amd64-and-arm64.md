---
id: SKT-0211
title: Make causal span timestamps identical on amd64 and arm64
status: To Do
assignee: []
created_date: '2026-10-25T15:15:00Z'
labels:
  - causal
dependencies: []
ordinal: 415000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Landing-13 review C1 (codex/loop58/rv13 notes in DECISIONS D190): the Go compiler fuses multiply-add expressions in internal/workload/app/causal_mint.go (about :621, :647, :680, :694, :716, :718) on arm64 but not amd64, so acme-ai-platform's shipped digest differs by 1 ns between architectures and CI (x86_64) fails while arm64 gate hosts pass. Wrap products in explicit float64() conversions, regenerate the acme digest and pins, and prove the digest test under GOARCH=amd64 in the landing proof.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 TestShippedBlueprintsDefaultUnchanged passes for acme-ai-platform on arm64 and under GOARCH=amd64
- [ ] #2 The landing proof for any causal arithmetic change includes a GOARCH=amd64 digest run
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
