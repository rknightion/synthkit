---
id: SKT-0049
title: Restore otlp-native to the lab deployment once a fixed image ships
status: Done
assignee:
  - '@codex'
created_date: '2026-08-31 15:13'
updated_date: '2026-09-02 21:14'
labels: []
dependencies: []
ordinal: 140000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The standing lab deployment (rkps-awsinfra `applications/synthkit/values.yaml`, EKS-0065) had
`otlp-native` REMOVED from `blueprintNames` on 2026-08-31 as an emergency stop, because the image it
pins bakes the version of that blueprint carrying an `ai_agent` fleet at `sessions_per_min: 600`.
That was pushing fabricated gen_ai OTLP metrics and traces into stack 1802885 continuously and
caused problems in the Agent Observability product.

The blueprint is fixed upstream (commit 187b7f0): the agents moved to `e2e/fixtures/e2e-agents.yaml`,
outside `blueprints/`. But the lab pins an immutable image tag, `main-65a7e94`, so the fix cannot
reach it until the tag moves.

`otlp-native` is the ONLY blueprint exercising the two-mode native-OTLP comparison
(k8s_monitoring-enriched vs naked) that this lab exists to see, so leaving it out has a real cost.
`profiling-demo` keeps the OTLP-metrics lane fed meanwhile, which is why readiness stayed green
rather than sitting at not_attempted forever.

TO RESTORE:
  1. Confirm CI published an image containing 187b7f0 or later — ghcr.io/rknightion/synthkit,
     tag main-<sha>.
  2. In rkps-awsinfra `applications/synthkit/values.yaml`, bump `image.tag` to that tag AND add
     `otlp-native` back to `blueprintNames`. Both in one commit; bumping the tag alone leaves the
     blueprint deselected, and re-adding it alone re-starts the 600/min emission.
  3. Delete the temporary paragraph above `blueprintNames` explaining the removal.
  4. Verify from the pod's own log that the selected set is 7 and that no gen_ai/agent lines appear:
       kubectl -n synthkit logs -l app.kubernetes.io/name=synthkit | grep -i 'selected blueprints'

NOTE the emergency stop was applied BOTH in git and by a direct `kubectl set env` on the Deployment,
because ArgoCD had not yet polled. Git and the cluster agree, so ArgoCD selfHeal will not fight it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 image.tag in rkps-awsinfra points at a build containing commit 187b7f0 or later
- [x] #2 otlp-native is back in blueprintNames and the temporary removal note is deleted
- [x] #3 The running pod's log shows 7 selected blueprints and no gen_ai or agent emission
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Confirm the fixed immutable image is published, then change only the standing deployment values file: set image.tag to main-4fe898f, restore otlp-native in blueprintNames, and remove the temporary emergency note. Commit and push that repository, wait for ArgoCD synchronization, then verify the running revision, selected set, health, absence of agent/Sigil emission, and restored two-mode native-OTLP data. Revert the values change if health does not converge.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-02 evidence: the bounded values-only change is committed and synchronized. The workload is healthy on the fixed immutable image, selects seven safe blueprints including otlp-native, and logs show no agent or Sigil emission. The tracker ACs are met. The overnight goal's additional two-mode far-side data proof remains unproven because no authorized query path was established; resume with explicit access to the standing lab data destination and prove both enriched and naked native-OTLP series after synchronization. Synthkit just check and just dump passed; generation was not applicable.

2026-09-03 read-back evidence: the distinct enriched/naked native-OTLP query was attempted against the named lab context with the captured http_server_request_duration_seconds_count family and the two declared service identities. The context was online, but datasource discovery and the explicit Prometheus datasource path both returned Unauthorized. The standing-cluster read path also stopped at an expired AWS SSO session. Therefore the goal-level far-side proof for the two modes, SKT-0046 promoted labels, and SKT-0048 span-resource changes remains unproven; no infrastructure write was made. Resume with a fresh noninteractive metrics-read credential or refreshed standing-lab read session, then query both declared services in one bounded window and compare the sourced label/resource sets.

2026-09-02: far-side proof obtained; the read-credential blocker is resolved. A CAP token with metrics:read and logs:read on every instance in the org is stored at /Users/rob/repos/chat-personal/grafana/cap_token_metrics_read (chmod 600, policy 2nd-sept-2026-token), verified HTTP 200 on Mimir and Loki query for all four instances.

Root cause of the three previous read parks was not a missing credential. The Mimir Basic-auth username is the Prometheus instance id (hmInstancePromId), not the stack id; Loki wants hlInstanceId. Both differ from the stack id. Using the stack id returns 401 with the same body a revoked credential produces, so a working one reads as dead. Lab stack 1802885 maps to prom tenant 3529994 and loki tenant 1760672. Resolve tenant ids from the Cloud API instances endpoint at run time.

Two-mode read-back, one bounded window, http_server_request_duration_seconds_count by service_name: otlp-api-enriched returned 6 series carrying k8s_cluster_name, k8s_deployment_name, k8s_namespace_name and k8s_pod_name; otlp-api-naked returned 4 series with no k8s_* label at all. Shared on both: deployment_environment_name, http_request_method, http_response_status_code, http_route, instance, job, service_instance_id, service_name, service_namespace, service_version. The two declared modes are therefore distinguishable in live gateway-translated data, which is what this blueprint exists to show. Same result is the first live proof of SKT-0046 promoted resource labels, recorded as not-proven-live in two consecutive wave reports. SKT-0048 span-resource proof needs a traces-side query and is not covered here.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-09-02: Parked at the goal's stricter live-data boundary after the tracker ACs passed: the lab is healthy on the fixed image with otlp-native restored and no agent emission, but far-side two-mode data remains unproven.

2026-09-03: Parked at the unauthorized/expired live read boundary. The deployment remains previously proven healthy, but the two-mode far-side data and SKT-0046/SKT-0048 live emission are not proved by this wave.

2026-09-02 closeout: closed on live far-side evidence. A bounded read-back proves the two declared native-OTLP modes produce distinguishable label sets: otlp-api-enriched carries the four k8s_* resource labels, otlp-api-naked carries none. The blocker was the Mimir Basic-auth username, which is the Prometheus instance id rather than the stack id, not a missing credential.
<!-- SECTION:FINAL_SUMMARY:END -->
