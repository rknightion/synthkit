#!/usr/bin/env bash
# Securely append a SECRET to .env without it passing through any LLM/agent context.
# Run this YOURSELF in your terminal:  bash scripts/add-secret.sh GC_TOKEN
# The value is read with hidden input and written straight to .env; it is never printed.
set -euo pipefail
key="${1:?usage: add-secret.sh KEY [envfile]}"
file="${2:-.env}"
touch "$file"
if grep -q "^${key}=" "$file"; then
  tmp="$(mktemp)"; grep -v "^${key}=" "$file" > "$tmp"; mv "$tmp" "$file"
fi
read -rsp "Paste value for ${key} (input hidden): " v; echo
printf '%s=%s\n' "$key" "$v" >> "$file"
unset v
echo "Wrote ${key} to ${file} (value not shown)."
