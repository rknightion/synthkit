---
title: Platform Automation Traces
description: Bounded platform workflows with coherent approval decisions, HTTP retries and trace-derived metrics.
---

# Platform automation traces

An `app` workload can declare one optional `automation` sequence beneath a single `type: job`
controller service. This is a synthetic telemetry recipe, not a workflow engine: no API request,
approval service, shell command or physical operation is executed. Declare one app instance per
independently rated workflow. The ordinary [app service graph](../workloads.md) is unchanged when
`automation` is absent.

The fixture `e2e/fixtures/ai-factory-automation.yaml` covers node provisioning, firmware rollout,
drain and return to service, storage provisioning and self-service portal requests. Three additional
node-provisioning variants exercise recovery, exhaustion and rejected approval. Its one-request-per-
second traffic and probability endpoints are **test-only**, never production settings.

## Configuration and execution

```yaml
- type: app
  name: automation-node-provisioning
  for_each_env: true
  # Illustrative estate assumption: four provisioning runs per site per day.
  # Flat sparse arrivals approximate an operational schedule, not a periodic scheduler.
  traffic: {off_peak_rps: 0.0000462962962962963, peak_rps: 0.0000462962962962963}
  services:
    # Illustrative singleton controller; not a separately deployed service per task.
    - {name: provisioning-controller, type: job, entry: true, namespace: platform-automation, runtime: go, replicas: 1}
  automation:
    name: Node provisioning
    steps:
      - name: maintenance
        # Illustrative policy: one-minute decision budget and 1% rejection, not a captured rate.
        approval: {duration_ms: 60000, rejection_probability: 0.01}
      - name: provision-node
        # One-second API acknowledgement budget, not physical node commissioning time.
        # Initial send plus at most two immediate resends; illustrative 1% failure/95% recovery.
        http: {method: POST, url: "https://provisioner.example/v1/nodes", duration_ms: 1000, max_attempts: 3, failure_probability: 0.01, retry_success_probability: 0.95}
      - name: verify-node
        # Same API budget and bounded illustrative transient-error policy as provision-node.
        http: {method: GET, url: "https://provisioner.example/v1/nodes/ready", duration_ms: 1000, max_attempts: 3, failure_probability: 0.01, retry_success_probability: 0.95}
```

Each request follows declaration order. An approval emits `approval approved <step>` or
`approval rejected <step>`. A rejected decision terminates the workflow: later tasks emit nothing.
The decision span remains UNSET because the decision itself completed successfully; this recipe
marks the incomplete workflow ERROR with `error.type="_OTHER"`.

An HTTP task has an INTERNAL wrapper and one CLIENT span for each simulated send. The first send
fails with `failure_probability`; subsequent sends succeed with `retry_success_probability`.
`max_attempts` includes the initial send, defaults to one when omitted/zero, and cannot exceed five.
Retrying stops immediately after success. A recovered task/workflow remains UNSET; its earlier failed
attempt remains ERROR. Exhaustion marks the task/workflow ERROR and suppresses subsequent steps.
Retries are immediate simulated resends, **not a model of a particular client's backoff policy**.

All spans share the ledger trace ID. Child span IDs and outcomes are deterministic for that request,
so repeated projection cannot change its execution story. Approval and attempt durations are fixed
budgets; attempts run sequentially and tasks cover their attempts. The workflow adds two milliseconds
of controller overhead, with children inside its window. These recipes model the declared API-facing
sequence, not firmware installation time or physical provisioning completion.

### Bounded declaration

- Nonempty workflow name; 1–32 uniquely named steps, each exactly one `approval` or `http`.
- Finite probabilities in `[0,1]`, positive millisecond durations, worst-case sequence no longer
  than 24 hours including controller overhead. Excessive values are rejected before duration conversion.
- Exactly one traced job service without graph calls. Profiles, inline metrics/logs/spans, agentic
  flow, models, profiling and native OTLP metrics are unsupported in this bounded declaration.
- Known HTTP method and absolute HTTP(S) URL with a valid host/port. URL credentials, query strings
  and fragments are rejected. Use static generic operational endpoints; never place payloads,
  credentials, command output or resource/user IDs in paths or names.
- Outcomes are configured recipe probabilities, not an extension of app incident activation.

## Telemetry vocabulary and provenance

Workflow, task and approval metadata use **span names**, not new attributes. These operation names
are blueprint-declared; they are not standardized vendor workflow names. Native start/end timestamps
carry workflow/task/API latency. Retry ordinal belongs only to the HTTP resend attribute.

