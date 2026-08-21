# Synthkit operational-friendliness and usability closeout

Date: 2026-08-21  
Campaign: `SKT-0005` — Operational readiness for first-time Grafana practitioners  
Stable release: `v1.3.1`  
Stable source revision: `bcf9a1e6dfe099bfd04bd7b69e4cb75c0a9999de`  
Tracker reconciliation head: `59b83abf9d4c693190ef75b3e014733a284e5e72`  

## Executive outcome

The operational-friendliness and usability campaign is complete. `SKT-0005` and all nineteen child tasks are Done. The delivered path now covers safe first use, focused onboarding, blueprint lifecycle operations, portable agent instructions and skills, real readiness, control-plane safety, delivery visibility, optional Grafana product dispositions, and reproducible signed upgrades and rollbacks.

The result was verified beyond local tests. The exact stable image passed its publisher gate, independent image verification, exact-SHA CI, and a complete standing-host sequence from the legacy deployment through a release candidate and stable release, back to the candidate as a rollback, and finally to stable again. The standing deployment was healthy and stable at the report boundary.

Two unrelated pre-existing queue items remain outside this campaign: `SKT-0004` is To Do and `SKT-0002` is Parked. Neither is operational-friendliness or usability work.

## Campaign completion

All nineteen children of `SKT-0005` are terminal. Together they delivered:

- safe and explicit source and Docker defaults, including credential-free dry-run behavior;
- one focused first-workload journey with concrete metrics, logs, and traces identities;
- trustworthy custom and Git blueprint validation, staging, apply, restart, and loaded-state semantics;
- canonical portable instructions and forward-tested operational skills for Codex and Claude Code;
- mandatory live-configuration preflight, real Compose health/readiness, writable-state checks, and delivery-aware status;
- authenticated control operations, deliberate non-loopback exposure, and owner-only persisted files;
- observable delivery failures, queue pressure, sizing guidance, and exact optional-lane dispositions;
- safe item-level blueprint and scenario mutations;
- a Docker-shipped Synthetic Monitoring provisioner with private ownership, adoption, migration, and crash-recovery state; and
- immutable release identity, signature/provenance verification, integrity-checked state snapshots, guarded selector changes, published-Compose testing, and documented rollback.

Telemetry-signal realism remained outside the campaign, as defined at its start.

## Release-gate history and recovery

The unsuccessful release attempts were retained as evidence and corrected through normal forward releases rather than rewritten.

### RC.27

RC.27 proved exact index identity, signature, provenance, and binary version/revision reporting. It exposed two defects in the release gate:

1. The binary probe could cache a platform-selected image under the multi-platform index alias, weakening later index checks.
2. The continuously running Compose service was sampled before phase-spread emissions had completed.

The gate was changed to probe the immutable platform child, validate the committed default separately, prove health and writable state, and then run a quiesced complete `-once` emission through the same published Compose service. The corrected sequence passed locally with all intended fake-sink lanes decoded.

### RC.28

RC.28 at revision `fedb517` passed main CI, exact index identity, signature, provenance, binary version/revision, and the published Compose test body. Its hosted job then failed during temporary-directory cleanup because the uid-65532 container had written owner-only state.

The recovery was deliberately scoped: after Compose shutdown, only the isolated temporary state directory has its ownership restored to the runner before Go removes the test directory. The published-image path then passed locally without weakening production state permissions.

### v1.3.0

The `v1.3.0` release was merged before standing-host validation. Its image passed identity, signature, provenance, and the published Compose test body, but its hosted publisher still carried the RC.28 cleanup failure. The tag and release were preserved as historical evidence.

Recovery proceeded through a normal `v1.3.1` patch cycle. The scoped cleanup fix and repository-bound stable-publisher dispatch were validated before the corrected stable release was accepted.

## Stable release evidence

The accepted `v1.3.1` image has the following closed identity:

| Identity | Value |
|---|---|
| Source revision | `bcf9a1e6dfe099bfd04bd7b69e4cb75c0a9999de` |
| Multi-arch index | `sha256:0ddf2e621d7a0023c058d39e366de4aa00fc947ca748cf31f6a4399a2069525f` |
| amd64 manifest | `sha256:5fe68b18e60323bca55a80d0f40912b48680c5741bfabd6afa45fc1feff7f8c6` |
| amd64 config/runtime image ID | `sha256:9ea6fa176afb232a8bac051f4ca33654cd3a5b0469a55e97e2c02344d19358c9` |

