---
id: doc-0002
title: Wave operating model
type: guide
created_date: '2026-08-14 16:07'
updated_date: '2026-08-29 12:59'
---
# Wave operating model — synthkit

This document carries **only what is specific to this repository**. The campaign model itself —
run contract and run modes, the routing contract, authority and the thread pool, child lane
briefs, external-contract freezing, the unattended blocker contract, the goal-file template and
the pre-flight checklist — lives in the **Agent fan-out protocol (canonical)** doc. Read that
first. Nothing here restates it; if a section below could be pasted into another project
unchanged, it is in the wrong document.

---

## 1. Rules this project added, and the failure behind each

### The three-tier rule is a lane boundary, not just an architecture rule

`internal/construct/<kind>/` and `internal/workload/<kind>/` may import core/fixture/shape/state/
ledger/sink-types and the shared mechanic libs (`internal/cw`, `internal/genai`) **and nothing
else** — never each other, never the blueprint package, never a blueprint name. This is
grep-enforced by `internal/archtest`'s `TestCatalogImportIsolation`, whose allowlist is the
authoritative statement of the boundary.

**Why it matters for a wave:** it is what makes construct and workload lanes safely parallel at
all. 42 construct kinds and 3 workload kinds sit behind that seam, so a wave can fan out across
them with genuinely disjoint file ownership. A lane that "just needs one import" from a sibling
construct is not a shortcut — it collapses the property the fan-out depends on, and the gate
catches it only at the end of the wave, after every lane has already built on the wrong shape.

**Lane brief consequence:** if a lane believes it needs a cross-construct import, that is a STOP
condition. Return the question; do not add to the allowlist to make a build go green.

### Never invent a metric, label, or field name

Every name must be sourced from `signals/<area>.md` or from vendor documentation via ctx7. A name
that cannot be sourced gets a PENDING row in `cantfind.md` and is flagged — it does not get
guessed.

**Why:** the entire value of this generator is that the data it emits is structurally
indistinguishable from the real thing. One invented name is not a cosmetic defect; it is a
dashboard that works against synthkit and breaks against production. This is the single defect
most likely to arrive from a lane that is otherwise doing good work, because inventing a
plausible name is *easier* than sourcing one and looks identical in review.

### `cantfind.md` has its own ID space and it is not the tracker

`cantfind.md` is the PENDING register for signals whose exact shape is unconfirmed. Its `SK-N`
IDs are stable, never renumbered, never reused, and cited across `signals/`, code comments and
commit messages.

**Do not import `SK-N` items into the tracker as tasks, and do not renumber them.** Doing so
creates a second ID space over the same history — a `SYN-NNNN` that can never be made to match
the `SK-N` already cited in a dozen files. When a wave resolves a PENDING, the resolution is
recorded in `cantfind.md` and `signals/<area>.md` as the file's own rules describe; a *task* may
point at `SK-N`, but `SK-N` remains the identifier.

### The realism direction is one-way

When synthetic output diverges from observed reality, **the synth is corrected, never the
captured data**. Real observability data discovered through any pathway — live capture, exporter
inspection, metric-stream output, vendor docs — is recorded in `signals/<area>.md` with its
provenance and date, including signals not previously listed, and the constructs, fixtures and
structs are updated to match.

**Why it needs saying to a lane:** a lane holding a failing comparison has two ways to make it
pass, and the wrong one is faster. A lane that "fixed" a capture to match the generator has
destroyed the only ground truth in the repo.

### Secrets live only in `.env`, and the env surface is gate-enforced

Every environment read in `internal/config` goes through `get("LIT")` / `getInt("LIT")` with a
**string-literal** key, because `TestEnvSurfaceAligned` parses for those literals to keep four
surfaces aligned: Go reads, `.env.example`, `docker-compose.yml` interpolations, and the local
`.env`. A computed or variable key silently drops out of the check.

Comments in `.env` / `.env.example` go on their **own line** — Docker's `env_file` does not strip
an inline `value # comment`, so an inline comment becomes part of the value.

