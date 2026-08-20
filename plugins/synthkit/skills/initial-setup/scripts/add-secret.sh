#!/usr/bin/env bash
# Securely append a SECRET to .env without it passing through any LLM/agent context.
# Run this YOURSELF in your terminal:  bash scripts/add-secret.sh GC_TOKEN
# The value is read with hidden input and written straight to .env; it is never printed.
set -euo pipefail
umask 077

usage() {
  printf 'usage: add-secret.sh KEY [envfile]\n' >&2
  exit 2
}

die() {
  printf 'add-secret.sh: %s\n' "$1" >&2
  exit 1
}

if (( $# < 1 || $# > 2 )); then
  usage
fi

key=$1
file=${2:-.env}

# Keep this validation before any target-file access. The fixed pattern never contains user input.
[[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die 'invalid environment key'

# The secret is read from stdin and is never placed in an argument or diagnostic.
if ! IFS= read -r -s -p "Paste value for ${key} (input hidden): " value; then
  printf '\n' >&2
  die 'failed to read secret value'
fi
printf '\n' >&2

target=$file
if [[ "$target" != /* ]]; then
  target="./$target"
fi

target_dir=${target%/*}
[[ -n "$target_dir" ]] || target_dir=/
[[ -d "$target_dir" ]] || die 'target directory does not exist'
if [[ -e "$target" && ! -f "$target" ]]; then
  die 'target is not a regular file'
fi

device_of() {
  if [[ "$(uname -s)" == Darwin ]]; then
    stat -f '%d' "$1"
  else
    stat -c '%d' "$1"
  fi
}

tmp=''
cleanup() {
  local status=$?
  trap - EXIT HUP INT TERM
  if [[ -n "$tmp" ]]; then
    rm -f "$tmp" || :
  fi
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

if [[ "$target_dir" == / ]]; then
  tmp_template='/.synthkit-env.XXXXXXXX'
else
  tmp_template="${target_dir%/}/.synthkit-env.XXXXXXXX"
fi
tmp="$(mktemp "$tmp_template")"
chmod 600 "$tmp"

# Refuse a cross-filesystem move: mv would copy-and-remove there, which is not atomic.
[[ "$(device_of "$tmp")" == "$(device_of "$target_dir")" ]] ||
  die 'temporary directory is on a different filesystem'

if [[ -e "$target" ]]; then
  # index() is literal matching; the validated key is never interpolated as a regexp.
  awk -v env_key="$key" 'index($0, env_key "=") != 1 { print }' "$target" > "$tmp"
fi
printf '%s=%s\n' "$key" "$value" >> "$tmp"
chmod 600 "$tmp"

# A same-filesystem rename atomically publishes the mode-0600 file. Leave tmp set so
# cleanup also removes it if mv fails or the process is interrupted during the call.
mv -f "$tmp" "$target"
tmp=''

unset value
printf 'Wrote %s to %s (value not shown).\n' "$key" "$file"
