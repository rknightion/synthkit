---
id: SKT-0043
title: 'Sigil is now Agent Observability: re-verify endpoints, auth and payload format'
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-30 12:35'
updated_date: '2026-09-02 08:55'
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

Also re-check the credential topology while in here. The generated-profiles sink and process self-profiling sink use separate credential triplets and may deliberately target different stacks even when they share a regional profiles host. Do not infer a mismatch from the shared host.

A rejected sink must not be able to fail silently for hours again. Whatever the fix, the readiness or self-observability surface should make a persistently failing sink visible without reading container logs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Current endpoints, auth and payload format are confirmed against the live product or current vendor docs, with provenance recorded
- [ ] #2 An actual rejection response body is captured and the cause named, not inferred from the status code
- [x] #3 The sigil construct and its signals entry are corrected to match, or the lane is explicitly retired if the product surface no longer exists
- [x] #4 The profiles/pyroscope tenant mismatch is resolved
- [x] #5 A persistently failing sink is visible without reading container logs
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
2026-09-03 wave: Establish the current Agent Observability contract from current vendor docs and one root-scheduled request capped at one request per ten minutes; capture the redacted response body; correct the owned emitter/signal surface or park at the design seam; prove exact teardown. Never execute the 600/min fixture or weaken the shipped-agent guard.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Restoring one line the description lost to shell substitution when this task was created. The sentence reading "The same container was also logging , which is a DIFFERENT failure mode" should read:

The same container was also logging a faro sink flush failure with code=transport, which is a DIFFERENT failure mode from the sigil rejection and probably a separate problem — do not assume one fix covers both. A transport code means the request did not complete; a rejected code means the endpoint answered and refused the payload.

Privacy-safe context on where synthkit was running when this was found, 2026-08-30:
  local acceptance deployment -> selected synthetic target; this is the one that was failing and was stopped
  separate lab deployment     -> different selected target; left running deliberately
  other reviewed hosts        -> no synthkit process

So the sigil rejection was observed against the local acceptance deployment. Whether the separate lab deployment was failing the same way had not yet been checked, and nothing surfaced a per-sink failure except the container log.

Answered the open question above, same day. The separate lab deployment was not failing the same way, and the reason was which blueprints each selected:

  local acceptance deployment, 8 blueprints:
    k8s-full-stack, aws-cloud-services, dbo11y-mysql, netobs-enterprise,
    profiling-demo, otlp-native, acme-ai-platform, synthetic-checks
    Result: readiness 503, sigil rejected continuously, faro transport errors.

  separate lab deployment, 7 blueprints:
    k8s-full-stack, k8s-logs-events, aws-cloud-services, dbo11y-mysql,
    netobs-enterprise, otlp-native, profiling-demo
    Result: 1/1 Running 42h, ZERO lines matching failed or error in a 10 minute window,
    heartbeat healthy.

The two sets differ: the separate lab deployment has k8s-logs-events and does not have acme-ai-platform or synthetic-checks. acme-ai-platform is the blueprint that carries the sigil lane, so that deployment never exercises it — that is why it is clean, not because the sigil lane works there.

Two consequences. The breakage is real and is not environment-specific, so nothing is fixed by the EKS deployment looking healthy. And the faro transport errors are almost certainly tied to synthetic-checks or acme-ai-platform for the same reason, which narrows where to look.

Reproduce by running acme-ai-platform anywhere, not by comparing the two deployments.

CORRECTION, same day: acceptance criterion 4 was WRONG and is retracted. There is no tenant mismatch to resolve.

GC_PROFILES_USER and GC_PYROSCOPE_USER are two DIFFERENT data paths that deliberately point at DIFFERENT stacks, and the code says so explicitly:

  GC_PROFILES_*   the SYNTHETIC profiles sink. Ships generated profile data to the TARGET
                  stack, same one as metrics/logs/traces. Auth REUSES the shared GC_TOKEN.
  GC_PYROSCOPE_*  SELF-OBSERVABILITY. Ships the synthkit BINARY OWN continuous profiles, via
                  its own credential triplet, and internal/config/config.go says
                  "NOT GC_TOKEN" while .env.example says "to a DIFFERENT Grafana Cloud stack".

The local generated-profiles and process self-profiling credentials intentionally select different targets. Both can legitimately resolve to the same regional Pyroscope cluster, which is what made the separate values look like a tenant mismatch.

Verified against the Grafana Cloud API rather than inferred: the generated-signal credentials consistently matched the selected synthetic target, and the process self-profiling credential was the only intentionally separate value. Deployment and account identifiers are omitted from this tracker.

Making those credentials identical would have pointed process self-profiling at the synthetic target and broken the separation the design is built on. Criterion 4 is therefore satisfied as no-change-needed rather than leaving a trap in the task.

2026-08-30 hygiene correction: removed deployment-specific names and numeric tenant/account identifiers while preserving the product contract, reproduction boundary, and intentional credential separation.

2026-09-02 overnight containment follow-up: an unqualified local e2e run selected the test-only agent fixture and produced rejected Sigil flushes. Compose was torn down immediately and no further local deployment or local e2e was run. The e2e fixture now requires literal SYNTHKIT_E2E_INCLUDE_AGENT=true; empty or false selects only the non-agent smoke topology. A later just check exposed a second selector path: the default signal-fidelity inventory selected grafana-ai-o11y. The default fidelity blueprint list now excludes that blueprint while retaining an explicit SIGNAL_FIDELITY_BLUEPRINTS opt-in. These are containment guards, not evidence that the endpoint or payload defect is fixed.

2026-09-03 capped re-verification: current Grafana Cloud documentation was refreshed through Context7 from https://grafana.com/docs/grafana-cloud/machine-learning/agent-observability/configure/sdk, https://grafana.com/docs/grafana-cloud/machine-learning/agent-observability/get-started/grafana-cloud, and https://grafana.com/docs/grafana-cloud/machine-learning/agent-observability/reference/generation-ingest-api. The documented contract is HTTP transport to the endpoint supplied by the Agent Observability configuration page, authenticated with a Grafana Cloud instance ID plus Cloud Access Policy token. The one authorized live request corroborated the existing generation endpoint and HTTP Basic tenant/token transport: POST /api/v1/generations:export with application/json protojson was accepted without X-Scope-OrgID. The response was HTTP 202 with three accepted generation results; identifiers are omitted. Existing sink construction and signals vocabulary therefore already match the currently accepted generation path, so no emitter correction or lane retirement is justified. Persistent failure visibility is already objective: pushstatus folds every non-dry-run failure into current error state with normalized cause and timestamps; readiness becomes not-ready for the required lane; authenticated /control/status exposes sink and queue history. Exact-head hosted CI at 401bc6af included the pushstatus/control tests and passed. Probe arithmetic and stop: exactly one HTTP POST containing one plausible three-turn conversation in one ten-minute window, 1/10 = 0.1 request/session per minute, with zero redirects, retries, replays, or follow-up probes; execution stopped after the first response. Post-probe census found no exact probe or synthkit process, synthkit container, volume, or k3d cluster, no selected agent-declaring blueprint, and the agent fixture remained false. AC2 remains unproven: the only authorized request was accepted, so there is no current rejection body and the historical rejection cause cannot honestly be named.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked after the single capped live request returned HTTP 202 with all three generations accepted. Current docs plus the live response confirm the present endpoint/auth/payload path and existing code matches it; control status already exposes persistent sink failure. Resume only from a naturally occurring future rejection body or a separately authorized capped probe, then name the rejection cause without inference. No additional request is authorized in this run.
<!-- SECTION:FINAL_SUMMARY:END -->
