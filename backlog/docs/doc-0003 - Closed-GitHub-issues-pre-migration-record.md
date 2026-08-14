---
id: doc-0003
title: Closed GitHub issues (pre-migration record)
type: other
created_date: '2026-08-14 16:08'
updated_date: '2026-08-14 16:52'
---
# Closed GitHub issues — the record

synthkit tracked its work in **GitHub Issues** until **2026-08-14**, when open work moved to this
Backlog.md board.

**`#26` was deleted from GitHub on 2026-08-14, deliberately and without an archive.** `gh issue
view 26` now 404s and the body is not recoverable. **This document is therefore the record, not a
pointer** — everything below is what survives. It was the only issue synthkit ever closed.

**Two ID spaces, no overlap.** Historical work is cited as `#NN` and keeps its GitHub numbering
forever; new work is `SKT-NNNN`. Never renumber a `#NN` into a task ID — the `#NN` is still cited in
commit messages and cross-repo links, and a `SKT-NNNN` could never be made to match it.

**The GitHub tracker stays open, deliberately.** External contributors can still file issues, and
Renovate's dependency dashboard (`#9`) lives there and is recreated on every run. Anything arriving
that way becomes an `SKT-NNNN` task; the board, not the issue, is where it gets worked.

## `#26` — Docs site: redesign & rebrand alignment + SEO/LLM discoverability

Opened and closed **2026-07-03** (`completed`), by the repo owner, with no comments. Part of the
fleet-wide docs effort tracked in `rknightion/m7kni-net-site#28` (Phase 2). It bundled two things
because they touched the same files: a brand/redesign alignment, and a set of SEO/LLM
discoverability improvements.

**What it asked for:**

- Adopt the m7kni.io brand (wordmark, palette, type tokens) but tuned for **documentation, not
  marketing** — clean, dense, readable, strong nav/TOC/search — with the visual spec deferred to the
  hub redesign rather than front-run.
- Server-render the head tags via a `docs/overrides/main.html` `extrahead` override: canonical,
  `theme-color`, OpenGraph, Twitter `summary_large_image`, `robots`, read from `config.extra.*` +
  `page.meta.*`. The existing override was an empty passthrough, so any such metadata was absent or
  injected client-side and therefore invisible to non-JS crawlers and LLM fetchers.
- Per-page JSON-LD `Article` (explicitly **not** `TechArticle`) plus a repo-level
  `SoftwareSourceCode`/`SoftwareApplication` node and a site-wide `Organization`.
- Remove any client-side `seo.js`, superseded by the server-side rendering.
- A GEO content-shape pass on the prose: definition-first sentences, self-contained chunkable
  `##`/`###` sections that restate their subject instead of leaning on "it"/"this", config and env
  reference as **tables** rather than prose, language-tagged code fences, one H1 per page.
- A `docs/robots.txt` allowing the current AI user-agents.
- Align the pinned zensical version with the fleet decision in `m7kni-net-site#29` — explicitly
  "follow the fleet approach, don't invent a per-repo variant".

It also noted that `llms.txt` / `llms-full.txt` / sitemaps are generated **centrally by the hub**
over this repo's Markdown, and are not per-repo work here. That is still true.

### What actually shipped, and why it does not match

The issue was closed `completed` on 2026-07-03, but **no code landed that day** — the only commit in
that window is the unrelated release `8da8ae8`. The work landed a month later, **2026-08-08 →
2026-08-10**, and it landed *differently*. This is the part worth keeping:

synthkit adopted the m7kni.io **inverted docs model** (`3a8bb1e`, 2026-08-08). The hub now owns the
SEO template, the brand stylesheet, the fonts, the project icon and the social card, and injects them
into a clone at build time, generating `zensical.toml` from this repo's `docs.toml`. Those injected
paths are **gitignored here on purpose** — a tracked copy is drift, and preventing exactly that is
what the manifest model is for.

So the issue's per-repo override tasks are **not unfinished — they were made obsolete** by the hub
owning them. Do not re-open them by re-adding `docs/overrides/`, a per-repo `seo.js`, or a tracked
`zensical.toml`. The rest of the range is the content side, which the issue also asked for and which
did ship here: `830a9d3` positioning page, `79ed716` FAQ + security page, `50559d5` AGPL
`license_note`, `8388082` + `3872de1` fleet icon and social card, `b690f69` + `a632a33` link fixes,
and `1ccf84b`…`a75ec94` the README and landing-page pass.

## Not indexed here

- **`#9` Dependency Dashboard** is **open**, not closed — a Renovate bot artefact, not project work.
  It is not a task and must not become one; Renovate recreates it.
- **`cantfind.md`** is not issue history. It is the live PENDING register for unconfirmed signals,
  with its own stable `SK-N` ID space that is never renumbered or reused. It stays exactly where it
  is and is deliberately **not** mirrored onto this board — see the *Wave operating model* doc.
