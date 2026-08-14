---
id: doc-0003
title: Closed GitHub issues (pre-migration index)
type: other
created_date: '2026-08-14 16:08'
updated_date: '2026-08-14 16:08'
---
# Closed GitHub issues — index

synthkit tracked its work in **GitHub Issues** until **2026-08-14**, when open work moved to this
Backlog.md board. This document indexes every issue that was **closed** before the migration, so the
history is readable from the checkout alone.

**The issues still exist.** Nothing was deleted — synthkit had only two issues in total, so there was
nothing worth archiving-and-deleting. Full bodies, comments and cross-references are live at
`gh issue view <N>` or `https://github.com/rknightion/synthkit/issues/<N>`. This index is a pointer,
not a replacement.

**Two ID spaces, no overlap.** Historical work is cited as `#NN` and keeps its GitHub numbering
forever; new work is `SKT-NNNN`. Never renumber a `#NN` into a task ID — the `#NN` is already cited
in commit messages and issue cross-links, and a `SKT-NNNN` could never be made to match it.

**The GitHub tracker stays open, deliberately.** External contributors can still file issues, and
Renovate's dependency dashboard (`#9`) lives there and is recreated on every run. Anything arriving
that way becomes an `SKT-NNNN` task; the board, not the issue, is where it gets worked.

## Closed issues

| # | Title | Closed | Reason | Resulting work |
|---|---|---|---|---|
| [#26](https://github.com/rknightion/synthkit/issues/26) | Docs site: redesign & rebrand alignment + SEO/LLM discoverability | 2026-07-03 | completed | Landed later, **2026-08-08 → 2026-08-10**, via the fleet-wide inverted docs model rather than the per-repo overrides the issue specified — see below. Range `3a8bb1e`…`a75ec94`. |

### `#26` — what actually shipped, and why it does not match the issue

The issue was closed `COMPLETED` on 2026-07-03, but **no code landed that day**; the only commit in
that window is the unrelated release `8da8ae8`. The work landed a month later, and it landed
*differently*, which is the part worth keeping:

The issue specified per-repo SEO plumbing — a `docs/overrides/main.html` `extrahead` override
emitting canonical / OpenGraph / Twitter / JSON-LD server-side, a per-repo `robots.txt`, and a
per-repo pinned zensical version. **That approach was superseded.** synthkit adopted the m7kni.io
**inverted docs model** (`3a8bb1e`, 2026-08-08): the hub owns the SEO template, the brand stylesheet,
the fonts, the project icon and the social card, and injects them into a clone at build time,
generating `zensical.toml` from this repo's `docs.toml`. Those injected paths are **gitignored here on
purpose** — a tracked copy is drift, and preventing exactly that is what the manifest model is for.

So the issue's per-repo override tasks are not "unfinished"; they were **made obsolete** by the hub
owning them. Do not re-open them by re-adding `docs/overrides/` or a tracked `zensical.toml` to this
repo. The remaining commits in the range are the content side, which the issue also asked for and
which did ship here: `830a9d3` positioning page, `79ed716` FAQ + security page, `50559d5` AGPL
`license_note`, `8388082` + `3872de1` fleet icon and social card, `b690f69` + `a632a33` link fixes,
and `1ccf84b`…`a75ec94` the README and landing-page pass.

## Not indexed here

- **`#9` Dependency Dashboard** is **open**, not closed — a Renovate bot artefact, not project work.
  It is not a task and must not become one; Renovate recreates it.
- **`cantfind.md`** is not issue history. It is the live PENDING register for unconfirmed signals,
  with its own stable `SK-N` ID space that is never renumbered or reused. It stays exactly where it
  is and is deliberately **not** mirrored onto this board — see the *Wave operating model* doc.
