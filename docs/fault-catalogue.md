---
title: Fault Catalogue
description: The eight causal fault modes on the resource axis, the resource kind each acts on, the story it models and a complete blueprint that loads.
---

# Fault Catalogue

A causal fault changes the state of a declared resource, and every signal that depends on that
resource follows. This page lists the eight fault modes on the `resource` axis, the kind of
resource each one acts on, the production story it models and a minimal blueprint for each. Read
[Declaring causes](blueprints.md#declaring-causes) first for the model and a worked example.

Every YAML block on this page is a complete blueprint. A test loads each one through the real
loader, so the names, kinds and modes are checked, but the rates are small illustrations sized for
the arithmetic beside them. A shipped blueprint states the real-world basis for every size, hold
time and rate it declares.

| Mode | Acts on a `kind:` | Story | Effect at intensity `i` |
|---|---|---|---|
| [`pool_exhausted`](#pool_exhausted) | `pool` | A service loses connections from its pool (a leak, a lowered maximum). | Effective size is `size x (1 - i)`. |
| [`concurrency_reduced`](#concurrency_reduced) | `concurrency` | A function's reserved concurrency is cut, or the account is squeezed. | Effective limit is `size x (1 - i)`. |
| [`quota_reduced`](#quota_reduced) | `quota` | A provider tightens a rate limit on a shared API. | Effective limit is `limit_rps x (1 - i)`. |
| [`consumer_slowdown`](#consumer_slowdown) | `queue` | Consumers fall behind and the backlog ages. | Service rate is multiplied by `1 - i`. |
| [`dependency_slow`](#dependency_slow) | `dependency` | A downstream system gets slow (lock contention, a cold cache). | Latency factor is `1 + (slow_factor - 1) x i`. |
| [`dependency_unavailable`](#dependency_unavailable) | `dependency` | A downstream system refuses connections. | A share `i` of calls fail to connect. |
| [`credential_expired`](#credential_expired) | `credential` | A certificate or token for a dependency expires. | A share `i` of calls to the dependency fail authentication. |
| [`rule_regression`](#rule_regression) | `rule` | A validation rule or a bad deploy starts rejecting good records. | A share `i x reject_share` of arrivals fail. |

`intensity` is in `[0, 1]` and defaults to 1 when omitted. `pool_exhausted` at 1 empties the pool
and `dependency_slow` at 1 applies the full `slow_factor`; most stories read better at a fractional
intensity.

## How the effects compose

- **The cause sits on one resource and the symptom appears elsewhere.** `dependency_slow` is
  declared on the dependency. A pool, concurrency limit or queue that lists it in `held_across`
  holds each unit of work longer, so its utilization rises, and the exhaustion that follows is
  emergent. Nothing declares the pool as faulty.
- **Capacity resources reject or wait.** A pool waits (up to `acquire_timeout_ms`) and then
  rejects with a 503 by default. A concurrency limit and a quota reject at once with a 429. A queue
  stores the excess and ages it, and rejects only past a `max_depth`.
- **Dependencies and credentials fail transport.** Their failures carry no HTTP status of their
  own; the caller sees a connection or handshake error, and its client policy decides whether to
  resend (`call_policies`).
- **A rule fails the record.** It rejects with a 422 unless `error.status` says otherwise.
- **Resending is bounded.** A caller's `retries` (at most 3) add demand to the resource they hit.
  Credential, rule and 4xx failures are never resent.
- **Recovery is immediate for memoryless resources.** Pools, concurrency limits and quotas return to
  baseline when the incident ends. A queue drains over time, at the rate its consumers can sustain
  above the arrival rate.

## `pool_exhausted`

**Resource kind:** `pool`. **Story:** a service's database connection pool loses connections, for
example from a leak or a lowered maximum, so requests queue for a connection and then time out.

At intensity 0.6 the pool below drops from 10 connections to 4. At the 100 rps peak the offered load
is `100 x 0.050 = 5` busy connections, so utilization is `5 / 4 = 1.25` and `1 - 1 / 1.25 = 20%` of
arrivals are rejected after the acquire timeout.

```yaml
name: fault-pool-exhausted
environments:
  - name: prod
    resources:
      - name: checkout-db-pool
        kind: pool
        size: 10                     # connections
        hold_ms: 50                  # one transaction holds a connection for 50 ms
        acquire_timeout_ms: 2000     # a caller waits this long for a connection, then fails
        demand: { off_peak_rps: 10, peak_rps: 100 }   # 100 x 0.050 = 5 busy connections at peak
scenarios:
  - name: pool-leak
    effects:
      - { mode: pool_exhausted, target: checkout-db-pool, intensity: 0.6 }
incidents:
  - { scenario: pool-leak, at: "10:30", for: 30m }
```

## `concurrency_reduced`

**Resource kind:** `concurrency`. **Story:** a function's reserved concurrency is cut. Requests above
the new limit are throttled at once with a 429, and the executions that were admitted carry on.

At intensity 0.7 the limit below falls from 20 to 6. At the 15 rps peak the offered load is
`15 x 0.9 = 13.5` concurrent executions, so `1 - 6 / 13.5 = 56%` of invocations are throttled.
A function declared under `cloud.lambda.functions` binds to this kind with `resource:` and then
emits its throttles, invocations and concurrency from the same state.

```yaml
name: fault-concurrency-reduced
environments:
  - name: prod
    resources:
      - name: enrich-fn-concurrency
        kind: concurrency
        size: 20                     # reserved concurrency
        hold_ms: 900                 # one execution runs for 900 ms
        demand: { off_peak_rps: 2, peak_rps: 15 }   # 15 x 0.9 = 13.5 of 20 at peak
        error: { status: 429, type: TooManyRequestsException, message: "Rate Exceeded." }
scenarios:
  - name: concurrency-cut
    effects:
      - { mode: concurrency_reduced, target: enrich-fn-concurrency, intensity: 0.7 }
incidents:
  - { scenario: concurrency-cut, at: "10:30", for: 20m }
```

## `quota_reduced`

**Resource kind:** `quota`. **Story:** a provider tightens the rate limit on an API that many
callers share. Calls above the new limit are throttled with a 429, and the callers' retry policy
decides whether the rejected calls come back as extra demand.

At intensity 0.5 the quota below falls from 80 to 40 requests per second. With a 60 rps peak,
`1 - 40 / 60 = 33%` of calls are rejected.

```yaml
name: fault-quota-reduced
environments:
  - name: prod
    resources:
      - name: partner-api-quota
        kind: quota
        limit_rps: 80                # admitted requests per second
        demand: { off_peak_rps: 5, peak_rps: 60 }
scenarios:
  - name: quota-tightened
    effects:
      - { mode: quota_reduced, target: partner-api-quota, intensity: 0.5 }
incidents:
  - { scenario: quota-tightened, at: "10:30", for: 30m }
```

## `consumer_slowdown`

**Resource kind:** `queue`. **Story:** a queue's consumers slow down. Arrivals carry on, so the
backlog grows and its oldest message ages, and both drain after the fault ends.

The queue below serves `4 / 0.4 s = 10` messages per second at baseline. At intensity 0.5 it serves
5, so against the 8 msg/s peak the backlog grows by 3 messages per second. A queue bound with
`resource:` on `cloud.sqs.queues` or an `integrations.solace` queue emits its visible messages,
oldest age (SQS) or spool usage (Solace) from this state.

```yaml
name: fault-consumer-slowdown
environments:
  - name: prod
    resources:
      - name: ingest-queue
        kind: queue
        size: 4                      # concurrent consumers
        hold_ms: 400                 # each message takes 400 ms: 4 / 0.4 s = 10 msg/s
        demand: { off_peak_rps: 1, peak_rps: 8 }
scenarios:
  - name: consumers-behind
    effects:
      - { mode: consumer_slowdown, target: ingest-queue, intensity: 0.5 }
incidents:
  - { scenario: consumers-behind, at: "10:30", for: 40m }
```

## `dependency_slow`

**Resource kind:** `dependency`. **Story:** a downstream system gets slower, for example from lock
contention. Every node that calls it takes longer, and every pool, concurrency limit or queue that
holds a unit of work across it holds that unit longer.

The effective hold is `hold_ms + latency_ms x (factor - 1)`. In the example the validator goes from
350 ms to 1050 ms at intensity 1, so each message holds a consumer for `400 + 350 x 2 = 1100 ms`,
the queue serves `4 / 1.1 = 3.6` msg/s and the 8 msg/s peak builds a backlog. The queue declares no
fault of its own. The worked example in [Declaring causes](blueprints.md#declaring-causes) does the
same to a connection pool.

```yaml
name: fault-dependency-slow
environments:
  - name: prod
    resources:
      - name: validation-queue
        kind: queue
        size: 4
        hold_ms: 400                 # includes the 350 ms validator call
        held_across: external-validator
        demand: { off_peak_rps: 1, peak_rps: 8 }
      - name: external-validator
        kind: dependency
        latency_ms: 350
        slow_factor: 3               # 350 ms becomes 1050 ms at intensity 1
scenarios:
  - name: validator-slowdown
    effects:
      - { mode: dependency_slow, target: external-validator }
incidents:
  - { scenario: validator-slowdown, at: "10:30", for: 40m }
```

## `dependency_unavailable`

**Resource kind:** `dependency`. **Story:** a downstream system refuses connections. A share `i` of
the calls to it fail to connect, with no HTTP status, and the work that was held across it fails
with them.

At intensity 0.5 half the executions below fail. A bound Lambda function reports them as errors; an
`app` caller sees a connection failure and answers 502 unless a `call_policies` entry with
`retry_on: [connect]` resends it.

```yaml
name: fault-dependency-unavailable
environments:
  - name: prod
    resources:
      - name: enrich-fn-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        held_across: partner-api
        demand: { off_peak_rps: 2, peak_rps: 15 }
      - { name: partner-api, kind: dependency, latency_ms: 120 }
scenarios:
  - name: partner-refusing
    effects:
      - { mode: dependency_unavailable, target: partner-api, intensity: 0.5 }
incidents:
  - { scenario: partner-refusing, at: "10:30", for: 15m }
```

## `credential_expired`

**Resource kind:** `credential`. **Story:** the certificate or token a caller uses for a dependency
expires. A share `i` of calls to that dependency fail authentication: a server or client
certificate fails the handshake with no HTTP status, and a token is refused with a 401.

A `credential` names the dependency it authenticates with `of:` and a `credential_type`. The error
class and message are values you choose; the catalogue ships none.

```yaml
name: fault-credential-expired
environments:
  - name: prod
    resources:
      - { name: partner-api, kind: dependency, latency_ms: 120 }
      - name: partner-api-cert
        kind: credential
        of: partner-api
        credential_type: server_certificate   # or client_certificate, or token
        error:
          type: SSLHandshakeException
          message: "partner-api: PKIX path validation failed: certificate expired"
scenarios:
  - name: partner-cert-expired
    effects:
      - { mode: credential_expired, target: partner-api-cert }
incidents:
  - { scenario: partner-cert-expired, at: "10:30", for: 20m }
```

## `rule_regression`

**Resource kind:** `rule`. **Story:** a data-quality rule regresses and rejects records it used to
accept. At full intensity a share `reject_share` of arrivals fail with a 422. The rule's `error`
block carries the message.

```yaml
name: fault-rule-regression
environments:
  - name: prod
    resources:
      - name: completeness-rule
        kind: rule
        reject_share: 0.35           # share of records a fully regressed rule rejects
        error: { status: 422, message: "rule completeness rejected record: mandatory field missing" }
scenarios:
  - name: completeness-regression
    effects:
      - { mode: rule_regression, target: completeness-rule }
incidents:
  - { scenario: completeness-regression, at: "10:30", for: 30m }
```

The same kind models a bad deploy local to one service: a rule whose `error.status` is 500 rejects
a share of that service's requests with a server error, and nothing downstream of it is involved.

```yaml
name: fault-bad-deploy
environments:
  - name: prod
    resources:
      - name: bad-deploy
        kind: rule
        reject_share: 0.2            # a faulty release fails a fifth of requests at full intensity
        error: { status: 500, type: NullPointerException, message: "unhandled error in the new release" }
scenarios:
  - name: release-regression
    effects:
      - { mode: rule_regression, target: bad-deploy }
incidents:
  - { scenario: release-regression, at: "10:30", for: 25m }
```

## Binding a resource to something that shows it

A resource on its own is a declaration. Something has to meet it before a signal moves.

| Consumer | How it meets a resource |
|---|---|
| `app` workload | A service node lists `uses: [name, ...]`. Capacity resources and rules attach to the node a request arrives at; dependencies and credentials attach to a node something calls. |
| Lambda function | `cloud.lambda.functions[].resource` names a `concurrency` resource. |
| SQS queue | `cloud.sqs.queues[].resource` names a `queue` resource. |
| Solace queue | `integrations.solace.brokers[].vpns[].queues[].resource` names a `queue` resource. |
| RDS or DocumentDB instance | `databases[].resource` names a `dependency` or `pool` resource. |

A bound construct reads the same model state as every other consumer, so one declared cause moves
the application's status codes, durations, backlog and the cloud metrics together. The
[incident reference](incidents.md) lists what each bound construct emits.

## Activation

A fault on the `resource` axis activates like any other: an entry in `incidents:`, a scenario
referenced from an incident, or live through the control plane. A target is a declared resource
name. A resource named `<base>-<environment>` is also reached by `<base>` in every environment, and
`resource:*` covers every declared resource. The mode name decides the axis, so a resource that
shares its name with a service node or a database is still reached by its own modes.
