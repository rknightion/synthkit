---
id: SKT-0020
title: >-
  Multi-cloud capture environments: model synthkit against every major managed
  Kubernetes
status: To Do
assignee: []
created_date: '2026-08-29 09:20'
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
- [ ] #1 A synthkit-terraform repository exists, modelled on rkps-awsinfra, owned by and scoped to synthkit
- [ ] #2 AWS, Azure and GCP capture environments each stand up and tear down from terraform alone, with no hand steps
- [ ] #3 Every environment tears down completely, verified by a post-destroy check rather than assumed
- [ ] #4 The reality corpus carries captures from more than the two substrates it has today
- [ ] #5 Per-substrate divergence is expressible in the corpus, so a shape verified on one cloud is never silently treated as evidence about another
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
