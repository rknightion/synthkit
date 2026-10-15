# Security Policy

## Supported versions

`synthkit` follows semantic versioning. Security fixes are applied to the latest released `1.x`
minor and shipped in a new patch release. Older majors are not maintained.

| Version | Supported          |
| ------- | ------------------ |
| 1.x     | :white_check_mark: |
| < 1.0   | :x:                |

## Reporting a vulnerability

Please **do not open a public issue** for security vulnerabilities.

Report privately via GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository (**Security → Report a vulnerability**), including the details and, if possible, a
minimal reproduction.

You can expect an acknowledgement within a few business days. We will keep you informed of
progress, agree a disclosure timeline with you, and credit you in the release notes unless you
prefer to remain anonymous.

## Scope notes

`synthkit` is a **synthetic telemetry generator**: it reads blueprint YAML files and emits
structurally-correct synthetic metrics, traces, and logs to Grafana Cloud. The following are
especially in scope:

- **Credential handling** — the synthetic `GC_TOKEN`, optional product credentials, self-observability
  credentials and git-source tokens; any path that could leak those to a log,
  metric label, or trace attribute.
- **Egress sinks** — the Prometheus Remote-Write v2 sink (`internal/sink/promrw`), the OTLP metrics/logs/trace
  sink (`internal/sink/otlp`), and the Loki log sink (`internal/sink/loki`): any path that could
  cause unintended data exfiltration or SSRF.
- **Capture tooling** — `skcapture` runs inside a Kubernetes cluster (kubectl-shell container)
  and uses service-account credentials to read inventory. Secret and arbitrary ConfigMap values
  are not collected; a narrow optional grant reads the cluster identity from a named ConfigMap. `skforge` decrypts age-encrypted
  inventory bundles. Vulnerabilities in their RBAC handling, secret exposure, or encryption are
  in scope.
- **Admin HTTP surface** — the `/control` HTTP API and embedded admin UI: authentication bypass,
  arbitrary file read, or command injection.

Out of scope: the realism of deliberately fictional fixture content. Unintended disclosure of
credentials, private supplied content or captured identifiers remains in scope.
