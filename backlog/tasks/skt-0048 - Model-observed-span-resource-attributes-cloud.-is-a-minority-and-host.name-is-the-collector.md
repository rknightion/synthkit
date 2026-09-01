---
id: SKT-0048
title: >-
  Model observed span resource attributes: cloud.* is a minority, and host.name
  is the collector
status: Done
assignee:
  - '@codex'
created_date: '2026-08-31 12:40'
updated_date: '2026-09-01 20:42'
labels: []
dependencies: []
ordinal: 139000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Two measurements from the multi-cloud capture lab on 2026-08-31 (synthkit-terraform RKSY-0025 and
RKSY-0029, which carry the full evidence). Both say the same thing: a real span's RESOURCE is not
what an idealised model would produce, and synthkit currently models the ideal.

All three clusters ran the SAME k8s-monitoring 4.5.0 configuration and the same otel-demo. The
alloy-receiver ConfigMap is byte-identical across them apart from the cluster name.

## 1. cloud.* on spans is a MINORITY property, and absent entirely on EKS

    rksy-azure-aks   resource.cloud.provider="azure"   services carrying it: payment
    rksy-gcp-gke     resource.cloud.provider="gcp"     services carrying it: frontend, payment
    rksy-aws         no value at all, 747142 bytes inspected

Out of 17 services. The collector runs `otelcol.processor.resourcedetection` with
`detectors = ["env","system"]` — NO cloud detector — so every cloud.* attribute is produced by the
APPLICATION SDK. Only the Node.js services running
`NODE_OPTIONS=--require @opentelemetry/auto-instrumentations-node/register` have one at all.

EKS is empty because the AWS EC2 detector cannot complete the IMDSv2 token exchange. Proven from
inside a pod on rksy-aws:

    GET  169.254.169.254/latest/meta-data/instance-id  -> HTTP/1.1 401 Unauthorized
    PUT  169.254.169.254/latest/api/token              -> socket timeout

IMDS is REACHABLE — the 401 is a real answer. Only the token PUT fails, and every node runs
`HttpTokens: required` with `HttpPutResponseHopLimit: 1`, which is the terraform-aws-modules/eks
default and what a security-conscious EKS customer runs. AKS and GKE metadata services need no
token exchange, so their detectors return.

The METRICS lane agrees — this is not a traces-only quirk. target_info label keys:

    rksy-aws        cloud, host_arch, host_name
    rksy-azure-aks  cloud, cloud_platform, cloud_provider, cloud_region, cloud_resource_id,
                    host_arch, host_id, host_name, host_type
    rksy-gcp-gke    cloud, cloud_account_id, cloud_availability_zone, cloud_platform,
                    cloud_provider, host_arch, host_id, host_name

`cloud` with no suffix is present everywhere because Alloy stamps it as an external label; it is
not detected. LOGS carry no cloud.* on any cluster.

VERDICT RECORDED IN RKSY-0025: model the ASYMMETRY, not the ideal. Emitting cloud.* on every span
is more uniform than any of the three real substrates, and would validate a dashboard or a scope
query that breaks on real data.

## 2. host.name on every span is the COLLECTOR'S pod, on all three clouds

    rksy-aws        host_name = grafana-k8s-monitoring-alloy-receiver-947448fc9-2mk8f
    rksy-azure-aks  host_name = grafana-k8s-monitoring-alloy-receiver-848479bbb4-tpjk2
    rksy-gcp-gke    host_name = grafana-k8s-monitoring-alloy-receiver-7c87c68fb8-pfvn9

host.arch, os.type and os.version follow — they describe the collector's node kernel, not the
application's. Cause: the chart's resourcedetection runs with `override = true`, so the collector's
`system` detector replaces whatever the SDK set. On AKS this produces an internally inconsistent
resource: cloud_resource_id names the app's VMSS instance while host_name names the collector pod.
It also means host.name has ZERO per-application cardinality — every service shares one value.

## What this asks of synthkit

  - span resource attributes should reflect a MIXED estate: most services carrying no cloud.*, a
    minority carrying it, and none on an AWS substrate
  - host.name / host.arch / os.* on a collector-forwarded span should be the collector's identity,
    not a per-service host, if synthkit models the k8s-monitoring default at all
  - nothing should treat a DETECTED resource attribute as a reliable scoping dimension. The
    collector-stamped cluster/cloud label is the only one present on all three substrates and all
    four signals

Do NOT close this by stamping cloud.* uniformly, and do NOT "fix" host.name to the app's host
without deciding deliberately: both would record an estate that does not exist.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The catalog's span resource attributes reflect the measured mix rather than a uniform ideal, with a stated rule for which services carry cloud.* and which do not
- [x] #2 A decision is recorded on whether synthkit models the collector-identity override of host.name/host.arch/os.*, with its reason
- [x] #3 Nothing in synthkit or its dashboards scopes per-cloud behaviour off a detected span resource attribute
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
After SKT-0046, implement the measured minority/asymmetric cloud resource rule and deterministic collector host identity, audit provider scoping, and prove cardinality/determinism with focused tests.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented the measured mixed span-resource policy: only Node-runtime services on two non-AWS substrates receive cloud.provider; AWS and non-Node services omit it. Collector host.name, host.arch and os.type are deterministic per cluster and shared across applications, gated by application_observability. os.version is omitted because no sourced deterministic contract exists. Dashboard audit found no universal scoping on detected span cloud attributes. Focused tests plus just check, just dump, and just e2e passed; new emission was not proved live because the standing deployment pins an immutable image.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Done: span resources now model the observed cloud-provider minority and collector-identity override instead of a uniform ideal, and the dashboard audit found no detected-cloud universal scope. Verified by focused tests and full check/dump/e2e gates; live proof awaits a later immutable-image promotion.
<!-- SECTION:FINAL_SUMMARY:END -->
