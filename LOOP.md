# Loop: synthkit
tier: guarded
gate: just check
ci-required: ci-success
release-on-push: yes
deploy-on-push: no
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
  `internal/archtest` enforces it (`TestCatalogImportIsolation`), and a lane needing a cross-construct
  import stops and returns the question; never widen the allowlist to go green.
- Derive values from a seed unit (generation ID, entity identity), never the wall clock or a global
  RNG: the inventory gate compares two `-once -dump` runs. Counters and histograms are cumulative via
  `internal/state` (push totals); CloudWatch `_sum` series are per-period gauges, never `rate()`d, and
  `internal/cw` owns that. Request-scoped IDs never become labels; an absent dimension is omitted,
  never `""` or `"NA"`. Metrics go out by `sink/promrw` Remote-Write v2; OTLP carries traces only.
- When synthetic output diverges from observed reality, correct the synth, never the captured data.
  Record new observed signals in `signals/<area>.md` with provenance in the same run, and resolve a
  PENDING out of `cantfind.md`. Do not import `SK-N` items into the tracker or renumber them.
- Every environment read in `internal/config` uses `get("LIT")` or `getInt("LIT")` with a string-literal
  key (`TestEnvSurfaceAligned` parses for them); put `.env` comments on their own line.
- Renderer changes are verified by inventory diff (`just dump` against `signals/`), not unit test.
- A new blueprint field or config struct needs `just gen`; `just gen-check` fails on drift. A new `.go`
  file needs the AGPL SPDX header (`just spdx-check`).
- `just race` excludes `internal/integration` on purpose (OOMs a 16 GB runner); `just test` covers it.
  Do not "fix" the exclusion.
- The three measured dominant teardown-forecast tests in `scripts/race-isolated.txt` join the
  existing isolated package invocation because hosted package aggregate work exceeded 600 s even
  after the decoder shortcut. This is separate from caller-margin admission: retain every case,
  `-race`, caller bound and default 600 s package timeout in both invocations. Other forecast tests
  remain in the sweep; prove unchanged exactly-once selection before full final gates and hosted CI.
- Slow tier: `just lab` (about 35 minutes in CI) and `just race` (about 10 minutes). Run once on the
  final candidate, never in the inner loop.
- `cmd/synthkit-control-dash` under `just race` runs against Go's default 600 s test timeout (562 s in
  CI, one 600.953 s failure). Host load above about 10 logical CPUs confounds it: a local timeout
  under load proves nothing, and never relax the timeout or an assertion to get green.
- Keep scratch on a canonical path, never macOS's symlinked `/tmp`: private snapshot path validation
  fails under it.
- Docker `just secret-scan` from a linked worktree cannot read the worktree's git metadata: "0 commits
  scanned" is not a pass. Mount the metadata and confirm the commit count, or leave it to CI.
- The tree is habitually dirty with unrelated work: stage explicit paths, never `git add -A` or
  `git add .`.
- `backlog task edit --notes` / `--plan` bare silently replace the whole section; use
  `--append-notes` / `--append-plan`.
- Release automation is release-please on push to main; the publish workflow also pushes the edge
  `:main` image on every push.

## Mutexes
- gate: synthkit-check | timeout 3600 gaterun -- just check </dev/null
- gate: synthkit-race | timeout 3600 gaterun -- just race </dev/null
- gate: synthkit-check-local | env CI=true timeout 2400 just check </dev/null
- gate: synthkit-e2e | timeout 2400 just e2e </dev/null
- `synthkit-check` and `synthkit-race` run on the remote gate runner; `synthkit-check-local` only
  after gaterun exits 70 or 124 or is missing, with the reason recorded. `synthkit-e2e` needs the
  local Docker daemon, an exclusive resource.
- Single-owner files, serialised: `internal/runner/`, `go.mod`, `go.sum`, shared core, fixture, shape,
  state and ledger types, each `blueprints/*.yaml`, `signals/<area>.md`, generated
  `BLUEPRINT-SCHEMA.md` and `fielddocs.json`, `cantfind.md`, `.env`, `.env.example`,
  `internal/archtest/arch_test.go`.
- `cantfind.md` appends are serialised so two lanes cannot claim the same `SK-N`;
  `internal/archtest/arch_test.go` allowlist changes are an architecture decision, not a lane action.
- Docker (`just e2e`, `just secret-scan`), the live Grafana stacks (the charter names which; never
  assume) and the reference EKS and Windows capture clusters (treat as absent unless just confirmed)
  are exclusive resources. A live capture is read-only and returns findings; the root applies the
  resulting `signals/` edits. Stack names and tenant IDs never enter a task, doc or commit message.

## Lane brief

- A lane runs `go build ./... && go vet ./... && go test ./...` scoped to its own packages. The full
  `just check` (race leg, SPDX sweep, forbidden-words guard) runs once on the integrated tree.
- One construct kind per lane under `internal/construct/<kind>/` and one workload kind per lane under
  `internal/workload/<kind>/` are disjoint by the isolation rule.
