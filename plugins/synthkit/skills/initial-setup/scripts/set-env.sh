#!/usr/bin/env bash
# Upsert a NON-SECRET key=value into .env. Safe for config flags (DRY_RUN, SYNTHKIT_BIND, *_ENABLED).
# Do NOT use for secrets — the value is a CLI argument and would enter shell/agent context.
set -euo pipefail
key="${1:?usage: set-env.sh KEY VALUE [envfile]}"
val="${2-}"
file="${3:-.env}"
touch "$file"
if grep -q "^${key}=" "$file"; then
  tmp="$(mktemp)"; grep -v "^${key}=" "$file" > "$tmp"; mv "$tmp" "$file"
fi
printf '%s=%s\n' "$key" "$val" >> "$file"
echo "set ${key} in ${file}"
