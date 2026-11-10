# A stock SynthKit demo in 7-10 minutes

Use the shipped `otlp-native` and `dbo11y-mysql` examples to demonstrate the path from a local
Docker command to Grafana's native apps. Complete the [private preparation](README.md) first:
image pulled, isolated state writable, destination and token in the ignored `.env`, preflight
successful, live delivery enabled, and required app setup approved. Start with the emitter stopped.

The 9:30 allocation below leaves time for navigation and ingestion. Rehearse your spoken pace.
The environment produces synthetic telemetry; it does not deploy the systems being modelled.

## Prepare the shared screen

Use a terminal in this directory and a dedicated browser window. Keep credential editors and
unrelated windows closed. To explain the actual `.env` configuration, show only known non-secret
settings and a masked token line:

```bash
awk -F= '
/^(SYNTHKIT_IMAGE_REF|BLUEPRINT_NAMES|DRY_RUN|SYNTHKIT_BIND|GC_PROM_RW|GC_PROM_USER|GC_OTLP_ENDPOINT|GC_OTLP_USER|GC_LOKI|GC_LOKI_USER)=/ { print }
/^GC_TOKEN=/ { print "GC_TOKEN=<hidden>" }
' .env
```

Open the local console and your own Grafana sandbox's Kubernetes workloads, Database queries,
Application services and Loki/Tempo Explore tabs. The service to focus is `otlp-api-enriched`,
namespace `otlp-api-enriched`, cluster `otlp-native-prod-euw1`. The independent database is
`dbo11y-mysql-app`, with schema `app`.

