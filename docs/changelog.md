---
title: Changelog
description: Version history for synthkit, generated from Conventional Commits via release-please.
---

# Changelog

synthkit uses [Conventional Commits](https://www.conventionalcommits.org/) and [release-please](https://github.com/googleapis/release-please-action) for automated changelog generation and version management.

On every push to `main`, release-please maintains a release PR that accrues changes. Merging that PR tags `vX.Y.Z`, creates the GitHub Release with the changelog section as release notes, and triggers the multi-arch container image publish to GHCR.

The current `CHANGELOG.md` is maintained in the repository. GitHub Releases (including attached SBOMs and third-party notices) are at:

**[https://github.com/rknightion/synthkit/releases](https://github.com/rknightion/synthkit/releases)**

---

## v1.0.0 — 2026-06-29

Initial public release of synthkit — composable synthetic telemetry generator for Grafana Cloud.

### Features

- Full catalog of 42 construct kinds and 2 workload kinds across AWS, Azure, GCP, Kubernetes, AI/LLM, Network, and Grafana product areas.
- Blueprint YAML composition: declare infrastructure and applications in one file; emit structurally-correct synthetic metrics (Prometheus Remote-Write v2), traces (OTLP), logs (Loki), optional RUM (Faro), and synthetic profiles (Pyroscope).
- Two-cadence scheduler with decoupled in-memory delivery queue.
- Control plane with operator UI, live scenario activation, live pod scaling, span-metrics opt-in.
- `skcapture` + `skforge` capture-to-blueprint tooling (AWS/EKS v1).
- `synthkit-dash` + `synthkit-control-dash` Grafana v2 dashboard generators.
- Claude Code / Codex / OpenCode LLM skills: `/initial-setup`, `/create-blueprint`, `/setup-fleet-management`, `/verify-deployment`.
- AGPL-3.0-only license with full OSS governance: SPDX headers enforced, forbidden-words hygiene guard, release-please automation, Renovate dependency management.
- GitHub Actions: release-please, GHCR multi-arch image publish, CodeQL, zizmor, actionlint, dependency review.

---

For the full commit history, see the [GitHub commit log](https://github.com/rknightion/synthkit/commits/main).
