---
id: SKT-0043
title: 'Sigil is now Agent Observability: re-verify endpoints, auth and payload format'
status: To Do
assignee: []
created_date: '2026-08-30 12:35'
updated_date: '2026-08-30 12:36'
labels: []
dependencies: []
ordinal: 134000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The sigil lane was built against the product as it existed in June 2026. Rob's read is that it has moved on substantially — it is now Grafana's Agent Observability product, with different URLs and changes to the data format. That makes every sigil-shaped assumption in this repository suspect until re-verified.

LIVE CORROBORATION, 2026-08-30. The local Docker deployment (blueprints k8s-full-stack, aws-cloud-services, dbo11y-mysql, netobs-enterprise, profiling-demo, otlp-native, acme-ai-platform, synthetic-checks) had been running two hours and was UNHEALTHY — readiness 503 — with a continuous stream of:

  queue: sigil sink flush of N items failed: code=rejected

Rejected, not a transport error: the endpoint answered and refused the payload. That is the signature of a format or contract change rather than a network or credential problem, and it had been failing for the whole two hours without anyone noticing, because nothing surfaces a per-sink failure except the container log.

The same container was also logging , which is a DIFFERENT failure mode and probably a separate problem — do not assume one fix covers both.

Scope, and none of it should be taken from memory or from the existing code, which is the thing under suspicion:
  - the current product name, endpoints and regional hostnames
  - the auth model and which tenant id or token it now expects
  - the request format, and what specifically is being rejected — capture an actual response body rather than only the status
  - whether the vocabulary the construct emits still matches the product's schema
  - whether the emitted signal is still a distinct product surface at all, or has folded into an existing OTLP or Prometheus path, which would change the design rather than the endpoint

Also re-check the credential topology while in here: the same local deployment had GC_PROFILES_USER=1319552 (robk) and GC_PYROSCOPE_USER=1802885 (rkaidev) pointed at the SAME profiles host. Two tenants for one signal in one process; one of them is wrong.

A rejected sink must not be able to fail silently for hours again. Whatever the fix, the readiness or self-observability surface should make a persistently failing sink visible without reading container logs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Current endpoints, auth and payload format are confirmed against the live product or current vendor docs, with provenance recorded
- [ ] #2 An actual rejection response body is captured and the cause named, not inferred from the status code
- [ ] #3 The sigil construct and its signals entry are corrected to match, or the lane is explicitly retired if the product surface no longer exists
- [ ] #4 The profiles/pyroscope tenant mismatch is resolved
- [ ] #5 A persistently failing sink is visible without reading container logs
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Restoring one line the description lost to shell substitution when this task was created. The sentence reading "The same container was also logging , which is a DIFFERENT failure mode" should read:

The same container was also logging a faro sink flush failure with code=transport, which is a DIFFERENT failure mode from the sigil rejection and probably a separate problem — do not assume one fix covers both. A transport code means the request did not complete; a rejected code means the endpoint answered and refused the payload.

Full context on where synthkit was running when this was found, 2026-08-30:
  local Docker      -> robk    (metrics, OTLP, profiles) — this is the one that was failing; now STOPPED
  EKS lab robknight -> rkaidev (GC_PROM_USER 3529994, GC_OTLP_USER and GC_PROFILES_USER 1802885) — left running deliberately
  jules             -> nothing
  camden            -> synthetic-monitoring-agent only, not synthkit

So the sigil rejection was observed against the robk-targeted local deployment. Whether the rkaidev-targeted EKS deployment is failing the same way has NOT been checked and should be, because it has been running 42 hours and nothing surfaces a per-sink failure except the container log.

Answered the open question above, same day. The EKS deployment is NOT failing the same way, and the reason is which blueprints each runs:

  local Docker -> robk, 8 blueprints:
    k8s-full-stack, aws-cloud-services, dbo11y-mysql, netobs-enterprise,
    profiling-demo, otlp-native, acme-ai-platform, synthetic-checks
    Result: readiness 503, sigil rejected continuously, faro transport errors.

  EKS robknight -> rkaidev, 7 blueprints:
    k8s-full-stack, k8s-logs-events, aws-cloud-services, dbo11y-mysql,
    netobs-enterprise, otlp-native, profiling-demo
    Result: 1/1 Running 42h, ZERO lines matching failed or error in a 10 minute window,
    heartbeat healthy.

The two sets differ: EKS has k8s-logs-events and does NOT have acme-ai-platform or synthetic-checks. acme-ai-platform is the blueprint that carries the sigil lane, so the EKS deployment never exercises it — that is why it is clean, not because the sigil lane works there.

Two consequences. The breakage is real and is not environment-specific, so nothing is fixed by the EKS deployment looking healthy. And the faro transport errors are almost certainly tied to synthetic-checks or acme-ai-platform for the same reason, which narrows where to look.

Reproduce by running acme-ai-platform anywhere, not by comparing the two deployments.
<!-- SECTION:NOTES:END -->
