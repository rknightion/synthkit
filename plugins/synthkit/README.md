# synthkit Claude plugin

Installing this plugin supplies guidance and its helper scripts only. It does not install the
synthkit binary or create a checkout.

Before using a skill that operates on synthkit, clone or otherwise locate a synthkit checkout and
verify its root. The skills use `${CLAUDE_PLUGIN_ROOT}` for plugin-owned helpers and the verified
checkout for repository files such as `.env`, blueprints, and `docker-compose.yml`.

The same canonical skills remain available to Codex from the repository's `.agents/skills` symlinks.

The `contribute` skill covers authorized repository changes, including focused validation, generated
artifacts, signal evidence, and the final local/hosted gate distinction. It uses the same
[contributor guide](../../docs/contributing.md) as human contributors. Operational skills remain
separate: `initial-setup`, `verify-deployment`, `create-blueprint`, and `setup-fleet-management`.
