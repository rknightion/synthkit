---
title: Security
description: What synthkit handles and never handles, credential scope, secret storage, transport security, and how to report a vulnerability.
---

# Security

This page covers synthkit's data-handling posture, credential requirements, and the security
properties of the control plane. For the formal disclosure process, see
[Reporting a vulnerability](#reporting-a-vulnerability).

## What data synthkit handles

synthkit reads blueprint YAML and emits modeled metrics, traces, logs and optional RUM, profiles
and AI generation records. It does not deploy or scrape the infrastructure represented by those
blueprints. Many identities are derived from stable seeds, while others are supplied directly by
the blueprint or a capture-derived draft. Configuration can therefore contain real identifiers;
review it before publishing or sending telemetry.

Signal names and shapes follow the catalogue. Values and content are modeled fixtures, but that
does not sanitize arbitrary user-supplied values. The `ai_agent` profile permits modeled content
under its documented capture modes. Use synthetic examples, not private prompts or credentials.
Credential exposure and unintended disclosure remain security concerns even in a synthetic generator.

Synthetic delivery writes to real configured backends. Optional SM provisioning and Fleet
registration also use real control-plane APIs, and git blueprint sources contact their configured
repositories. These operations are distinct from the modeled infrastructure.

`skcapture` reads live Kubernetes
inventory (deployments, services, node metadata) to seed a blueprint draft. It never captures Secret
or arbitrary ConfigMap data values; its narrow optional cluster-identity grant reads only the `cluster` key
from one named collector release-info ConfigMap. Prefer the encrypted output mode
(`--passphrase-file`) over `--plain` whenever the source cluster is not a throwaway. See [Capture &
Tooling](tools.md).

## Credentials and minimum scope

Synthetic metrics, logs, traces and profiles share `GC_TOKEN`, with destination-specific endpoints
and usernames. Live startup requires the core metrics/logs/traces configuration; optional products
have their own gates. Use the exact variable checklist in [Credentials](credentials.md), and grant
only the scopes needed by the selected lanes.

| Purpose | Credential | Minimum scope |
|---|---|---|
| Synthetic metrics, logs, traces, profiles | `GC_TOKEN` (one CAP token, shared) | `metrics:write`, `logs:write`, `traces:write`, and `profiles:write` only if you enable profiles |
| Synthetic Monitoring provisioning | `GC_SM_TOKEN` | A dedicated SM API token — not `GC_TOKEN` |
| Fleet Management registration | `GC_FM_TOKEN` | CAP token with `fleet-management:write` only |
| Self-observability (synthkit's own process telemetry) | `GC_SELF_OTLP_PASSWORD` / `GC_PYROSCOPE_PASSWORD` | A CAP token on a **separate** stack — never `GC_TOKEN` |
| Private git blueprint sources | `GIT_TOKEN` or a per-source `token_env_var` | An HTTPS PAT scoped to read the blueprint repo only |

Scope each credential to its intended service and permissions. Self-observability uses a separate
stack and credential set; Fleet Management uses its explicitly selected stack and dedicated token.
Git credentials are repository credentials, not Grafana stack credentials. See [Credentials](credentials.md) for the
full reference and where to generate each one.

Git source credentials are limited to `GIT_TOKEN` (also the empty-name default) and
`GIT_TOKEN_<non-empty suffix>` environment variables. Names such as `CONTROL_TOKEN` or
synthetic/self-observability tokens are rejected both at source admission and before git I/O,
including for persisted legacy sources. Rename old custom token variables to the permitted prefix.

For delegated control access, configure `GIT_SOURCE_HOST_ALLOWLIST` with exact trusted hostnames or
IP addresses. Empty permits any HTTPS host and therefore still lets a control-plane user send a git
PAT to a chosen host. Matching is case-insensitive and does not include subdomains; any HTTPS port
on an allowed host is permitted. URLs, ports, wildcards and empty list entries are rejected. This
policy is enforced at admission and before fetch/ref lookup, before credential resolution. Redirects
are refused to prevent a trusted URL from forwarding credentials to another destination. This is a
host-name policy, not an IP-range or DNS-rebinding defense; enforce network egress restrictions
separately where needed. Use read-only, repository-scoped git PATs.

## Where secrets live

**Credentials belong only in a gitignored `.env` file. They must never be committed, and they must
never appear in a blueprint YAML file or in `docker-compose.yml`.** The committed
`docker-compose.yml` is deliberately secret-free. The emitter reads credentials via
`env_file: .env`; the one-shot SM profile interpolates only its dedicated SM credentials. Blueprints
are meant to be shared, copied, and version-controlled, so nothing secret can belong in one.

Two secondary places also need attention:

- **The control-state snapshot** (`CONFIG_SNAPSHOT_PATH`, default `./control-state.json`) contains
  operational state and git source metadata. It persists only a private source's configured
  `token_env_var` name (for example `GIT_TOKEN_REPO`), never the resolved token value. The token is
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

## Exposure boundaries

A compromised process can use the credentials, files and network access available to it. Synthetic
modeling does not remove that risk: supplied blueprint content, git-source access and optional
product APIs are real inputs and destinations.

- A token-free loopback control plane permits local callers to change modeled state. Configure
  authentication and deliberate exposure before making it reachable beyond a trusted local host.
- `GET /control/config` redacts secret values. This API behavior does not protect process memory
  or environment variables from an attacker who already controls the process.
- The official image is distroless and runs as non-root uid 65532. The deployment's read-only root
  filesystem and writable-state mount reduce exposure; they are not proof against container escape.
- Limit network reachability, filesystem mounts and credential scopes to the declared purpose.

## Reporting a vulnerability

Do not open a public issue for a security vulnerability. Report it privately via GitHub's private
vulnerability reporting on this repository (**Security → Report a vulnerability**), including
details and, where possible, a minimal reproduction. Expect an acknowledgement within a few
business days; disclosure timelines are agreed with the reporter, and credit is given in the
release notes unless the reporter prefers to stay anonymous.

Especially in scope: credential handling paths that could leak `GC_TOKEN` or the other write keys
into a log, metric label, or trace attribute; the egress sinks (`promrw`, `otlp`, `loki`) for
unintended data exfiltration or SSRF; `skcapture`'s and `skforge`'s handling of Kubernetes
service-account credentials and age-encrypted inventory bundles; and the `/control`
admin HTTP surface for authentication bypass, arbitrary file read, or command injection. The realism of deliberately fictional fixture content is out of scope. Unintended disclosure
of credentials, private supplied content or captured identifiers remains in scope.

Security fixes are applied to the latest released `1.x` minor and shipped in a new patch release;
older majors are not maintained.
