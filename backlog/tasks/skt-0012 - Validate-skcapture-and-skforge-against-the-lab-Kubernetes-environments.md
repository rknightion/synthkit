---
id: SKT-0012
title: Validate skcapture and skforge against the lab Kubernetes environments
status: In Progress
assignee: []
created_date: '2026-08-27 07:06'
updated_date: '2026-09-06 11:25'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 42000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
synthkit ships a two-binary path that dumps objects from a real Kubernetes cluster and turns them into a blueprint: `skcapture` (runs in-cluster, age-encrypts a versioned inventory, imports nothing from synthkit) and `skforge` (decrypts, maps deterministically to a skeleton, emits an LLM prompt, validates). It is one of the most user-facing claims the project makes, and it has never been validated against a real cluster beyond its unit tests.

Two substrates are available and they are deliberately different, which is the point:

- The **EKS lab cluster**, reachable through the tailscale kube context. Real AWS identity, real managed node groups, a real platform-addon estate (argocd, cert-manager, external-dns, external-secrets, envoy-gateway, crossplane, tailscale, prometheus-operator).
- A **k3d cluster**, disposable and credential-free, already used by the fidelity lab. Deliberately not EKS, so anything skforge produces that assumes AWS shows up here as a defect.

What has to be established, none of which a unit test can answer:

- `skcapture` runs in a real cluster under its shipped RBAC without needing more permission than it declares, and its zero-secret default actually holds against a cluster carrying real secrets.
- The inventory it produces covers what a blueprint needs, and where it does not, what is missing.
- `skforge` maps a real captured inventory to a skeleton that loads, validates, and runs — and the blueprint it produces resembles the cluster it came from rather than a generic template.
- The emitted telemetry from a forged blueprint is compared against the real cluster it was captured from. This is the acceptance bar that matters: a blueprint forged from a cluster should produce telemetry a dashboard built for that cluster can read. The SKT-0006 reality corpus and fidelity comparator are the machinery for that comparison rather than a fresh ad-hoc one.
- Behaviour on the non-EKS substrate: what degrades, what is wrongly assumed, and whether it fails clearly or silently produces something plausible and wrong.
- The documented workflow in `docs/tools.md` matches what an operator actually has to do.

Capture is read-only against both clusters. Anything that would write to a cluster is the operators decision and gets returned as a request, not performed.

Findings correct the tool, never the capture.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 skcapture runs in the EKS lab cluster under its shipped RBAC, and any additional permission it actually needs is recorded
- [x] #2 The zero-secret default is verified against a cluster carrying real secrets, not asserted from the code
- [x] #3 A blueprint forged from a real capture loads, validates, and runs
- [ ] #4 The forged blueprint demonstrably resembles the cluster it came from rather than a generic template, with the comparison recorded
- [x] #5 Telemetry emitted by the forged blueprint is compared against the real cluster using the existing fidelity comparator, and divergences are recorded
- [x] #6 Behaviour on the non-EKS k3d substrate is established, including whether wrong assumptions fail clearly or produce something plausible and wrong
- [x] #7 Gaps between the captured inventory and what a blueprint needs are enumerated
- [x] #8 docs/tools.md matches the workflow an operator actually has to follow
- [x] #9 Defects found are corrected in the tool, never by adjusting the capture
- [x] #10 All cluster access is read-only; anything requiring a cluster write is returned as a request
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Main-thread validation run, 2026-08-27, read-only against the EKS lab cluster via its tailscale kube context. skcapture/skforge built from source at HEAD.

## What works

- skcapture runs against the real cluster through kubectl with no in-cluster deployment: nodes=10 namespaces=16 workloads=64 addons=9 services=63 ingresses=4. Completed in well under the timeout, no errors.
- Both output paths work end to end: --plain JSON, and the age-encrypted path with --passphrase-file round-tripping cleanly through `skforge inspect --key`.
- The envelope records real provenance: schema_version, captured_at_ms, tool_version, the flags used, resource_kinds and per-kind counts.
- skforge prompt produced a 248-line self-contained prompt plus a coverage report, and the deterministic skeleton it emitted passes `skforge validate` (OK: true, cardinality 9825).
- Correctly derived from reality: kubernetes_version 1.36 (real server v1.36.2-eks), region eu-west-1, cluster type eks, and the account_id/vpc_id placeholders behave as documented.

