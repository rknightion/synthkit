---
id: SKT-0020
title: >-
  Multi-cloud capture environments: model synthkit against every major managed
  Kubernetes
status: Done
assignee: []
created_date: '2026-08-29 09:20'
updated_date: '2026-09-06 16:01'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 102000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The reality corpus is captured from exactly two substrates: `eks` via `gcx_live_readback` (499 of the 646 coverage gaps) and `k3s` via `k3d_lab` (147). Every fidelity verdict synthkit makes is therefore a verdict about AWS and a local k3d cluster, and nothing else.

That is a real limit on what the comparator can prove. Managed Kubernetes differs between clouds in exactly the places synthkit models: node identity and labels, the CNI's network metrics, the CSI driver's volume metrics, the cloud-controller's Service and LoadBalancer behaviour, the kubelet's cgroup driver, and which control-plane components are even scrapeable. A label shape verified on EKS is not evidence about AKS, and today the corpus has no way to say so per substrate.

Rob is provisioning AKS and GKE lab environments, and later a Rancher one. With the existing k3d lab covering the generic upstream case, that gives five substrates: `eks`, `aks`, `gke`, `rancher`, `k3s`.

The deliverable is a dedicated `synthkit-terraform` repository, modelled on the structure of `rkps-awsinfra` but owned by and scoped to synthkit: reusable, spin-up-and-tear-down infrastructure for capture environments across AWS, Azure and GCP, extensible to further providers. Standing them up by hand each time is what stops captures happening; making them a `terraform apply` is what makes a per-substrate corpus maintainable.

Cost discipline is a first-class requirement, not an afterthought. These are capture environments that exist to be destroyed: every environment must tear down completely, and a forgotten managed control plane in three clouds is the failure mode to design against.

This epic covers standing up the repository and the environments. Capturing from them and reconciling the resulting per-substrate divergence is its own work, tracked as subtasks.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A synthkit-terraform repository exists, modelled on rkps-awsinfra, owned by and scoped to synthkit
- [x] #2 AWS, Azure and GCP capture environments each stand up and tear down from terraform alone, with no hand steps
- [x] #3 Every environment tears down completely, verified by a post-destroy check rather than assumed
- [x] #4 The reality corpus carries captures from more than the two substrates it has today
- [x] #5 Per-substrate divergence is expressible in the corpus, so a shape verified on one cloud is never silently treated as evidence about another
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [x] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-06 per-AC evidence map: AC1 is the existing dedicated private repository with frozen contract (RKSY-0001). AC4 is the seven capture-v2 corpus files promoted by 73a13f4 and 8dc5d9b, with substrates aks, gke, eks, aws, azure, gcp and full. AC5 is SKT-0020.05 Done with per-substrate comparator evidence. AC2 and AC3 remain unchecked: reviewed RKSY-0002/0003/0004/0006 notes do not prove complete per-cloud apply/destroy cycles followed by sweeps. This run does not treat historical Done status or an UNKNOWN orphan check as proof. Parent remains To Do.

2026-09-06 managed-cycle reconciliation: AWS apply phases 10+115, capture exit0, destroy125, scoped sweep standing=0 unknown=0. Azure apply60, capture0, first destroy58 then directory403; guarded recipe deleted the state-bound owned application and proved application=0 service_principal=0, destroy retry0 and scoped sweep standing=0 unknown=0. GCP apply77, capture0, first destroy53 then role/Helm errors; dependency correction and recipe retry destroyed24, scoped sweep standing=0 unknown=0. Before sweeps were standing=0 unknown=0 for all three; children carry verbatim outputs. Repairs and retries used repository recipes, no permission expansion, state abandonment or manual cloud writes. New captures stay protected candidates pending reviewed producer routing; earlier corpus ACs remain historical. just check, safe explicit dump and one agent-excluded local e2e passed this wave; schema generation completed once for integrated native config fields. Rancher child remains separately bounded until its capture and teardown finish.
<!-- SECTION:NOTES:END -->
