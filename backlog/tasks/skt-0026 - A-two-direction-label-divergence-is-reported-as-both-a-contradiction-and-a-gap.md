---
id: SKT-0026
title: A two-direction label divergence is reported as both a contradiction and a gap
status: To Do
assignee: []
created_date: '2026-08-29 09:23'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 117000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
33 findings appear byte-identical in both the Contradictions and the Coverage gaps sections of the fidelity report. Verified 2026-08-29 by sorting each section's finding lines and intersecting them.

The cause is that a `labels` comparison producing both `only-in-synth` and `only-in-reality` keys is a divergence in two directions, and the report emits the whole finding into both sections rather than splitting it. `aws_applicationelb_info` is the clearest example: `only-in-synth=[tag_VpcId]; only-in-reality=[scrape_job]` appears in full under both headings.

This matters for the gate flip specifically. SKT-0010.05 makes contradictions fail the build and gaps only report. A finding sitting in both sections has an ambiguous verdict — it fails the build, but it is also presented as the forgivable class — and fixing the synth side clears it from Contradictions while leaving an identical line under Coverage gaps that now names a divergence which no longer exists in that direction.

It also makes the two counts non-independent, so any plan reading 67 and 646 as separate quantities is wrong by 33.

Split the finding instead: the `only-in-synth` keys are the contradiction, the `only-in-reality` keys are the gap, and each belongs in exactly one section. Whether the split lines keep the full label sets for context is a presentation choice; what must not survive is one line claiming both verdicts.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A finding appears in exactly one section, so no line carries two verdicts
- [ ] #2 The only-in-synth keys report as the contradiction and the only-in-reality keys as the gap
- [ ] #3 The contradiction and coverage-gap counts are independent, verifiable by intersecting the two sections and finding nothing
- [ ] #4 Fixing one direction of a two-direction finding leaves no stale line naming the fixed direction
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