---

## 2. Recurring defects in this codebase

These are the things that have actually gone wrong here. A lane brief that omits them gets them
back.

**A new `.go` file without the SPDX header.** `just spdx-check` requires
`// SPDX-License-Identifier: AGPL-3.0-only` on **line 1** of every tracked `.go` file, including
files that also carry a `//go:build` constraint (the constraint goes on line 3). Vendored
`*.pb.go` are excluded. This fails the gate at the very end of a wave, after every lane has
finished, and it fails for *every* lane that created a file — so it reads as one broken thing
when it is N independent omissions.

**Blueprint field changes without regenerating the schema.** Any change to a blueprint field or
to a construct/workload config struct requires `just gen`; `TestSchemaCurrent` (in
`internal/blueprintschema`, inside the ordinary `test` leg) fails on drift. The regeneration is a
**wiring-pass action**, not a lane action — `BLUEPRINT-SCHEMA.md` and the embedded `fielddocs.json`
are single-owner files and several lanes changing fields means one regen at the end, not N.

**Non-determinism that only shows up across two runs.** Values must be derived from a seed unit
(generation ID, entity identity) rather than from wall clock or a global RNG, because the
inventory gate compares two `-once -dump` runs and any wall-clock-derived decision flips between
them. A lane that uses `rand` without a derived seed passes its own unit test and fails the
inventory diff.

**High-cardinality keys reaching label position.** Request-scoped IDs never become Mimir labels or
Loki stream labels; the Loki sink asserts this. Related and equally easy to get wrong: an absent
dimension is **omitted**, never `""` and never `"NA"`.

**Cumulative-vs-delta confusion.** Counters and histograms are cumulative across ticks via
`internal/state`; push totals, never deltas. Separately, CloudWatch `_sum` series are **per-period
gauges** and must never be `rate()`d — the five-stat expansion, the gauge rule and per-suffix
label isolation all live in `internal/cw` and every AWS construct delegates there rather than
re-deriving.

**The OTel SDK creeping onto the synthetic path.** Metrics go to `sink/promrw` as Remote-Write v2
with final pre-mangled names; OTLP carries traces only, hand-encoded. `internal/selfobs` is the
sole sanctioned OTel SDK user and instruments the *process*, not the data. Constructs and
workloads must never import the OTel SDK, `selfobs`, or profiling.

**The race leg's deliberate exclusion.** `just race` excludes `internal/integration` — it builds
the full estate and OOM-reaps a 16 GB runner under the race detector's shadow memory. That package
still runs under the plain `test` target. A lane "fixing" the exclusion to be thorough breaks CI
with a SIGTERM/143 that does not look like an OOM.

---

## 3. Lane conventions and exclusive resources

**Naturally disjoint (fan out freely):** one construct kind per lane under
`internal/construct/<kind>/`, one workload kind per lane under `internal/workload/<kind>/`. The
import-isolation rule is what guarantees these do not collide.

**Single-owner wiring files — never edited by two lanes, and never by a lane at all if a wiring
pass exists:**

- `internal/runner/` — the composition root and the explicit registry.
- `go.mod` / `go.sum`.
- The fixture vocabulary and shared `core`/`shape`/`state`/`ledger` types.
- `blueprints/*.yaml` — one blueprint is one owner. When several lanes need blueprint changes,
  give each lane a *different* blueprint, or serialize the edits through the wiring pass.
- `signals/<area>.md` — serialize. Multiple lanes discovering signals in the same area is common
  and the file is prose, so it merges badly.
- `BLUEPRINT-SCHEMA.md` + the embedded `fielddocs.json` — generated; owned by the regen step.
- `cantfind.md` — append-only register with a stable ID space; serialize appends so two lanes
  cannot claim the same `SK-N`.
- `.env` / `.env.example` — four-surface alignment means a partial edit fails the gate.
- `internal/archtest/arch_test.go` — the allowlist. Changing it is an architecture decision, not a
  lane action.

