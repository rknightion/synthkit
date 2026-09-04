---
title: Security
description: What synthkit handles and never handles, credential scope, secret storage, transport security, and how to report a vulnerability.
---

# Security

This page covers synthkit's data-handling posture, credential requirements, and the security
properties of the control plane. For the formal disclosure process, see
[Reporting a vulnerability](#reporting-a-vulnerability).

## What data synthkit handles

synthkit is a **synthetic telemetry generator**. It reads blueprint YAML files you author and
emits structurally-correct but entirely fictional metrics, traces, logs, and optional RUM/profiles
to Grafana Cloud. It has no inbound integration with a real production system and nothing to steal
from one:

- Every identity it emits — pod names, node IDs, instance keys, database names — is derived
  deterministically from the blueprint's own name and path via a seeded hash. None of it is read
  from, or corresponds to, a real fleet.
- Metric and label **names** are sourced from the `signals/` catalogue (real, production-validated
  contracts), but the **values** attached to them are synthetic. synthkit never invents a name and
  never emits real captured values.
- The content of emitted synthetic data is explicitly **out of scope** for vulnerability reports —
  it carries no real user data by construction.

The one path where synthkit does touch something real is `skcapture`, which reads live Kubernetes
inventory (deployments, services, node metadata) to seed a blueprint draft. It never captures Secret
or arbitrary ConfigMap data values; its narrow optional cluster-identity grant reads only the `cluster` key
from one named collector release-info ConfigMap. Prefer the encrypted output mode
(`--passphrase-file`) over `--plain` whenever the source cluster is not a throwaway. See [Capture &
Tooling](tools.md).

## Credentials and minimum scope

Each signal type synthkit can emit uses its own credential, scoped to only the write permission it
needs. Fill in only what you use — every credential is optional except the four synthetic-sink
variables required for a live push.

| Purpose | Credential | Minimum scope |
|---|---|---|
| Synthetic metrics, logs, traces, profiles | `GC_TOKEN` (one CAP token, shared) | `metrics:write`, `logs:write`, `traces:write`, and `profiles:write` only if you enable profiles |
| Synthetic Monitoring provisioning | `GC_SM_TOKEN` | A dedicated SM API token — not `GC_TOKEN` |
| Fleet Management registration | `GC_FM_TOKEN` | CAP token with `fleet-management:write` only |
| Self-observability (synthkit's own process telemetry) | `GC_SELF_OTLP_PASSWORD` / `GC_PYROSCOPE_PASSWORD` | A CAP token on a **separate** stack — never `GC_TOKEN` |
| Private git blueprint sources | `GIT_TOKEN` or a per-source `token_env_var` | An HTTPS PAT scoped to read the blueprint repo only |

All of these are write-only or read-only credentials scoped to a single Grafana Cloud stack and,
for Fleet Management and self-observability, kept deliberately isolated from the synthetic-data
stack so a leak of one cannot be used against the other. See [Credentials](credentials.md) for the
full reference and where to generate each one.

## Where secrets live

**Credentials belong only in a gitignored `.env` file. They must never be committed, and they must
never appear in a blueprint YAML file or in `docker-compose.yml`.** The committed
`docker-compose.yml` is deliberately secret-free. The emitter reads credentials via
`env_file: .env`; the one-shot SM profile interpolates only its dedicated SM credentials. Blueprints
are meant to be shared, copied, and version-controlled, so nothing secret can belong in one.

Two secondary places also need attention:

- **The control-state snapshot** (`CONFIG_SNAPSHOT_PATH`, default `./control-state.json`) contains
  operational state and git source metadata. It persists only a private source's configured
  `token_env_var` name (for example `MY_GIT_TOKEN`), never the resolved token value. The token is
  read from the process environment only when a fetch runs. The snapshot is still owner-only
  operational data and should be excluded from untrusted backups. See [Control Plane](control-plane.md).
- **Docker's persistent volume** (`/data`, bind-mounted to `control-state-data/` and owned by uid
  65532) holds this same snapshot plus staged custom blueprints. Setup uses mode `0700` for its
  directories and `0600` for the snapshot, manifests, and staged YAML files.

`GET /control/config` returns the runtime configuration with all secret values replaced by
`[redacted]` — safe to share for debugging without exposing credentials.

## Transport security

- All synthetic-data pushes (Remote-Write v2, OTLP, Loki, Faro, Pyroscope) go to Grafana Cloud
  endpoints over HTTPS.
- The embedded control plane (`JSON_HTTP_ADDR`, default `127.0.0.1:8088`) serves **plain HTTP**
  with no built-in TLS. Loopback remains the frictionless default. With `CONTROL_TOKEN` set,
  sensitive control reads, all Infinity data routes, and every mutation require HTTP Basic auth;
  only `/healthz` and sanitized `/control/readiness` remain public. A non-loopback bind will not
  start unless `CONTROL_TOKEN` is non-empty and `CONTROL_EXPOSURE_ACK` is exactly
  `trusted-network` or `tls-proxy`. Do not send Basic credentials over untrusted plaintext HTTP;
  use an SSH tunnel or a browser-trusted TLS proxy such as `tailscale serve`.

## What a compromised instance could and could not reach

**Could:**

- Push synthetic-looking data to whichever Grafana Cloud sinks its configured `GC_TOKEN` (or
  self-obs / FM / SM credentials) can write to, within that token's granted scopes.
- On the intentionally compatible loopback/no-token setup, mutate live state: inject
  failures, scale workloads to zero, disable blueprints or constructs, or replace active
  scenarios — all without authentication.
- Read a configured git blueprint source's PAT only if it can read the source's named process
  environment variable; the resolved value is never serialized into `control-state.json`.

**Could not:**

- Access any real production data — synthkit has no code path that reads from, or forwards, a real
  customer system. The synthetic data path and the credentials it uses are entirely separate from
  anything synthkit's own telemetry might describe.
- Read back the plaintext of `GC_TOKEN` or any other credential through the control plane API —
  `GET /control/config` redacts every secret value.
- Escalate beyond its container: the official image is distroless (no shell, no package manager),
  runs as non-root uid 65532, and the root filesystem is read-only outside the `/data` volume.

## Reporting a vulnerability

Do not open a public issue for a security vulnerability. Report it privately via GitHub's private
vulnerability reporting on this repository (**Security → Report a vulnerability**), including
details and, where possible, a minimal reproduction. Expect an acknowledgement within a few
business days; disclosure timelines are agreed with the reporter, and credit is given in the
release notes unless the reporter prefers to stay anonymous.

Especially in scope: credential handling paths that could leak `GC_TOKEN` or the other write keys
into a log, metric label, or trace attribute; the egress sinks (`promrw`, `otlp`, `loki`) for
unintended data exfiltration or SSRF; `skcapture`'s and `skforge`'s handling of Kubernetes
service-account tokens, cluster secrets, and age-encrypted inventory bundles; and the `/control`
admin HTTP surface for authentication bypass, arbitrary file read, or command injection. The
content of emitted synthetic data itself is explicitly out of scope — it is intentionally
fictional and carries no real user data.

Security fixes are applied to the latest released `1.x` minor and shipped in a new patch release;
older majors are not maintained.
