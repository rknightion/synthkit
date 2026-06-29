# Cloudflare (→ Mimir) — ScopeBlueprint

Cloudflare synthetic families. Unlike the Azure/GCP CSP families, Cloudflare is **blueprint-scoped**
(carries the `blueprint` label). Global rules: see [`00-canon.md`](00-canon.md) — scoping
`[slug: scoping]`. The tunnel family uses the `env` label (see [`00-canon.md`](00-canon.md)
`[slug: env-label-keys]`).

---

*Provenance: predecessor SIGNALS §6.3 Group C + `emit/cloudflare.go`. Source: `lablabs/cloudflare-exporter` (OSS-verified).*
Feature `cloudflare`; sub-signals `zone,tunnel`. `job="cloudflare_exporter"`. ⚠ Unlike the Azure/GCP
CSP families, Cloudflare is **blueprint-scoped** (carries the `blueprint` label). Zone name, account
ID, and PoP codes are config-driven knobs with generic defaults (SK-19 resolved 2026-06-14: source
confirmed = `lablabs/cloudflare-exporter`, OSS-verified).

## Zone metrics — `cloudflare_zone_*` ✅ [slug: cloudflare-zone]

**Zone metrics** (zone-global; `st.Add`; base labels `zone`, `account`, `job`):
`cloudflare_zone_requests_total`, `_requests_cached`, `_requests_status` (+`status`), `_requests_country`
(+`country,region`), `_bandwidth_total`, `_bandwidth_cached`, `_threats_total`, `_threats_type`
(+`type`), `_pageviews_total`, `_uniques_total`, `_colocation_requests_total` (+`colocation,host`),
`_colocation_visits` (+`colocation,host`), `_firewall_events_count` (+`action,source,rule,host,country`),
`_health_check_events_origin_count`.

```yaml signals
family: cloudflare_zone
scope: blueprint
sink: promrw
labels:
  job: cloudflare_exporter
  zone: <zone-name>
  account: <account-id>
metrics:
  - {root: cloudflare_zone_requests_total, type: counter, unit: count, v: ok}
  - {root: cloudflare_zone_requests_cached, type: counter, unit: count, v: ok}
  - {root: cloudflare_zone_requests_status, type: counter, unit: count, v: ok, note: "+label status"}
  - {root: cloudflare_zone_requests_country, type: counter, unit: count, v: ok, note: "+labels country,region"}
  - {root: cloudflare_zone_bandwidth_total, type: counter, unit: bytes, v: ok}
  - {root: cloudflare_zone_bandwidth_cached, type: counter, unit: bytes, v: ok}
  - {root: cloudflare_zone_threats_total, type: counter, unit: count, v: ok}
  - {root: cloudflare_zone_threats_type, type: counter, unit: count, v: ok, note: "+label type"}
  - {root: cloudflare_zone_pageviews_total, type: counter, unit: count, v: ok}
  - {root: cloudflare_zone_uniques_total, type: counter, unit: count, v: ok}
  - {root: cloudflare_zone_colocation_requests_total, type: counter, unit: count, v: ok, note: "+labels colocation,host"}
  - {root: cloudflare_zone_colocation_visits, type: counter, unit: count, v: ok, note: "+labels colocation,host"}
  - {root: cloudflare_zone_firewall_events_count, type: counter, unit: count, v: ok, note: "+labels action,source,rule,host,country"}
  - {root: cloudflare_zone_health_check_events_origin_count, type: counter, unit: count, v: ok}
```

## Tunnel metrics — `cloudflare_tunnel_*` ✅ [slug: cloudflare-tunnel]

**Tunnel metrics** (per declared tunnel; `st.Set` gauges; base labels `account, tunnel_id, tunnel_name,
tunnel_type="cfd_tunnel", env, job`): `cloudflare_tunnel_info` (0/1), `_health_status` (0/1),
`_connector_active_connections` (+`client_id`), `_connector_info` (0/1; +`client_id`).

```yaml signals
family: cloudflare_tunnel
scope: blueprint
sink: promrw
labels:
  job: cloudflare_exporter
  account: <account-id>
  tunnel_id: <tunnel-id>
  tunnel_name: <tunnel-name>
  tunnel_type: cfd_tunnel
  env: <env>
metrics:
  - {root: cloudflare_tunnel_info, type: gauge, unit: bool, v: ok, note: "0/1"}
  - {root: cloudflare_tunnel_health_status, type: gauge, unit: bool, v: ok, note: "0/1"}
  - {root: cloudflare_tunnel_connector_active_connections, type: gauge, unit: count, v: ok, note: "+label client_id"}
  - {root: cloudflare_tunnel_connector_info, type: gauge, unit: bool, v: ok, note: "0/1; +label client_id"}
```
