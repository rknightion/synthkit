---
id: SKT-0099
title: Make Synthetic Monitoring provisioning work without a shared volume
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-11 18:19'
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
<!-- SECTION:NOTES:END -->
