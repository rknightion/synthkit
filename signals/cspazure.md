# CSP Azure (→ Mimir) — ScopeSubstrate

Azure Monitor metrics emitted via the `cloud_azure` construct. Substrate-scoped: disambiguated by
`subscriptionID` + `resourceID`; never carries a `blueprint` label. Two distinct ingestion paths
exist with different label shapes — see the dual-path note in the overview section below. Global
rules: see [`00-canon.md`](00-canon.md).

---

## Overview — window-gauge invariant + dual ingestion paths [slug: cspazure]

*Provenance: predecessor SIGNALS §13 Lane C + `emit/cloud_azure.go`+`cloud_shared.go`.*
Feature `cloud_azure`; sub-signals `compute,databases,storage,networking,messaging,logs`. NO blueprint
label. `job="integrations/azure_exporter"` on every series.

> ⚠ **Window-gauge invariant (CRITICAL):** EVERY Azure metric is `st.Set`, NEVER `st.Add` — even
> names ending `_total_count` (the `_total` fragment is part of the Azure metric NAME, not a counter
> suffix). Azure Monitor delivers per-`PT5M`-window totals; making them monotonic breaks
> `increase()`/`rate()`.

Base labels (`azureBaseLabels`): `job`, `resourceID` (full ARM path, ⚠ **fully lowercased** — last
segment must equal `resourceName`; mixed-case silently breaks the app's `label_replace`),
`subscriptionID`, `subscriptionName`, `resourceGroup`, `resourceName`, `interval="PT1M"`,
`timespan="PT5M"`, `owner` (tag).

> ✅ **Dimension-label convention — SK-16 FULLY RESOLVED + §2.5 RE-AUDIT (live capture of BOTH paths 2026-06-14).**
> Azure Monitor metrics reach Mimir via **two distinct ingestion paths that label dimensions DIFFERENTLY** —
> a blueprint may use either (serverless preferred):
> • **Serverless `cloud/azure`** (GC cloud-provider integration; `job="cloud/azure/microsoft-<...>"`,
>   `credential`, `instance=<resourceID>`, `region`): per-dimension labels ride as **`dimension_<AzureName>`**
>   — **UNDERSCORE** separator, Azure CamelCase preserved (`dimension_EntityName`, `dimension_Endpoint`,
>   `dimension_Tier`, …). This is the form synthkit currently emits.
> • **azure_exporter** (`prometheus.exporter.azure`; `job="integrations/azure"`, `resourceID`/`resourceName`/
>   `subscriptionID`/`subscriptionName`/`resourceGroup`/`instance=<opaque hash>`/`interval="PT1M"`/`timespan="PT1M"`):
>   a **single** requested dimension is the bare label **`dimension="<value>"`**; **multiple** dimensions are
>   **`dimension<Name>`** with **NO underscore** (live: `dimensionEndpoint="<fqdn>"`, `dimensionHttpStatusGroup="2XX"|"4XX"`).
>   ⚠ The prior 2026-06-13 note that called the underscore form an "azure_exporter capture" was a **conflation** —
>   that data was the serverless path; the real azure_exporter uses NO underscore (the predecessor's no-underscore
>   camelCase form was actually closer to azure_exporter reality).
> **§2.5 audit deltas vs synthkit's `azureBaseLabels`:** synthkit sets `job="integrations/azure_exporter"`
> (real exporter = `integrations/azure`; serverless = `cloud/azure/<...>`) and `timespan="PT5M"` (real = `"PT1M"`);
> `interval="PT1M"` matches. synthkit's `dimension_<Name>` matches the SERVERLESS path (the preferred one), so the
> dimension form is fine for serverless blueprints but does NOT match an azure_exporter blueprint — a construct
> decision deferred (see cantfind notes). Front Door live dims: `dimensionEndpoint`(fqdn)+`dimensionHttpStatusGroup`
> ∈{2XX,3XX,4XX,5XX} (exporter) / `dimension_Endpoint`+`dimension_HttpStatusGroup`+`dimension_ClientCountry` (serverless).
> Service Bus: per-entity metrics (`messages`,`activemessages`,`size`) carry the entity name — `dimension="<queue|topic>"`
> (exporter single) / `dimension_EntityName` (serverless). SQL elastic-pool: **NO `elastic_pool_name` label** —
> identity is `resourceID` (path `…/servers/<server>/elasticpools/<pool>`) + `resourceName` = **`<server>`** (azure_exporter)
> or **`<server>/<pool>`** (serverless). Azure SQL DB has NO `database` dimension — identity is `resourceName=<db>`
> (azure_exporter) / **`<server>/<db>`** (serverless) + the nested `resourceID`.
> ✅ **Serverless `cloud/azure` path FULLY captured 2026-06-14 (managed scraper, new resources Front Door + Service Bus).**
> Identity labels: `credential="<scraper-cred-name>"` (e.g. `ps_azure`) — **ALWAYS present on the serverless path,
> named after the managed-scraper credential** (the azure_exporter path has NO `credential` label); `job="cloud/azure/microsoft-<provider>-<type>"`;
> `instance` = the **full ARM `resourceID` with original casing preserved** (`/subscriptions/…/providers/Microsoft.ServiceBus/namespaces/<n>`),
> and `resourceID` likewise **PascalCase-preserved** — ⚠ contrast the azure_exporter path which **lowercases** `resourceID`
> and uses an **opaque hash** `instance`; plus `region`, `resourceGroup`, `resourceName`, `subscriptionID`, `subscriptionName`
> (NO `interval`/`timespan` — those are azure_exporter-only). Front Door (`microsoft-cdn-profiles`) serverless dims (all
> underscore): `dimension_Endpoint`(fqdn), `dimension_HttpStatusGroup` (⚠ **lowercase `2xx`/`4xx`** — azure_exporter emits
> **uppercase `2XX`/`4XX`**), `dimension_HttpStatus`(`200`/`404`), `dimension_ClientCountry`(`united kingdom`),
> `dimension_ClientRegion`(`emea`), `dimension_Origin`(`example.com:443`), `dimension_OriginGroup`. Service Bus per-entity
> metrics: `dimension_EntityName="<queue|topic>"`.
> ✅ **§2.5 RE-VALIDATED against a long-running managed scraper (~33 resource types, 2026-06-14) — supersedes the earlier this-session capture where noted:**
> • **resourceID casing CONFIRMED** — `/subscriptions/<id>/resourceGroups/<rg>/providers/<Namespace>/<type>/<name>`:
>   `resourceGroups` (capital G), provider namespace PascalCase (`Microsoft.Compute`, `Microsoft.Sql`, `Microsoft.Storage`,
>   `Microsoft.DBforPostgreSQL`, `Microsoft.Network`, `Microsoft.EventHub`, `Microsoft.ServiceBus`, `Microsoft.Cdn`),
>   type token camelCase (`virtualMachines`, `storageAccounts`, `flexibleServers`, `loadBalancers`, `virtualNetworks`)
>   — **EXCEPT `elasticpools` which is LOWERCASE** (`Microsoft.Sql/servers/<srv>/elasticpools/<pool>`). `instance` == this
>   full `resourceID`. job slug = `cloud/azure/` + provider/type tokens hyphen-joined, lowercased
>   (`microsoft-sql-servers-elasticpools`, `microsoft-storage-storageaccounts-blobservices`).
> • **serverless `resourceName` = the resource NAME segments joined by `/`** (NOT the bare last segment): flat resources →
>   the name (`vm-app-01`); nested → `<parent>/<child>` — SQL DB `<server>/<db>`, **elastic pool `<server>/<pool>`**
>   (⚠ CORRECTS the prior "serverless carries NO resourceName" note — it DOES; the line-1027 `<server>/<pool>` form was right).
> • **Storage `resourceID` is ACCOUNT-ONLY on serverless** — `…/Microsoft.Storage/storageAccounts/<account>` (NO
>   `/blobServices/default` suffix); the blob/queue/file/table namespace lives in the JOB slug + metric name only. `resourceName`=account.
> • **`credential` ALWAYS present on serverless**, named after the managed-scraper credential; azure_exporter has none.
> • **Resource tags → `tag_<tagname>` labels are OPT-IN**: the DEFAULT managed scraper surfaces **NO `tag_*` labels**
>   (live-confirmed: long-running scraper series carry none) — they appear only when the scraper's `tags` setting
>   (serverless) / `included_resource_tags` (azure_exporter) is configured. Use lowercase CAF keys (`app, env, owner,
>   costcenter, businessunit, criticality, managedby`) for cross-cloud consistency. `target_info` carries the full tag set regardless.

⚠ Variable-picker seed metrics must carry `job`, `subscriptionName`, `resourceGroup`, `resourceName`.

---

## Compute — `azure_microsoft_compute_virtualmachines_*` ✅ [slug: cspazure-compute]

**Inventory (exact names — the contract).** Compute (`microsoft.compute/virtualmachines`):
`azure_microsoft_compute_virtualmachines_vmavailabilitymetric_average_count` (seed),
`_percentage_cpu_average_percent`, `_available_memory_bytes_average_bytes`,
`_cpu_credits_consumed_average_count`, `_cpu_credits_remaining_average_count`,
`_disk_read_bytes_total_bytes`, `_disk_write_bytes_total_bytes`,
`_disk_read_operations_sec_average_countpersecond`, `_disk_write_operations_sec_average_countpersecond`,
`_inbound_flows_average_count`, `_outbound_flows_average_count`, `_network_in_total_total_bytes`,
`_network_out_total_total_bytes`.

```yaml signals
family: azure_microsoft_compute_virtualmachines
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless path: cloud/azure/microsoft-compute-virtualmachines
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Compute/virtualMachines/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <vm-name>
  interval: PT1M
  timespan: PT5M    # azure_exporter; serverless omits interval/timespan
  owner: <tag>
  # serverless-only: credential=<scraper-cred-name>, instance=<full-ARM-resourceID>, region=<region>
metrics:
  - {root: vmavailabilitymetric_average_count, type: gauge, unit: count, v: ok, note: seed}
  - {root: percentage_cpu_average_percent, type: gauge, unit: percent, v: ok}
  - {root: available_memory_bytes_average_bytes, type: gauge, unit: bytes, v: ok}
  - {root: cpu_credits_consumed_average_count, type: gauge, unit: count, v: ok}
  - {root: cpu_credits_remaining_average_count, type: gauge, unit: count, v: ok}
  - {root: disk_read_bytes_total_bytes, type: gauge, unit: bytes, v: ok, note: "_total is part of the Azure metric name — NOT a counter suffix (window-gauge)"}
  - {root: disk_write_bytes_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: disk_read_operations_sec_average_countpersecond, type: gauge, unit: count_per_second, v: ok}
  - {root: disk_write_operations_sec_average_countpersecond, type: gauge, unit: count_per_second, v: ok}
  - {root: inbound_flows_average_count, type: gauge, unit: count, v: ok}
  - {root: outbound_flows_average_count, type: gauge, unit: count, v: ok}
  - {root: network_in_total_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: network_out_total_total_bytes, type: gauge, unit: bytes, v: ok}
```

---

## SQL — `azure_microsoft_sql_*` ✅ [slug: cspazure-sql]

SQL DB (`microsoft.sql/servers/databases`): `_connection_successful_total_count`, `_deadlock_total_count`,
`_sessions_count_average_count`, `_cpu_percent_average_percent`, `_cpu_limit_average_count`,
`_cpu_used_average_count`, `azure_microsoft_sql_servers_databases_storage_maximum_bytes` (⚠ NO
`_maximum` infix before `_bytes`), `_storage_percent_maximum_percent`, `_dtu_used_average_count`,
`_dtu_consumption_percent_average_percent`, `_dtu_limit_average_count`. Elastic Pools
(`/elasticpools`): `_allocated_data_storage_average_bytes`, `_storage_used_average_bytes`,
`_storage_limit_average_bytes`, `_cpu_percent_average_percent`, `_sql_instance_memory_percent_maximum_percent`,
`_edtu_used_average_count`, `_sessions_count_average_count`, `_allocated_data_storage_percent_average_percent`,
`_storage_percent_average_percent`.

SQL elastic-pool identity: **NO `elastic_pool_name` label** — identity is `resourceID` (path
`…/servers/<server>/elasticpools/<pool>`) + `resourceName` = **`<server>`** (azure_exporter) or
**`<server>/<pool>`** (serverless). Azure SQL DB has NO `database` dimension — identity is
`resourceName=<db>` (azure_exporter) / **`<server>/<db>`** (serverless) + the nested `resourceID`.
⚠ `elasticpools` token is **LOWERCASE** in the resourceID (`Microsoft.Sql/servers/<srv>/elasticpools/<pool>`).

```yaml signals
family: azure_microsoft_sql_servers_databases
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-sql-servers-databases
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Sql/servers/<server>/databases/<db>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: "<db>"    # azure_exporter bare name; serverless: <server>/<db>
  interval: PT1M
  timespan: PT5M
metrics:
  - {root: connection_successful_total_count, type: gauge, unit: count, v: ok}
  - {root: deadlock_total_count, type: gauge, unit: count, v: ok}
  - {root: sessions_count_average_count, type: gauge, unit: count, v: ok}
  - {root: cpu_percent_average_percent, type: gauge, unit: percent, v: ok}
  - {root: cpu_limit_average_count, type: gauge, unit: count, v: ok}
  - {root: cpu_used_average_count, type: gauge, unit: count, v: ok}
  - {root: storage_maximum_bytes, type: gauge, unit: bytes, v: ok, note: "⚠ NO _maximum infix before _bytes — full name azure_microsoft_sql_servers_databases_storage_maximum_bytes"}
  - {root: storage_percent_maximum_percent, type: gauge, unit: percent, v: ok}
  - {root: dtu_used_average_count, type: gauge, unit: count, v: ok}
  - {root: dtu_consumption_percent_average_percent, type: gauge, unit: percent, v: ok}
  - {root: dtu_limit_average_count, type: gauge, unit: count, v: ok}
```

```yaml signals
family: azure_microsoft_sql_servers_elasticpools
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-sql-servers-elasticpools
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Sql/servers/<server>/elasticpools/<pool>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: "<server>"    # azure_exporter; serverless: <server>/<pool>
  interval: PT1M
  timespan: PT5M
  # note: NO elastic_pool_name label — identity via resourceID path
metrics:
  - {root: allocated_data_storage_average_bytes, type: gauge, unit: bytes, v: ok}
  - {root: storage_used_average_bytes, type: gauge, unit: bytes, v: ok}
  - {root: storage_limit_average_bytes, type: gauge, unit: bytes, v: ok}
  - {root: cpu_percent_average_percent, type: gauge, unit: percent, v: ok}
  - {root: sql_instance_memory_percent_maximum_percent, type: gauge, unit: percent, v: ok}
  - {root: edtu_used_average_count, type: gauge, unit: count, v: ok}
  - {root: sessions_count_average_count, type: gauge, unit: count, v: ok}
  - {root: allocated_data_storage_percent_average_percent, type: gauge, unit: percent, v: ok}
  - {root: storage_percent_average_percent, type: gauge, unit: percent, v: ok}
```

---

## PostgreSQL Flexible — `azure_microsoft_dbforpostgresql_flexibleservers_*` ✅ [slug: cspazure-postgres]

PostgreSQL Flexible (`microsoft.dbforpostgresql/flexibleservers`):
`_active_connections_average_count` (seed), `_connections_succeeded_total_count`,
`azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count` (⚠ literal
DOUBLE `connections_`), `_cpu_percent_average_percent`, `_storage_used_maximum_bytes`,
`_storage_percent_maximum_percent`, `_read_iops_maximum_count`, `_write_iops_maximum_count`,
`_database_size_bytes_average_bytes`, `_storage_percent_average_percent`, `_memory_percent_average_percent`,
`_read_iops_average_count`, `_write_iops_average_count`, `_network_bytes_ingress_total_bytes`,
`_network_bytes_egress_total_bytes`, `_read_throughput_average_count`, `_write_throughput_average_count`.

```yaml signals
family: azure_microsoft_dbforpostgresql_flexibleservers
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-dbforpostgresql-flexibleservers
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.DBforPostgreSQL/flexibleServers/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <server-name>
  interval: PT1M
  timespan: PT5M
metrics:
  - {root: active_connections_average_count, type: gauge, unit: count, v: ok, note: seed}
  - {root: connections_succeeded_total_count, type: gauge, unit: count, v: ok}
  - {root: connections_connections_failed_total_count, type: gauge, unit: count, v: ok, note: "⚠ literal DOUBLE connections_ — full name azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count"}
  - {root: cpu_percent_average_percent, type: gauge, unit: percent, v: ok}
  - {root: storage_used_maximum_bytes, type: gauge, unit: bytes, v: ok}
  - {root: storage_percent_maximum_percent, type: gauge, unit: percent, v: ok}
  - {root: read_iops_maximum_count, type: gauge, unit: count, v: ok}
  - {root: write_iops_maximum_count, type: gauge, unit: count, v: ok}
  - {root: database_size_bytes_average_bytes, type: gauge, unit: bytes, v: ok}
  - {root: storage_percent_average_percent, type: gauge, unit: percent, v: ok}
  - {root: memory_percent_average_percent, type: gauge, unit: percent, v: ok}
  - {root: read_iops_average_count, type: gauge, unit: count, v: ok}
  - {root: write_iops_average_count, type: gauge, unit: count, v: ok}
  - {root: network_bytes_ingress_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: network_bytes_egress_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: read_throughput_average_count, type: gauge, unit: count, v: ok}
  - {root: write_throughput_average_count, type: gauge, unit: count, v: ok}
```

---

## Storage — `azure_microsoft_storage_storageaccounts_*` ✅ [slug: cspazure-storage]

Storage Blob (`.../blobservices`): `_containercount_average_count`, `_blobcount_average_count` (seed; ✅
SK-16: carries `dimension_BlobType="blockblob"` + `dimension_Tier` ∈{`hot,cool,transactionoptimized,untiered`}
— one series per tier; there is NO `storage_type`),
`_blobcapacity_average_bytes` (same `dimension_BlobType`/`dimension_Tier`), `_indexcapacity_average_bytes`, `_ingress_total_bytes`,
`_egress_total_bytes`, `_availability_average_percent`, `_transactions_total_count` (+
`dimension_ApiName`, `dimension_ResponseType`). Queue (`.../queueservices`): `_queuecount_average_count`
(seed), `_queuemessagecount_average_count`, `_queuecapacity_average_bytes`, `_ingress_total_bytes`,
`_egress_total_bytes`, `_availability_average_percent`, `_transactions_total_count` (+ dimension labels).

⚠ **Storage `resourceID` is ACCOUNT-ONLY on serverless** — `…/Microsoft.Storage/storageAccounts/<account>` (NO
`/blobServices/default` suffix); the blob/queue/file/table namespace lives in the JOB slug + metric name only. `resourceName`=account.

```yaml signals
family: azure_microsoft_storage_storageaccounts_blobservices
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-storage-storageaccounts-blobservices
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Storage/storageAccounts/<account>   # serverless: account-only (NO /blobServices/default suffix)
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <account>
  interval: PT1M
  timespan: PT5M
  # dimension labels (serverless underscore form):
  dimension_BlobType: blockblob    # on blobcount, blobcapacity
  dimension_Tier: hot|cool|transactionoptimized|untiered    # on blobcount, blobcapacity; NO storage_type label
  dimension_ApiName: <api-name>    # on transactions
  dimension_ResponseType: <response-type>    # on transactions
  # azure_exporter: dimensionBlobType, dimensionTier, dimensionApiName, dimensionResponseType (no underscore)
metrics:
  - {root: containercount_average_count, type: gauge, unit: count, v: ok}
  - {root: blobcount_average_count, type: gauge, unit: count, v: ok, note: "seed; SK-16 dimension_BlobType=blockblob + dimension_Tier per tier"}
  - {root: blobcapacity_average_bytes, type: gauge, unit: bytes, v: ok, note: "same dimension_BlobType/dimension_Tier as blobcount"}
  - {root: indexcapacity_average_bytes, type: gauge, unit: bytes, v: ok}
  - {root: ingress_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: egress_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: availability_average_percent, type: gauge, unit: percent, v: ok}
  - {root: transactions_total_count, type: gauge, unit: count, v: ok, note: "+ dimension_ApiName, dimension_ResponseType"}
```

```yaml signals
family: azure_microsoft_storage_storageaccounts_queueservices
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-storage-storageaccounts-queueservices
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Storage/storageAccounts/<account>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <account>
  interval: PT1M
  timespan: PT5M
metrics:
  - {root: queuecount_average_count, type: gauge, unit: count, v: ok, note: seed}
  - {root: queuemessagecount_average_count, type: gauge, unit: count, v: ok}
  - {root: queuecapacity_average_bytes, type: gauge, unit: bytes, v: ok}
  - {root: ingress_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: egress_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: availability_average_percent, type: gauge, unit: percent, v: ok}
  - {root: transactions_total_count, type: gauge, unit: count, v: ok, note: "+ dimension labels"}
```

---

## Networking — Load Balancer — `azure_microsoft_network_loadbalancers_*` ✅ [slug: cspazure-lb]

Networking — Load Balancer (`microsoft.network/loadbalancers`): `_syncount_total_count`,
`_packetcount_total_count`, `_bytecount_total_bytes`, `_snatconnectioncount_total_count`,
`_usedsnatports_average_count`, `_allocatedsnatports_average_count`.

```yaml signals
family: azure_microsoft_network_loadbalancers
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-network-loadbalancers
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Network/loadBalancers/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <lb-name>
  interval: PT1M
  timespan: PT5M
metrics:
  - {root: syncount_total_count, type: gauge, unit: count, v: ok}
  - {root: packetcount_total_count, type: gauge, unit: count, v: ok}
  - {root: bytecount_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: snatconnectioncount_total_count, type: gauge, unit: count, v: ok}
  - {root: usedsnatports_average_count, type: gauge, unit: count, v: ok}
  - {root: allocatedsnatports_average_count, type: gauge, unit: count, v: ok}
```

---

## Networking — App Gateway — `azure_microsoft_network_applicationgateways_*` ✅ [slug: cspazure-appgw]

App Gateway (`/applicationgateways`;
⚠ the predecessor's fixed `instance="integrations/azure_exporter"` here is RETIRED — `instance` is now
path-determined like every other resource: serverless=`resourceID`, azure_exporter=opaque hash):
`_totalrequests_total_count`,
`_failedrequests_total_count` (seed), `_responsestatus_total_count`, `_throughput_average_bytespersecond`,
`_applicationgatewaytotaltime_average_milliseconds`, `_currentconnections_total_count`.

```yaml signals
family: azure_microsoft_network_applicationgateways
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-network-applicationgateways
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Network/applicationGateways/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <gw-name>
  interval: PT1M
  timespan: PT5M
  # instance: serverless=<full ARM resourceID>; azure_exporter=<opaque hash> (NOT fixed string)
metrics:
  - {root: totalrequests_total_count, type: gauge, unit: count, v: ok}
  - {root: failedrequests_total_count, type: gauge, unit: count, v: ok, note: seed}
  - {root: responsestatus_total_count, type: gauge, unit: count, v: ok}
  - {root: throughput_average_bytespersecond, type: gauge, unit: bytes_per_second, v: ok}
  - {root: applicationgatewaytotaltime_average_milliseconds, type: gauge, unit: ms, v: ok}
  - {root: currentconnections_total_count, type: gauge, unit: count, v: ok}
```

---

## Networking — Front Door / CDN — `azure_microsoft_cdn_profiles_*` ✅ [slug: cspazure-frontdoor]

Front Door/CDN (`microsoft.cdn/profiles`): `_percentage4xx_average_percent`, `_percentage5xx_average_percent`,
`_requestsize_total_bytes`, `_responsesize_total_bytes`, `_totallatency_average_milliseconds`,
`_originhealthpercentage_average_percent`, `_originlatency_average_milliseconds`,
`_originrequestcount_total_count`, `_requestcount_total_count` (+ `dimension_Endpoint`,
`dimension_ClientCountry`, `dimension_HttpStatusGroup`).

Front Door live dims (serverless path, all underscore): `dimension_Endpoint`(fqdn),
`dimension_HttpStatusGroup` (⚠ **lowercase `2xx`/`4xx`** — azure_exporter emits **uppercase `2XX`/`4XX`**),
`dimension_HttpStatus`(`200`/`404`), `dimension_ClientCountry`(`united kingdom`),
`dimension_ClientRegion`(`emea`), `dimension_Origin`(`example.com:443`), `dimension_OriginGroup`.

```yaml signals
family: azure_microsoft_cdn_profiles
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-cdn-profiles
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Cdn/profiles/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <profile-name>
  interval: PT1M
  timespan: PT5M
  # serverless dimension labels (underscore form):
  dimension_Endpoint: <fqdn>
  dimension_HttpStatusGroup: 2xx|3xx|4xx|5xx    # ⚠ lowercase on serverless; azure_exporter: uppercase 2XX/4XX + dimensionEndpoint/dimensionHttpStatusGroup (no underscore)
  dimension_HttpStatus: "200"|"404"|...
  dimension_ClientCountry: <country>
  dimension_ClientRegion: <region>
  dimension_Origin: <origin-fqdn:port>
  dimension_OriginGroup: <group>
metrics:
  - {root: percentage4xx_average_percent, type: gauge, unit: percent, v: ok}
  - {root: percentage5xx_average_percent, type: gauge, unit: percent, v: ok}
  - {root: requestsize_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: responsesize_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: totallatency_average_milliseconds, type: gauge, unit: ms, v: ok}
  - {root: originhealthpercentage_average_percent, type: gauge, unit: percent, v: ok}
  - {root: originlatency_average_milliseconds, type: gauge, unit: ms, v: ok}
  - {root: originrequestcount_total_count, type: gauge, unit: count, v: ok}
  - {root: requestcount_total_count, type: gauge, unit: count, v: ok, note: "+ dimension_Endpoint, dimension_ClientCountry, dimension_HttpStatusGroup"}
```

---

## Networking — Virtual Networks — `azure_microsoft_network_virtualnetworks_*` ✅ [slug: cspazure-vnet]

Virtual Networks
(`microsoft.network/virtualnetworks`, ⚠ **NO aggregation infix** — end in `_count` directly, + `region`):
`_subnets_count` (seed), `_availableaddresses_count`, `_connectedpeerings_count`, `_peerings_count`,
`_availablesubnetaddresses_count` (+ `subnet_name`), `_assignedsubnetaddresses_count` (+ `subnet_name`).

```yaml signals
family: azure_microsoft_network_virtualnetworks
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-network-virtualnetworks
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.Network/virtualNetworks/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <vnet-name>
  region: <azure-region>
  interval: PT1M
  timespan: PT5M
  subnet_name: <subnet>    # on availablesubnetaddresses, assignedsubnetaddresses
metrics:
  - {root: subnets_count, type: gauge, unit: count, v: ok, note: "seed; ⚠ NO aggregation infix — ends in _count directly"}
  - {root: availableaddresses_count, type: gauge, unit: count, v: ok}
  - {root: connectedpeerings_count, type: gauge, unit: count, v: ok}
  - {root: peerings_count, type: gauge, unit: count, v: ok}
  - {root: availablesubnetaddresses_count, type: gauge, unit: count, v: ok, note: "+ subnet_name"}
  - {root: assignedsubnetaddresses_count, type: gauge, unit: count, v: ok, note: "+ subnet_name"}
```

---

## Messaging — Event Hubs — `azure_microsoft_eventhub_namespaces_*` ✅ [slug: cspazure-eventhubs]

Messaging — Event Hubs (`microsoft.eventhub/namespaces`): `_activeconnections_maximum_count`,
`_connectionsopened_maximum_count`, `_connectionsclosed_maximum_count`, `_incomingrequests_total_count`
(seed), `_successfulrequests_total_count`, `_throttledrequests_total_count`, `_usererrors_total_count`,
`_servererrors_total_count`, `_incomingbytes_total_bytes`, `_outgoingbytes_total_bytes`,
`_incomingmessages_total_count` (+ `dimension_EntityName`), `_outgoingmessages_total_count` (+dim),
`_capturedmessages_total_count` (+dim).

```yaml signals
family: azure_microsoft_eventhub_namespaces
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-eventhub-namespaces
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.EventHub/namespaces/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <namespace>
  interval: PT1M
  timespan: PT5M
  dimension_EntityName: <hub-name>    # serverless underscore form; azure_exporter single-dim: dimension="<value>"
metrics:
  - {root: activeconnections_maximum_count, type: gauge, unit: count, v: ok}
  - {root: connectionsopened_maximum_count, type: gauge, unit: count, v: ok}
  - {root: connectionsclosed_maximum_count, type: gauge, unit: count, v: ok}
  - {root: incomingrequests_total_count, type: gauge, unit: count, v: ok, note: seed}
  - {root: successfulrequests_total_count, type: gauge, unit: count, v: ok}
  - {root: throttledrequests_total_count, type: gauge, unit: count, v: ok}
  - {root: usererrors_total_count, type: gauge, unit: count, v: ok}
  - {root: servererrors_total_count, type: gauge, unit: count, v: ok}
  - {root: incomingbytes_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: outgoingbytes_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: incomingmessages_total_count, type: gauge, unit: count, v: ok, note: "+ dimension_EntityName"}
  - {root: outgoingmessages_total_count, type: gauge, unit: count, v: ok, note: "+ dimension_EntityName"}
  - {root: capturedmessages_total_count, type: gauge, unit: count, v: ok, note: "+ dimension_EntityName"}
```

---

## Messaging — Service Bus — `azure_microsoft_servicebus_namespaces_*` ✅ [slug: cspazure-servicebus]

Service Bus (`microsoft.servicebus/namespaces`):
`_incomingmessages_total_count`, `_outgoingmessages_total_count`, `_incomingrequests_total_count`,
`_successfulrequests_total_count`, `_activeconnections_total_count`, `_usererrors_total_count`,
`_servererrors_total_count`, `_messages_average_count` (seed), `_activemessages_average_count`
(+`dimension_EntityName`), `_size_average_bytes` (+`dimension_EntityName`).

Service Bus per-entity identity: `dimension="<queue|topic>"` (azure_exporter single-dim) /
`dimension_EntityName` (serverless underscore form).

```yaml signals
family: azure_microsoft_servicebus_namespaces
scope: substrate
sink: promrw
labels:
  job: integrations/azure_exporter   # serverless: cloud/azure/microsoft-servicebus-namespaces
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.ServiceBus/namespaces/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <namespace>
  interval: PT1M
  timespan: PT5M
  dimension_EntityName: <queue|topic>    # serverless; azure_exporter single-dim: dimension="<queue|topic>"
metrics:
  - {root: incomingmessages_total_count, type: gauge, unit: count, v: ok}
  - {root: outgoingmessages_total_count, type: gauge, unit: count, v: ok}
  - {root: incomingrequests_total_count, type: gauge, unit: count, v: ok}
  - {root: successfulrequests_total_count, type: gauge, unit: count, v: ok}
  - {root: activeconnections_total_count, type: gauge, unit: count, v: ok}
  - {root: usererrors_total_count, type: gauge, unit: count, v: ok}
  - {root: servererrors_total_count, type: gauge, unit: count, v: ok}
  - {root: messages_average_count, type: gauge, unit: count, v: ok, note: seed}
  - {root: activemessages_average_count, type: gauge, unit: count, v: ok, note: "+ dimension_EntityName"}
  - {root: size_average_bytes, type: gauge, unit: bytes, v: ok, note: "+ dimension_EntityName"}
```

---

## AI — Cognitive Services / Azure OpenAI — `azure_microsoft_cognitiveservices_accounts_*` ✅ [slug: cspazure-ai]

**OPT-IN sub-signal** — NOT included in the default `allSubSignals` set. Must be explicit in
the blueprint: `sub_signals: [ai]`.

Resource type: `microsoft.cognitiveservices/accounts`. ARM provider namespace: `Microsoft.CognitiveServices`.

*Provenance: Azure Monitor docs, `Microsoft.CognitiveServices/accounts` supported metrics table
(https://github.com/microsoftdocs/azure-monitor-docs/blob/main/articles/azure-monitor/reference/supported-metrics/microsoft-cognitiveservices-accounts-metrics.md).
Sourced via ctx7 `/microsoftdocs/azure-monitor-docs`, 2026-06-15. `v: ok` for all REST API names
confirmed from the table; `v: assumed` for `ProcessedFineTunedTrainingHours` (description confirmed,
exact REST API name spelling not byte-verified from table — see SK-45 in cantfind.md).*

**Per-model magnitude weighting:** Azure OpenAI construct emission is weighted per model by
`genai.VolumeWeight(modelId)` and cost metrics by `genai.BlendedCostPerToken(modelId, inputFrac)`.
The catalogue of Azure OpenAI model IDs (`gpt-4o`, `gpt-4o-mini`, `gpt-4.1`, `gpt-4.1-mini`,
`gpt-4.1-nano`, `o3`, `o4-mini`, `text-embedding-3-small`, `text-embedding-3-large`), costs, and
weights lives in [`signals/genai-models.md`](genai-models.md) `[slug: genai-models-azure-openai]`.

Account-level metrics (no per-deployment dimension): `_total_calls_total_count` (seed),
`_successful_calls_total_count`, `_blocked_calls_total_count`, `_total_errors_total_count`,
`_client_errors_total_count`, `_server_errors_total_count`, `_total_token_calls_total_count`.

Per-deployment metrics (+ `dimension_ModelDeploymentName`, `dimension_ModelName`):
`_processed_prompt_tokens_total_count`, `_generated_completion_tokens_total_count`,
`_tokens_per_second_average_count`, `_processed_fine_tuned_training_hours_total_count`
(⚠ SK-45: REST API name `ProcessedFineTunedTrainingHours` — assumed spelling, not byte-confirmed).

Env-awareness: when `fx.Env` is set, every series carries `env=<Env.Name>` and magnitudes scale
by `Shape.Factor(now, Env.Weight, Env.NonProd)`. When `fx.Env` is nil, `env` label is OMITTED (I13).

```yaml signals
family: azure_microsoft_cognitiveservices_accounts
scope: substrate
sink: promrw
labels:
  job: integrations/azure    # azure_exporter path; serverless: cloud/azure/microsoft-cognitiveservices-accounts
  resourceID: /subscriptions/<id>/resourceGroups/<rg>/providers/Microsoft.CognitiveServices/accounts/<name>
  subscriptionID: <sub-id>
  subscriptionName: <sub-name>
  resourceGroup: <rg>
  resourceName: <account-name>
  interval: PT1M
  timespan: PT1M    # azure_exporter; serverless omits interval/timespan
  # per-deployment metrics also carry (serverless underscore form):
  dimension_ModelDeploymentName: <deployment-name>    # e.g. gpt4o-deploy
  dimension_ModelName: <model-name>                   # e.g. gpt-4o
  # env-awareness (opt-in via fx.Env):
  env: <env-name>    # omitted when fx.Env is nil (I13)
metrics:
  # Account-level (no per-deployment dimension) — REST API name → snake_case metric suffix
  - {root: total_calls_total_count, type: gauge, unit: count, v: ok, note: "seed; REST: TotalCalls; ⚠ 'Do not use for Azure OpenAI service' note in docs refers to NEW AzureOpenAI metrics namespace — TotalCalls is still emitted for the accounts resource type"}
  - {root: successful_calls_total_count, type: gauge, unit: count, v: ok, note: "REST: SuccessfulCalls"}
  - {root: blocked_calls_total_count, type: gauge, unit: count, v: ok, note: "REST: BlockedCalls; rate/quota throttle"}
  - {root: total_errors_total_count, type: gauge, unit: count, v: ok, note: "REST: TotalErrors; 4xx+5xx"}
  - {root: client_errors_total_count, type: gauge, unit: count, v: ok, note: "REST: ClientErrors; 4xx"}
  - {root: server_errors_total_count, type: gauge, unit: count, v: ok, note: "REST: ServerErrors; 5xx"}
  - {root: total_token_calls_total_count, type: gauge, unit: count, v: ok, note: "REST: TotalTokenCalls; dims: ApiName, OperationName, Region"}
  # Per-deployment (+ ModelDeploymentName + ModelName dimensions)
  - {root: processed_prompt_tokens_total_count, type: gauge, unit: count, v: ok, note: "REST: ProcessedPromptTokens; input tokens on OpenAI model (PTU + PAYG)"}
  - {root: generated_completion_tokens_total_count, type: gauge, unit: count, v: ok, note: "REST: GeneratedCompletionTokens; output tokens (PTU + PAYG)"}
  - {root: tokens_per_second_average_count, type: gauge, unit: count_per_second, v: ok, note: "REST: TokensPerSecond; generation speed — total generated ÷ time (PTU + PTU-managed + PAYG)"}
  - {root: processed_fine_tuned_training_hours_total_count, type: gauge, unit: count, v: assumed, note: "REST: ProcessedFineTunedTrainingHours (⚠ SK-45: REST name spelling assumed — confirm against live capture); fine-tuned model training hours"}
```

---

## Logs [slug: cspazure-logs]

Logs: `{job="integrations/azure_event_hubs", topic="<hub>"}`; body = raw Azure Monitor JSON
(`time, resourceId, category, operationName, level, location`) — NOT logfmt.