Refresh relative-time views when you arrive; a page opened before startup can retain an old end
time. If Kubernetes Home was opened before data arrived, confirm fresh metrics after warmup, then
hard reload that same Cloud page if it still shows onboarding. A time-picker change alone may not
clear its cached discovery result. Use the
[cold-open guidance](../docs/troubleshooting.md#native-app-view-stays-empty-after-data-arrives)
to distinguish this from missing telemetry. A reused sandbox can have earlier entities and samples.
Do not present this as an empty-tenant discovery benchmark, and label any rehearsal capture by its
actual time.

## The 9:30 route

| Time | Screen | Action |
| --- | --- | --- |
| 0:00-0:30 | Terminal | Start with `docker compose up -d`. |
| 0:30-2:30 | SynthKit UI | Readiness, two stock blueprints, YAML and scoped HTTP error injection. |
| 2:30-3:30 | Kubernetes Monitoring | Stock workload readiness, CPU, memory and pods. |
| 3:30-5:00 | Database Observability | Stock MySQL queries, performance and sample views. |
| 5:00-6:30 | Application Observability | Stock service RED overview and traces. |
| 6:30-8:15 | Logs and traces | Failed HTTP request log → its exact trace ID. |
| 8:15-8:45 | SynthKit UI | Disable the injected fault. |
| 8:45-9:30 | Quick Start | Docker entry point, bundled skills and reuse. |

### 0:00 - Start locally

From this prepared quick-start directory, run `docker compose up -d`, then `docker compose ps`.

> This is the same release and the same example blueprints an SE gets from the project. I've configured the lab credentials and pulled the image before sharing. Docker reads the private configuration, so the on-screen start is just Compose up.
>
> The value is being able to prepare a useful PoV before customer telemetry is available. SynthKit emits synthetic telemetry; it doesn't deploy a real cluster, application or database.

### 0:30 - Show SynthKit while data flows

Open the console. Show **Ready**, **2 loaded / 2 active**, writable state and successful sinks. A starting container can take a collection interval to reach Ready. Local delivery success is separate from the received-data check in Grafana.

Click **otlp-native** and **View blueprint YAML source**. Point to the two services and their `otel.mode` declarations. Then show **dbo11y-mysql**, its one database and `observability.dbo11y: true`. Keep the shipped names and rates unchanged.

> The blueprint is the model: supported infrastructure, workloads and how their signals are composed. These examples are already included in the image. I can start here, see what they emit, and then tailor supported declarations for an account.
>
> The first example compares a Kubernetes-enriched service with an SDK-only service. The second demonstrates the database experience. They are separate examples; we haven't invented a call between them.

Open **Incidents**. In the **otlp-native** section's **Scoped failure injection** form:

1. Choose `error_burst` as the failure mode.
2. Choose `otlp-api-enriched` as the target.
3. Set intensity to `1.00` and click **Inject**.
4. Confirm **Inject failure** in the local confirmation and verify the active failure shows `error_burst · otlp-api-enriched · 1.00`.

There is no custom curated scenario in this stock blueprint. Use its normal live controls, not the global unscoped form or the database section.

> I'll introduce HTTP errors in the enriched service. The second service remains our comparison. This is a normal runtime control; the blueprint on disk stays unchanged.

### 2:30 - Show native Kubernetes Monitoring

In Kubernetes workloads, find `otlp-api-enriched`, namespace `otlp-api-enriched`, cluster `otlp-native-prod-euw1`. Open that workload. Show ready replicas, pod count, CPU and memory usage, requests and limits. Scroll past the beta assistant summary to the measured panels.

> The stock example gives us the Kubernetes context in Grafana's own app. We can see the replicas and resource usage without first building a custom dashboard.

The sandbox needs its standard Kubernetes alert and recording-rule backend installed during preparation. Rule evaluation still needs fresh samples after startup. If a panel is warming, show the populated inventory and continue. After a live scale change, navigate afresh to the workload before comparing readiness cards and the pod table; a retained page may show older counts. Keep cost, profiling and beta assistant diagnosis outside this route unless separately verified.

### 3:30 - Show Database Observability

Open Database **Queries overview** or the prepared instance detail. Find the stock `dbo11y-mysql-app` instance, schema **app**. Its native identity is a synthetic RDS-shaped connection string; it is not a real database to connect to.

> This is the second shipped blueprint. It supplies database telemetry: query activity, samples, waits and table information. There is no database server running on my laptop; we're using this to prepare and rehearse the native database experience.

Use **Queries overview** to show the populated instance/schema/table counts, query rate, wait categories and normalized SQL catalogue. Then open `SELECT * FROM users WHERE id = ?` and its **Query performance** tab. Click **Show full query** to expose the complete normalized statement, then point to duration, rate and rows. Prepare this view in a browser tab before presenting.

If there is time, **Query samples** shows sample points and an average-duration curve. **Table schema details** shows the `users` columns and primary index.

Keep **Explain plan** and full table DDL outside this timed route until separately validated in your stack. These deeper views were not reliable in the reference validation. The normalized SQL, sample durations and waits are synthetic; they are not results of executing queries against a live database. Locks are normally empty without a separate lock-contention control. Do not promise AI recommendations, PostgreSQL causality or an HTTP-to-MySQL join.

### 5:00 - Show Application Observability

Open Services, search `otlp-`, and select **otlp-api-enriched** with namespace `otlp-api-enriched`. Refresh its recent time range, then show **Overview**: duration, errors and rate. The error injection should make the error story visible after collection and backend aggregation.

> Now we're back in the application experience. These are Grafana's native service views. The controlled error gives us something repeatable to investigate, while the other stock service provides a comparison.

Click **Traces**, which retains the service/namespace filters. Open a recent HTTP request. The stock example has no declared database call, so show its HTTP span honestly.

Optional: use the prepared Entity Graph to show the discovered stock service identity. Click the `prod·otlp-api-enriched` group and choose **Only** to focus that identity. Do not add manual edges or promise a dependency graph the stock blueprint does not declare. Skip this if behind time.

### 6:30 - Join a failed log to its trace

In the same Application Observability service, open **Logs**, select **New Format**, and run the
query over a recent window. Expand a fresh `request failed` record whose JSON body has numeric
`status: 500`, then copy its `trace_id`. Use a record after this run's fault activation. The
namespace and service filters stay attached to the native view.

In Tempo Explore choose **Trace ID**, paste the value and click **Run query**. Use a slightly
older failed record if the newest trace is still ingesting. The HTTP root span should show the
same service and status 500.

For an older image or while the native view is warming, use Loki Explore with the lab's Loki
datasource and the same recent window. The exact stock fallback query is:

```logql
{source="app",service_name="otlp-api-enriched",cluster="otlp-native-prod-euw1",level="error"}
| json demo_msg="msg", demo_status="status"
| demo_msg="request failed"
| demo_status == 500
| __error__=""
```

The fallback follows the same exact-ID join; it does not substitute a service/time search for the returned trace.

> This is the exact failed synthetic request across logs and traces. It lets us rehearse the investigation and the explanation before customer access is ready. A service name and time window narrow the search; the trace ID makes the join precise.

A generic error-level record can be a 429, so choose the numeric status-500 detail record. Use Application Observability **Logs / New Format** only after the exact deployed image's fresh logs have been verified with matching `service_name` and `service_namespace`; see the [log identity prerequisite](../docs/troubleshooting.md#application-logs-new-format-is-empty). Otherwise keep the Explore route above. These app logs use the Loki sink; native OTLP HTTP metrics and traces do not imply an OTLP app-log exporter. Native HTTP metrics have no trace exemplars here. Keep this timed demo about errors; rehearse a latency control separately only after its emitted metrics and traces have been verified on the deployed image.

### 8:15 - Recover

Return to the **otlp-native** Incidents section. Under **Active failures here**, disable `error_burst · otlp-api-enriched`. Confirm the injected failure is no longer active. Other retained controls, such as scale or volume, may keep the posture at `MOD`.

> The fault is off. New requests return to the baseline probability. Rolling metric windows retain the bad period for a while, so we don't wait for the entire graph to recover on this call.

### 8:45 - Give SEs the starting point

Open the [setup instructions](README.md) or [canonical Quick Start](../docs/quickstart.md).

> Start with a shipped blueprint and a sandbox. The Quick Start covers Docker Compose, private configuration, an offline preview, preflight and checking the received data. The normal Docker path needs no host Go, Node, just or gcx.
>
> If you use an agent, the initial-setup, create-blueprint and verify-deployment skills follow the same process. Their references are bundled in the checkout, so a separate documentation-query plugin isn't required.
>
> Start stock, understand the signals, then tailor the supported model for the account. The customer's systems still need their own instrumentation and collection.

At eight minutes, skip remaining optional drilldowns, disable the fault and close with the Quick Start. Stop after questions:

```bash
docker compose stop
```

## If the live view is slow

- If readiness or preflight fails, stop the emitter and use clearly labelled rehearsal captures.
- If a rate panel is empty, allow multiple one-minute samples and refresh the time range. Continue
  with populated Kubernetes inventory or received logs while aggregation catches up.
- If fresh Kubernetes metrics exist but Home still shows onboarding, hard reload the Cloud page.
  For stale post-scale cards, navigate afresh to the workload before comparing its counts.
- If a log's trace is absent, choose a slightly older failed request from the same run. Do not claim
  correlation until the exact trace opens.
- If behind time, skip optional graph and schema drilldowns, disable the fault and close with setup.

## Terminal recovery controls

These loopback-only examples are the API equivalent of the named stock service's UI controls:

```bash
curl -fsS -X POST http://127.0.0.1:8088/control/failures \
  -H 'Content-Type: application/json' \
  -d '{"error_burst":{"enabled":true,"intensity":1,"scope":"otlp-api-enriched"}}'

curl -fsS -X POST http://127.0.0.1:8088/control/failures \
  -H 'Content-Type: application/json' \
  -d '{"error_burst":{"enabled":false,"intensity":1,"scope":"otlp-api-enriched"}}'
```

The error control changes HTTP request outcomes. It does not establish database-side causality.
The two stock blueprints remain independent, and their source files stay unchanged throughout.
