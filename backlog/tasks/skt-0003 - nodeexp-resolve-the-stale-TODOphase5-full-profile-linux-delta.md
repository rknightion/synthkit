---
id: SKT-0003
title: 'nodeexp: resolve the stale TODO(phase5) full-profile linux delta'
status: Done
assignee: []
created_date: '2026-08-14 16:09'
updated_date: '2026-08-19 07:39'
labels: []
dependencies: []
references:
  - internal/nodeexp/profiles.go
  - signals/host.md
  - cantfind.md
ordinal: 3000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/nodeexp/profiles.go:597 carries `// TODO(phase5): expand delta from host-capture.md.` — the only TODO left in the codebase. It is stale in two ways and needs a decision, not necessarily code.

1. **Its source file is gone.** `docs/superpowers/host-capture.md` was gitignored scratch and no longer exists in the checkout. Six places still cite it (profiles.go, signals/host.md:29, cantfind.md SK-79, windows_test.go, macos_test.go), so the TODO points at something nobody can open.

2. **The comment directly above it already settles the scope.** profiles.go:538-550 states the modest delta is INTENTIONALLY MODEST and that the hardware/driver-specific families (ZFS ~250 series, ~600 node_ethtool_*, node_mountstats_nfs_*, RAPL, hwmon, NUMA/THP vmstat) are DELIBERATELY OUT OF SCOPE, because they are non-portable across the declared fleet and would invent device topology synthkit has no fixture for. That is a reasoned decision. The TODO reads as an instruction to undo it.

So the real question is whether ProfileFull is finished by design or genuinely incomplete. The likely answer is that the TODO is stale and should be deleted, with the scope decision left standing — but that is the thing to establish, not assume. If ProfileFull IS incomplete, the missing names must come from a real capture, never from invention.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A decision is recorded on whether ProfileFull is complete by design: either the TODO is removed and the deliberate out-of-scope rationale is left as the standing answer, or a concrete list of missing names is identified.
- [x] #2 If names are added, every one is sourced from a real node_exporter capture or vendor docs and recorded in signals/host.md with provenance and date; nothing is invented.
- [x] #3 The dangling docs/superpowers/host-capture.md references are resolved — either the file is re-captured and committed somewhere durable, or the citing comments are corrected to stop pointing at a file that does not exist.
- [x] #4 No TODO/FIXME residue is left behind in internal/nodeexp/.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [x] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave lane (Sonnet subagent, dispatched 2026-08-19): establish whether ProfileFull is complete by design (expected: yes, the profiles.go:538-550 out-of-scope rationale stands), delete the stale TODO(phase5), repoint the dangling docs/superpowers/host-capture.md citations in internal/nodeexp/*.go at a durable source, and leave no TODO/FIXME residue. Lane returns replacement text for signals/host.md:29 and cantfind.md SK-79 for the main thread to apply.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
DECISION (2026-08-19): ProfileFull is COMPLETE BY DESIGN — the TODO was stale, not a real gap.

Evidence: (a) 'git log --diff-filter=D --all -- docs/superpowers/host-capture.md' returns nothing — the file has NO history in this repo at all, so there was never anything durable for the TODO to point at; (b) the comment block immediately above the TODO already carried the reasoned scope decision (universal self-metric surface confirmed and emitted; hardware/driver-specific families — ZFS, node_ethtool_*, NFS mountstats, RAPL, hwmon, NUMA/THP — excluded as non-portable across the declared fleet and requiring device topology synthkit has no fixture for); (c) no portable, fleet-generic node_exporter family was found missing from the delta. No metric name was added — none was sourced, none invented.

Lane changes (internal/nodeexp/, comment-only, no logic touched): the TODO(phase5) line deleted; the surrounding docs reworded from 'INTENTIONALLY MODEST' to a settled permanent decision ('SETTLED, NOT PENDING ... not a placeholder awaiting a future capture'); dangling docs/superpowers/host-capture.md citations repointed at signals/host.md + cantfind.md SK-79..SK-83 in profiles.go, windows.go, macos.go, linux.go, windows_test.go, macos_test.go. No new tests — comment work, per the proportionality rule.

Main-thread changes (files the lane does not own): signals/host.md:29 dead path removed so the file is itself the durable provenance record; cantfind.md SK-79 repointed at signals/host.md; and TWO FURTHER dangling citations the lane found outside its ownership were fixed under AC #3 — internal/construct/host/topology.go:186 and internal/construct/host/host_test.go:166, both now citing signals/host.md [slug: host-docker] for the Docker label schema.

Scoped verification: 'go test ./internal/nodeexp/... ./internal/construct/host/...' → ok, ok. 'grep -rn TODO(phase5) --include=*.go .' → no matches. 'grep -rn host-capture' over *.go/*.md → the only remaining hit is this task's own description quoting the original TODO (backlog markdown, never hand-edited). Full 'make gate' + '-once -dump' deferred to the wiring pass, because another lane (SKT-0001) is mid-edit in internal/sigil + internal/workload/aiagent and the tree does not build while its work is in flight.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
ProfileFull is COMPLETE BY DESIGN; the TODO(phase5) was stale, not a real gap. Landed in 4cdcb4c.

Evidence for the decision: 'git log --diff-filter=D --all -- docs/superpowers/host-capture.md' returns nothing — the file has no history in this repo at all (gitignored scratch), so the TODO pointed at something that was never durable and could not be 'expanded from'. The comment immediately above it already carried the reasoned scope, and no portable, fleet-generic node_exporter family was found missing from the delta. No metric name was added: none was sourced, none invented (AC #1, #2).

Changed: the TODO deleted and the surrounding docs reworded as a settled permanent decision rather than an open question; every dangling docs/superpowers/host-capture.md citation repointed at the durable records (signals/host.md, cantfind.md SK-79..SK-83) across internal/nodeexp/{profiles,windows,macos,linux}.go + windows_test.go + macos_test.go. signals/host.md now carries the capture provenance itself instead of deferring to a dead path, and cantfind.md SK-79 likewise. Two FURTHER dangling citations found outside the task's named six — internal/construct/host/topology.go:186 and host_test.go:166 — were also fixed, since AC #3 is to resolve the dangling references, not six of them (AC #3).

Verification: 'make gate' GREEN, exit 0 — build, vet, test, race (no data races), rw-proto-check, spdx-check (593 .go files), forbidden-words (846 files) (DoD #1). No blueprint field or config struct changed, and the gate's TestSchemaCurrent confirms zero schema drift, so no regen was owed (DoD #2). Two DRY_RUN -once -dump runs produce a BYTE-IDENTICAL series inventory — 2668 metric-name + label-key shapes, diff exit 0 (DoD #3). 'grep -rn TODO|FIXME internal/nodeexp/' returns nothing (AC #4); the only surviving host-capture string anywhere is this task's own description quoting the original TODO, which is backlog markdown and never hand-edited.

Comment and doc work only — no logic changed, so no tests were written, per the proportionality rule.
<!-- SECTION:FINAL_SUMMARY:END -->