Publisher run `32489965091` passed index and platform identity, signature, provenance, version/revision, the committed published Compose path, health, writable private state, fake-sink delivery, and cleanup. Independent local `verify-image` also passed. Exact-SHA CI run `32491016936` passed every job, including the aggregate `ci-success` gate.

The stable repository defaults were applied in `2e687c7195efb7c0fc7daacb7094b6316f3cc2c4`: both Compose fallbacks and the Makefile use `1.3.1`, while `.env.example` carries the exact stable index. Mutable `main` and `latest` remain documented edge/testing choices rather than standing-deployment defaults.

## Standing-host upgrade and rollback proof

The live exercise used preserved credentials and configuration, immutable candidate identities, and separate closed records for each transition. It completed this sequence:

1. Legacy deployment to the release candidate.
2. Release candidate to `v1.3.1` stable.
3. Stable back to the preserved release candidate rollback target.
4. Release candidate to `v1.3.1` stable as the final restored state.

Every transition verified:

- configured index, selected host manifest, OCI config, running image ID, binary version, and source revision;
- Compose health and delivery-aware readiness;
- owner-writable persisted state;
- successful delivery with zero failures for the four configured sink lanes;
- exactly nine secret-safe optional-lane dispositions; and
- fresh post-start metrics, logs, and traces in the selected Grafana environment.

The final standing deployment was healthy on stable `1.3.1` using:

- `/opt/compose/synthkit/docker-compose.2e687c7.yml`;
- `/opt/synthkit/deployment-records/skt-0005.14-image.override.yml`; and
- `/opt/synthkit/deployment-records/skt-0005.14-state.override.yml`.

The deployment configuration remained byte-for-byte unchanged, with `.env` SHA-256 `c6de23d399a91622f51545c57b884495ae789d2f18f18d9866d897dcfbdc57d2`. The legacy Compose artifact also remained unchanged at SHA-256 `920a02d30668208060d9c64013842f009fd2cf3787f874123b27260a0c29c5e6`. Persisted state remained mode `0700`, owned by uid/gid `65532`, with no special filesystem entries. The global Compose environment file was neither modified nor referenced by the selected Compose artifacts.

## Retained recovery artifacts

Recovery evidence was intentionally retained rather than cleaned up after the successful restoration:

- legacy, release-candidate, and stable state snapshots;
- closed release-candidate and stable image-identity records; and
- displaced state trees `.control-state-data.displaced-18621bfe162b` and `.control-state-data.displaced-f07991682df7`.

These artifacts preserve concrete rollback and forensic targets. They should not be removed as routine cleanup.

## Closing gates

The final campaign reconciliation passed:

- `make blueprint-schema`, with regenerated output producing no diff;
- `make gate`;
- complete-catalog dry-run coverage of 26 blueprints, 2,644 distinct series names, and 15 profile types;
- `make gate-ui`: 22 files and 169 tests, followed by typecheck and build;
- `make docs-check`;
- `make skills-check`;
- `make compose-check`;
- `actionlint`;
- `make e2e`; and
- exact-SHA hosted CI and the stable published-image gate described above.

The local `TestPublishedCompose` leg was skipped only because its published-image environment was not set in that local invocation. This is not counted as local coverage: the stable publisher exercised and passed the same published-Compose behavior against the released image.

CodeRabbit was used during the code-bearing implementation and recovery cycles, with Critical and Warning findings resolved before delivery. It was intentionally skipped for the final tracker reconciliation and this report because both are documentation-only changes.

## Bounded optional-lane evidence

The standing-host proof reported all nine optional lanes truthfully and without credential values: RUM, Fleet metrics, Fleet registration, Synthetic Monitoring provisioning, self-observability, process profiling, Sigil, private Git, and synthetic profiles. A disposition is not the same as live activation.

Only configured live lanes were claimed as live-verified. In particular, Synthetic Monitoring mutation was not attempted because that lane was not configured on the standing host. Its accepted evidence is the version-matched Docker provisioner, fake-API ownership/collision/adoption/migration/crash tests, and release-gate packaging proof. Disabled or partial optional lanes likewise remain disposition-verified rather than remotely activated.

## Final state

At the report boundary:

- `SKT-0005` and every one of its nineteen children were Done;
- stable `v1.3.1` was the healthy standing deployment;
- `main` and `origin/main` matched at tracker reconciliation head `59b83abf9d4c693190ef75b3e014733a284e5e72`;
- the generated local `runtime/sm-snapshot-v1.json` from the closing dry-run had been identified and removed, without touching retained deployment recovery artifacts; and
- no operational-friendliness or usability task remained open.
