---
id: SKT-0136
title: Bound one-shot validation and preflight by an absolute deadline
status: In Progress
assignee:
  - codex
created_date: '2026-10-15 13:33'
updated_date: '2026-10-15 13:55'
labels: []
dependencies: []
priority: high
ordinal: 259000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A Job relative execution budget starts when its controller activates it. A delayed controller can therefore start an otherwise valid validation or credentialed preflight process after the intended wall-clock expiry. Add an optional explicit absolute deadline to the public one-shot CLI: refuse expired invocations before configuration or network work, pass the remaining deadline through real endpoint probes and refuse a late success. Leave ordinary long-running generation unchanged. This is a generic engine capability for bounded orchestrators; no deployment-specific identities belong here.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A valid RFC3339 absolute deadline is accepted only for a single validation or preflight mode; malformed, elapsed or incompatible-mode input fails without echoing credential-bearing input
- [x] #2 Real preflight HTTP requests share the absolute context deadline and an invocation cannot report success after it; an already-expired invocation makes no receiver requests
- [x] #3 Actual CLI fixtures cover valid and expired entry, live local endpoint cancellation and unchanged omitted-deadline behavior; public docs and operational guidance describe the limits
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add an optional one-shot-deadline RFC3339 flag to validate/preflight only. Parse and check it before config loading, retain omitted behavior, and pass a deadline context into real preflight probes; check deadline again before reporting validation/preflight success. First observe a real CLI future-deadline validation fail on the unmodified binary, then cover expired/malformed/mode cases and real local TLS probe cancellation. This is an entry/HTTP-context boundary, not a guarantee that receiver-side processing stops atomically at wall-clock expiry. Update docs and relevant operational skill, regenerate links, focused race then final justcheck/CodeRabbit/exactCI. No live deployment or image change during the existing trial.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Real built CLI failed on unimplemented future-deadline validate before sourcechange, so old unknown-flag refusal is not counted as expiryproof. Implemented explicit-deadline flag parsed beforeconfig with fixed non-input-echoing diagnostics; omittedbehavior unchanged. Context passed through actualmain to realpreflight; context-aware offlinevalidation refuseslate-success. FocusedrealCLI/race green. Separate subprocess test executesactualmain/flags/config/probes with onlytest-edge TLSroot injection: healthycontrol3receiverrequests; blockedrequest canceledatabsolutedeadline, onecallonly and fixednonzero diagnostic. Removing actualmain contextwiring causedthat test tofail after8s/kill as expected, thenrestored. No production test hook or insecureTLS flag. Docs/skill/nestedcomposition contractupdated andjustgen0. FinalwholeNode24check andCodeRabbit active; no publicpush until prior logfix exactCIterminal, no liveimagechange.

Final whole contributor justcheck0 with Node24.18.0 at13:54:47Z and all seven final file hashes unchanged; all portable gates including full plain/race, helpers/UI/Helm/Compose/corpus/troubleshooting passed. Complete CodeRabbit7files0. Explicit otlp-native justdump matches prior clean inventory byte-for-byte156988bytes. Actualmain HTTP positivecontrol3calls and absolute deadline1call/canceled/nonsuccess; background-context negativecontrol fails as intended. Source landing/exactCI next, no live image change.
<!-- SECTION:NOTES:END -->
