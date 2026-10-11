---
id: SKT-0099
title: Make Synthetic Monitoring provisioning work without a shared volume
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-11 19:16'
labels:
  - feature
  - ha
  - sm
dependencies:
  - SKT-0094
ordinal: 221000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SM provisioning is a separate two-boot handoff: cmd/sm-provision takes an O_EXCL file lock and writes a registration the emitter reads at boot, and the registration binding includes the release version (internal/smstate/state.go), so every image bump silently disables SM emission until the provisioner re-runs and the emitter restarts. Under stateless HA: run sm-provision against the kubernetes state backend with a Lease-based lock, and stop a release-version bump with unchanged SM specs from invalidating the registration.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Diskless one-shot: real CLI with injected local named Kubernetes and SM HTTP proves dedicated provisioning Lease only, named GET/PUT only, zero writes or SM calls before lock acquisition, serialized jobs, durable pending before mutation, fail-closed ambiguity/lease loss and planned join-before-release; file mode and privacy proofs remain green.
- [ ] #2 Release-only bump: already complete v2 registration stays active with identical specs/target/schema at a newer emitter release without provisioning; actual runner tick emits authoritative SM metrics/logs with unchanged config_version and inventory across file HA-off, Kubernetes HA-off and Lease acquisition. Older tool rejects newer snapshot before remote work; standby writes nothing.
- [ ] #3 Same-key changed specs: each bound spec/target/schema change suppresses actual SM emission while unrelated constructs emit; missing, partial, corrupt or pending evidence never activates. Explicit matching-version apply and restart/acquisition restores authoritative emission without hot activation or foreign adoption. Concurrent snapshot CAS preserves evidence; explicit v1-to-v2 reprovision/restart preserves legacy ownership, previews, journal and lock.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement approved dedicated provisioner Lease, aggregate CAS sm-state key, v2 release-independent activation with strict tool/snapshot version compatibility and explicit format migration. Refresh consumer census against current main before changing. Prove local CLI with fake named Kubernetes and SM HTTP plus actual emission/restart behavior; preserve private file evidence, compare inventory before/after, regenerate config/skills outputs, full local gate and pre-land guarded reviews. Chart wiring is a separate unadmitted follow-up.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implementation preflight found no decision-changing engine drift. Chart named-state HA wiring has landed, while SM Job remains file-only; follow-up will extend that existing seam. Zero implementation attempts: required CLI HTTP proof needs an explicit backend client injection seam. Root grants backend_kubernetes.go solely for additive explicit injection using the existing validated gated backend, matching the Lease adapter precedent. Production credential discovery and accepted backend/gate/CAS contracts stay unchanged.

Candidate partial after two change-and-verify cycles (implementation/full gate, then owned architecture repair/focused race); initial ownership inspection consumed zero. Baseb8a0ef1da85ed9b0942be2398d35083ce90561f5, treeb6c51fac54a9b98f98dea061a5bd03d8a650ca59, complete39file patchd9b91adf56df730fffd8c9f1c3b904f75ede725287e5e94651b7be92238d4e91 verified and retained by root. Dedicated Lease/CAS/v2 activation/file evidence implemented with actual fake HTTP CLI and runner release-bump/restart positives, inventory byte-identical, focused race green. Full gate ran before final architecture correction and was red; unchanged chart test combines Helm mutually exclusive flags and fails before fixture API calls on available4.3.0/3.20.0. No assertion changed, no final fullgate pass claimed. Full composed-runner negative matrix still misses removal, target/schema mutation and all incomplete/duplicate/bad-Modified evidence cases. Root dispatched compatible-tool prerequisite mapping and independent full-candidate security review before next rescue. Initial CodeRabbit39files had2contract-conflicting majors under review; post-architecture checkpoint review emitted all39files0findings, a disclosed second-full-review cadence defect. Nothing accepted or landed; no CI.

Full independent security review BLOCK on treeb6c51fac: major torn file activation read in internal/smstore/file.go:33,45 can combine stale nil Pending with newly complete registration while durable pending remains; source-confirmed interleaving, no runtime reproduction yet. Both initial CodeRabbit majors rejected against approved mapping/lock requirements. Compatible official Helm3.19.0 focused unchanged chart proof passed all4cases with actual fake API reads/precreation and byte-identical state. Third counted change-and-verify cycle routed to retry worker for fail-first race repair and complete required composed-runner negative matrix, then exact finalfullgate and guarded delta reviews. Ceiling remains4; no reset. Local retained checkpoint dd4102104ae75654ada28c5d29882fbf8b5885ad identifies exact last-reviewed tree for limited delta review and is not a land.

Retry preflight stopped before edits because narrowed repair brief omitted cmd/sm-provision/lease.go file-mode lock composition. Root inspected and added that branch only for positively acquired store/release composition, preserving all Kubernetes Lease behavior and frozen APIs. Candidate remains exact prior tree, real index empty, no gate or CodeRabbit run; zero additional cycles consumed. Third counted cycle continues on same retry route.
<!-- SECTION:NOTES:END -->