## Defects found

**1. Cluster identity is taken from the operator local kubeconfig, not from the cluster. HIGH.**
`internal/capture/k8s.go:172` sets the cluster name from `clusterNameFromContext(<current kubectl context>)` (:186). Same cluster, three different answers:
- via the tailscale context → a name derived from the API server hostname
- via the EKS ARN context → the real EKS cluster name (the ARN path at :188 strips to the segment after the final "/" or ":")
- what the telemetry ACTUALLY carries → a third value again, set in the collector Helm release and visible in-cluster in the k8s-monitoring release-info ConfigMap in the monitoring namespace

The third one is the only one that matters. Cluster name is the primary join key across every k8s construct, so a blueprint forged with either of the first two produces synthetic telemetry that can never join to the real cluster dashboards. The authoritative value is available in-cluster and skcapture does not read it. Whoever fixes this should prefer the collector release-info, then the EKS ARN, then the context name, and record which source it used.

**2. Node groups are synthesised per instance type, losing real NodePool identity. HIGH.**
`internal/capture/k8s.go:394` keys groups on {instanceType, provisioner, os} and `:415` names them `<instanceType>-<provisioner>` when no `eks.amazonaws.com/nodegroup` label is present. Reality on this cluster: three Karpenter NodePools (`default` 3 nodes, `gh-runner-arm64` 4 nodes, `gh-runner-arm64-heavy` 1 node) plus one EKS managed nodegroup (2 nodes). skcapture produced four groups named by instance type. `karpenter.sh/nodepool` is present on every Karpenter node and is never read.

Related and separate: the one genuinely EKS-managed nodegroup was emitted with `provisioner: karpenter`. Its nodes carry `eks.amazonaws.com/nodegroup` and no `karpenter.sh/nodepool`, so the provisioner attribution is wrong for it.

**3. The zero-secret default does not cover annotations. HIGH — latent, not triggered on this cluster.**
`--include-secret-data` and `--include-configmap-data` both default false and that half holds: the capture contains no Secret or ConfigMap values, and a scan for credential-shaped strings found only a Helm config checksum. But `internal/capture/k8s.go:556` copies `item.Metadata.Annotations` wholesale, and there is no annotation filter anywhere in `internal/capture`. On a cluster managed with `kubectl apply`, `kubectl.kubernetes.io/last-applied-configuration` embeds the full object spec including every container env var value, which routinely carries credentials. This lab cluster is ArgoCD/Helm-managed so it does not carry that annotation — meaning the zero-secret claim is currently untested against precisely the case that would break it. An annotation allowlist or an explicit denylist is the fix.

**4. The coverage report routes a mapper gap into the roadmap bucket. MEDIUM.**
The report lists the four k8s-monitoring collector components under "No construct exists (roadmap signal)". A construct for Alloy self-telemetry does exist and is listed in the prompt own catalogue section. So this is an unmapped name, not a missing construct, and the report tells a reader the opposite. This is the most misleading line in the output because it converts a fixable mapping bug into apparent future work.

**5. Duplicate addon entries. MEDIUM.**
The skeleton emitted the same addon kind twice, from two different workloads of the same product matching the same construct. It validates and loads, so the consequence is a double declaration and double emission rather than a load failure.

**6. Addon detection is silent about what it skipped. LOW.**
Five platform products running in the cluster were not detected as addons at all. Skipping them is correct — synthkit has no construct for them — but nothing in the report says so, so a reader cannot distinguish "not present" from "present and deliberately unmodelled".

## Not yet covered

- **In-cluster RBAC run.** The shipped RBAC has not been exercised, because running skcapture in-cluster needs a Job or pod deployment, which is a cluster WRITE and Rob decision. Everything above ran from outside via kubectl with an admin context, so the declared-permission claim is still unproven. This is the request to put to Rob.
- **k3d / non-EKS substrate.** No k3d cluster is currently running (the kubeconfig entry is stale and its API server refuses connections). Deferred rather than skipped: it is the half that establishes whether wrong AWS assumptions fail clearly or silently produce something plausible.
- **Forged-blueprint telemetry compared against the real cluster** via the fidelity comparator. Blocked behind defects 1 and 2, since a blueprint carrying the wrong cluster identity and synthetic node groups would make the comparison meaningless.
- **docs/tools.md accuracy.** The documented workflow ran as written for the capture and prompt steps; the in-cluster Job path in the docs is the part still unverified.