| Metadata | Representation | Pinned source |
|---|---|---|
| Workflow/task/approval outcome | Native span name; INTERNAL kind and UNSET/ERROR status | [Tracing API v1.61.0](https://github.com/open-telemetry/opentelemetry-specification/blob/v1.61.0/specification/trace/api.md) |
| Controller identity and routing group | Resource `service.name`, `service.namespace` | [Service registry v1.44.0](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/registry/attributes/service.md) |
| API method/status | CLIENT `http.request.method`, integer `http.response.status_code` (`200`/`503`) | [HTTP spans v1.44.0](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/http/http-spans.md) |
| Target system | CLIENT `server.address`, integer `server.port`, sanitized static `url.full` | [HTTP spans v1.44.0](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/http/http-spans.md) |
| Retry count | CLIENT integer `http.request.resend_count`, omitted on first send, then `1`, `2`, … | [HTTP spans v1.44.0](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/http/http-spans.md) |
| Failed operation | `error.type="503"` on failed HTTP attempts/exhausted operations; `"_OTHER"` on rejected workflow | [Error registry v1.44.0](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/registry/attributes/error.md) |

[Recording errors v1.44.0](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/general/recording-errors.md)
(Development guidance) supports leaving recovered enclosing operations successful while retaining
failed attempt spans. No `approval.outcome`, `retry.count`, generic `workflow.name`, CI/CD or GenAI
attribute is borrowed. Existing app resource identity and request correlation remain unchanged.
Authoritative signal contracts are the trace/APM areas in the [signal catalogue](../signal-areas.md).

## Routing test

Assign platform controllers `namespace: platform-automation` and user services
`namespace: user-workloads`. Existing app identity stamps these values on the **resource**
`service.namespace`, not on the individual span.

Test exclusive collector routing using these resource predicates:

- Platform destination: `service.namespace == "platform-automation"`.
- User workload destination: `service.namespace == "user-workloads"`.
- Missing or unmatched classifier: explicitly configured fallback/quarantine, never silently platform.

`TestAutomationResourceRouting` captures emitted resources from both a real automation app and an
ordinary app, evaluates these predicates and verifies exclusive classification. This proves emitted
routing inputs, **not a running collector or live destination**. To verify an operator's collector,
feed both trace groups into its local receiver and confirm each destination receives only its selected
resource group; test the unmatched policy separately. No stack credentials are part of the fixture.

## Span-derived reporting

The existing default-off `EmitSpanMetrics` opt-in controls self-emission of
`traces_spanmetrics_calls_total`, `traces_spanmetrics_size_total` and dual native/classic
`traces_spanmetrics_latency`. Otherwise the chosen real producer derives metrics from the trace stream.
Automation self-emission observes actual ledger requests, including every executed approval, task and
attempt; it does not independently round a low RPS expectation. Calls and histogram observations are
cumulative. Repeated/accelerated ticks do not recount an observed request; skipped steps and unused
retry slots produce no observations. No remote service-graph edge is fabricated without an instrumented
remote SERVER span. API URLs, target addresses, request IDs and retry ordinals never become labels.

For long-term reporting, retain INTERNAL spans and span-name dimensions in the producer. The reference
collector-side spanmetrics prefilter skips INTERNAL spans by default, and a connector configured to
exclude span names cannot report individual workflow/task/approval names. Disable that filter and
retain those dimensions rather than falsifying span kinds. Producer A/B differences, captured bucket
bounds and the synthetic size model remain governed by the existing APM contract.

The observation window is initially 60 seconds and thereafter its unobserved part. Ledger retention
and capacity bound recovery; this is not durable observation replay after a long outage. Long backdated
operations also require appropriate deployment-side metrics-generator ingestion windows.

## Production volume assumptions

These are illustrative estate assumptions admitted for this blueprint, not measured universal rates.
Use both traffic endpoints equal to the flat approximation; a sparse tick with no trace is normal.

| Workflow | Planning basis per site | Flat runs/second |
|---|---|---:|
| Node provisioning | Four runs/day | `4/86400` |
| Firmware rollout | One wave/week | `1/604800` |
| Drain/return | Six runs/day for several hundred nodes | `6/86400` |
| Storage provisioning | Four allocation requests/day | `4/86400` |
| Self-service portal | Twelve infrastructure requests/day | `12/86400` |

Three attempts model an initial send plus at most two transient-error resends. Low failure/rejection
probabilities are explicitly illustrative policy assumptions, not captured vendor failure rates.
Never increase shipped rates to force an example into one tick.

## Offline acceptance surface

Stage the fixture as `custom/loop47__ai-factory-automation.yaml` under an isolated
`BLUEPRINT_DATA_DIR`. Select `loop47/ai-factory-automation` and use an isolated snapshot with
`volume_multiplier: 1`, `span_metrics_blueprints: ["loop47/ai-factory-automation"]` and
`blueprint_sources: []`. Run:

```bash
DRY_RUN=true SELFOBS_ENABLED=false \
BLUEPRINT_NAMES=loop47/ai-factory-automation \
BLUEPRINT_DATA_DIR=/absolute/scratch/blueprints \
CONFIG_SNAPSHOT_PATH=/absolute/scratch/control-state.json \
go run ./cmd/synthkit -env /dev/null -once -dump
```

Dump is key/name inventory only: compare it with the sourced trace/APM contracts, but do not count
it as proof of parentage, statuses, timing or routing values. Public-boundary capture tests supply
that complementary proof. With a one-second approval and 100-ms attempts, the four representative
node-provisioning cases have root durations `1202`, `1302`, `1302` and `1002` milliseconds for approved,
recovered, exhausted and rejected execution respectively.
