# synthkit Claude plugin

Installing this plugin supplies guidance and its helper scripts only. It does not install the
synthkit binary or create a checkout.

Before using a skill that operates on synthkit, clone or otherwise locate a synthkit checkout and
verify its root. The skills use `${CLAUDE_PLUGIN_ROOT}` for plugin-owned helpers and the verified
checkout for repository files such as `.env`, blueprints, and `docker-compose.yml`.

The same canonical skills remain available to Codex from the repository's `.agents/skills` symlinks.