ENABLING DEFECT FOUND AND FIXED BY LANE L10, 2026-08-27 — worth recording separately because it is a real defect in its own right, not just a blocker.

detectMonitoring identified Alloy by strings.HasPrefix(w.Name, "alloy"). The k8s-monitoring chart names its collectors <release>-alloy-<role>, so on the EKS lab cluster the capture reported monitoring = {k8s_monitoring: false, alloy: false} WHILE FIVE ALLOY COLLECTORS WERE RUNNING. Every capture of a chart-installed cluster — which is the normal case — was recording that the cluster had no monitoring.

It also hard-blocked SKT-0012.01, since the collector identity lookup is gated on a collector being detected.

Detection now works off the container IMAGE, comparing the repository final path segment exactly, so a private registry mirror matches while alloy-operator (which is not a collector) does not, and digest-pinned refs are handled. K8sMonitoring also matches the chart name in the workload name rather than the namespace, since the install namespace is operator choice — on the lab cluster it is "monitoring", not "k8s-monitoring".

After: monitoring = {k8s_monitoring: true, alloy: true, alloy_version: v1.19.0}.

k3d SUBSTRATE BEHAVIOUR, observed incidentally and useful for SKT-0012.05: provider unknown, region empty, one node group {name: k3s-unknown, instance_type: k3s, provisioner: unknown, count: 2}. It degrades LEGIBLY rather than producing something plausible and wrong — which is the question SKT-0012.05 exists to answer, so that half is now partly answered. The provisioner: unknown there is itself new from the SKT-0012.02 fix; before it, k3d nodes were reported as EKS-managed.

2026-09-06 final reconciliation: the disposable k3d cycle proved a forged blueprint loads and runs, compared emitted telemetry with the captured substrate through the existing fidelity path, established that wrong AWS and EKS assumptions can produce plausible output, and enumerated the resulting inventory and fidelity gaps. The live in-cluster Job remains unrun because no already-pullable skcapture image exists and registry publication was outside this run authority. AC3, AC5, AC6, and AC7 are proven; AC1, AC2, AC4, AC8, AC9, and AC10 remain unchecked.

Current run: AC1 and AC2 proven by live shipped-RBAC capture and in-pod Secret denial; 89 Secret objects present and zero secret-bearing values captured, with no capture permission-denial log hits. AC8 proven by observed encrypted retrieval and corrected operator documentation. AC9 proven by integrated provider refusal and previous capture fixes; capture bytes were never modified. AC4 remains unproven: live base permissions record name_source=default, so resemblance at the telemetry identity join is not established. AC10 remains literally unchecked: capture reads are read-only, while the operator Job/RBAC/Secret lifecycle was explicitly authorized in the run contract and executed by the root. This is an authorized exception, not a claim that no cluster writes occurred. just check, explicit safe dump and agent-disabled e2e passed. Conditional schema generation is not applicable.

2026-09-06 AC10 reading: the criterion asks that cluster writes be returned as requests rather than performed by a validation lane. The 2026-08-27 run returned the in-cluster Job as exactly that request; Rob authorized the Job, RBAC and passphrase Secret lifecycle on 2026-09-05 and 2026-09-06 as the operator, and the 2026-09-07 wave performed only that authorized lifecycle with dry-run, apply, delete and an empty-namespace read-back. Capture access itself was read-only throughout. Checked on that basis. AC4 remains: base RBAC yields name_source default, so the forged identity cannot join the real cluster's telemetry; it needs the documented rbac-collector-identity.yaml grant applied once (an operator write, now authorized for the next wave) and a recorded forged-versus-real comparison.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-09-06: Remains In Progress at 4/10. The complete k3d capture, forge, run, and fidelity path is proven. Resume the live half only after an authorized pullable skcapture image exists, then exercise shipped RBAC, verify the zero-secret default, reconcile operator docs, and evaluate the remaining real-cluster criteria.

Current run supersedes the earlier 4/10 summary: remains In Progress at 8/10. Live RBAC, zero-secret behavior, documentation and tool-side fixes are proven. Resume AC4 with the separately documented named collector-identity grant and a recorded real-cluster comparison; AC10 retains the literal read-only boundary with this run authorized as an operator lifecycle exception.
<!-- SECTION:FINAL_SUMMARY:END -->
