# Loop: synthkit
tier: guarded
gate: just check
ci-required: ci-success
baseline-red: SKT-0112 - local race-tier control-dash package exceeds the unchanged 10m timeout at a9a31e1121cd25d6b025584b99d14d6af88196aa; compare exact base and candidate gate failures until the performance repair is accepted. Keep every test, assertion and timeout, and all required CI checks.
release-on-push: yes
deploy-on-push: no
receiver: https://loopwatch.m7kni.com
grafana-stack: none

## Credentials
- Secrets live only in the ignored `.env`, never in YAML, a blueprint, a doc or a backlog task. A new
  variable goes in both `.env` and `.env.example`.
- The self-observability path uses a separate credential triplet and stack, never `GC_TOKEN`.
- A lane never needs a live credential: gate legs run offline, and live-stack recipes belong to the root.

## Traps
- `just selfobs-dashboard`, `just corpus-gcx` and `just provision` mutate a live Grafana stack and are
  `[confirm]`-gated. Stop and ask; never pass `--yes` or `JUST_YES=1`. Run `just` with stdin from
  `/dev/null`.
- Never invent a metric, label or field name: source `signals/<area>.md` or vendor docs, else add a
  PENDING to `cantfind.md`. `cantfind.md` `SK-N` ids are stable and separate from `SKT-NNNN` tasks.
- A shipped blueprint carries plausible real-world rates only. Never raise a rate to force emission;
  a lane that must fire inside one tick belongs in `e2e/fixtures/`. Anything under `blueprints/` is
  deployable by an operator.
- Constructs and workloads never import each other, the blueprint package, OTel, selfobs or profiling.
  `internal/archtest` enforces it.
- Renderer changes are verified by inventory diff (`just dump` against `signals/`), not unit test.
- A new blueprint field or config struct needs `just gen`; `just gen-check` fails on drift. A new `.go`
  file needs the AGPL SPDX header (`just spdx-check`).
- `just race` excludes `internal/integration` on purpose (OOMs a 16 GB runner); `just test` covers it.
  Do not "fix" the exclusion.
- Slow tier: `just lab` (about 35 minutes in CI) and `just race` (about 10 minutes). Run once on the
  final candidate, never in the inner loop.
- The tree is habitually dirty with unrelated work: stage explicit paths, never `git add -A` or
  `git add .`.
- `backlog task edit --notes` / `--plan` bare silently replace the whole section; use
  `--append-notes` / `--append-plan`.
- Release automation is release-please on push to main; the publish workflow also pushes the edge
  `:main` image on every push.

## Mutexes
- Single-owner files, serialised: `internal/runner/`, `go.mod`, `go.sum`, shared core, fixture, shape,
  state and ledger types, each `blueprints/*.yaml`, `signals/<area>.md`, generated
  `BLUEPRINT-SCHEMA.md` and `fielddocs.json`, `cantfind.md`, `.env`, `.env.example`,
  `internal/archtest/arch_test.go`.
- Docker and live stacks are exclusive resources; live captures are read-only findings.