**Exclusive resources — at most one lane may hold each:**

- **Live Grafana Cloud stacks.** Capture and verification lanes go to one of the project's two
  stacks; **which one is stated in the run contract and never assumed** — ask. Live-capture lanes
  are read-only and return **findings**, not repo writes: the main thread applies the resulting
  `signals/` edits, because those files are serialized above. Stack names, tenant IDs and namespaces
  are identifiers — they go in the run contract, never into a task, a doc or a commit message.
- **Docker.** `just e2e` and `just secret-scan` both need it; one at a time.
- **The reference EKS / Windows capture clusters** are provisioned on demand and torn down. Treat
  any of them as absent unless a lane has just confirmed otherwise, and never assume a previous
  wave left one running.

**Verification a lane runs itself:** the packages it owns. `go build ./... && go vet ./... &&
go test ./...` scoped to its own packages. **The full `just check` is a wiring-pass action**, once,
at the end — it includes the race leg, the SPDX sweep and the forbidden-words guard, and running
it per lane is minutes of wall clock buying nothing a scoped run did not already prove.

---

## 4. Ownership, and the escape hatch

One file, one owner, for the whole wave — not per stage. A lane that finds it must touch a file it
does not own has exactly two moves, and inventing a third is the failure this section exists to
prevent:

1. **If the file is in the single-owner list above:** stop, and return the required change as a
   precise diff-shaped request to the main thread. Do not edit it. Do not work around it by
   duplicating the declaration somewhere the lane does own — that is how a second source of truth
   gets created, and it survives review because both copies look correct in isolation.
2. **If the file is another lane's and not on the list:** return it as a **hand-off note**, naming
   the file, the change, and why the lane needs it. The main thread either serializes the edit or
   reassigns ownership.

**A boundary with no escape hatch is a stop condition wearing a safety label.** The escape hatch
above is what makes "do not touch it" a workable instruction rather than a lane that quietly
stalls or quietly cheats.

**Lanes never commit.** The campaign root owns every commit, always, including in this repo where
pushing straight to `main` is the norm for small work. A lane that commits has bypassed the gate.

**A lane that hits a decision its brief does not cover STOPS and returns the question.** One
round-trip is cheaper than the rewrite. This is worth repeating in every synthkit lane brief
specifically because the signal-sourcing rule creates a constant temptation to decide — a lane
that cannot source a name is *supposed* to stop, and a lane that invents one has produced work
that looks finished and is worse than nothing.

---

## 5. Branching

Significant work — a new signal type, a new construct or workload, a new sink, anything
cross-cutting the architecture — is built on a **feature branch and submitted as a PR**. Fixes,
CI and chore work, doc tweaks and single-construct touch-ups go straight to `main`. A wave is
almost always in the first category; decide it in the run contract, not at commit time.

Green — `just check` — is mandatory before every commit, as **evidence**, not assertion. Paste the
output.

---

## 6. Run-end against this tracker

The run's record is task state, not a file:

- Landed work is `Done`, with the commit SHA in the task's final summary, finalized in **one**
  call (`backlog task edit SYN-NNNN --check-ac 1 --check-ac 2 -s Done`).
- Blocked work is `Parked` with a **concrete resume boundary** — the file, the decision that was
  missing, and what the next session must establish before it can continue. "Blocked on review" is
  not a resume boundary.
- Untouched work stays `To Do` and needs no action; that is the point of the status.
- Work discovered mid-run becomes a new task labelled `needs-triage`, not a comment on an
  unrelated task.
- A signal discovered mid-run is recorded in `signals/<area>.md` with provenance and date, and a
  resolved PENDING is moved out of `cantfind.md` — **in the same wave**, not deferred. This is the
  one class of finding that is expensive to re-derive, because it usually cost a live capture.

The closing message goes to the terminal as a covering note: **what did this run learn that no
single task captures.** Nothing durable may live only there. Writing it is the last unit of work,
and it is a terminal action — nobody asks for it.
