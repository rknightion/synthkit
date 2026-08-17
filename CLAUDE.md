# synthkit — project context

Contributor and agent instructions live in `AGENTS.md`, which Claude Code and Codex both read.
One canonical file means the two cannot drift apart — so edit `AGENTS.md`, never this file.

Until 2026-08-17 this repo used a symlink (`AGENTS.md -> CLAUDE.md`). Anthropic's Claude Code
documentation gives the `@AGENTS.md` import as the primary arrangement and offers a symlink only
as a fallback "if you don't need to add Claude-specific content" — and the import is what every
other repo here uses. Anything Claude-Code-specific belongs below the import in this file;
everything else belongs in `AGENTS.md`.

@AGENTS.md
