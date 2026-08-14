---
id: SKT-0003
title: 'nodeexp: resolve the stale TODO(phase5) full-profile linux delta'
status: To Do
assignee: []
created_date: '2026-08-14 16:09'
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
- [ ] #1 A decision is recorded on whether ProfileFull is complete by design: either the TODO is removed and the deliberate out-of-scope rationale is left as the standing answer, or a concrete list of missing names is identified.
- [ ] #2 If names are added, every one is sourced from a real node_exporter capture or vendor docs and recorded in signals/host.md with provenance and date; nothing is invented.
- [ ] #3 The dangling docs/superpowers/host-capture.md references are resolved — either the file is re-captured and committed somewhere durable, or the citing comments are corrected to stop pointing at a file that does not exist.
- [ ] #4 No TODO/FIXME residue is left behind in internal/nodeexp/.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
