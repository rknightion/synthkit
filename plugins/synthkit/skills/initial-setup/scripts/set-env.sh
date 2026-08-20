#!/usr/bin/env bash
# Upsert a NON-SECRET key=value into .env. Safe for config flags (DRY_RUN, SYNTHKIT_BIND, *_ENABLED).
# Do NOT use for secrets — the value is a CLI argument and would enter shell/agent context.
set -euo pipefail
umask 077

usage() {
  printf 'usage: set-env.sh KEY VALUE [envfile]\n' >&2
  exit 2
}

die() {
  printf 'set-env.sh: %s\n' "$1" >&2
  exit 1
}

if (( $# < 2 || $# > 3 )); then
  usage
fi

key=$1
val=$2
file=${3:-.env}

# Keep this validation before any target-file access. The fixed pattern never contains user input.
[[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die 'invalid environment key'

# This helper writes unquoted dotenv values. Reject inputs that parseEnvFile or Docker env_file
# would trim, truncate, or reinterpret instead of silently changing their meaning.
[[ "$val" != *$'\n'* && "$val" != *$'\r'* ]] || die 'value must not contain newlines'
[[ "$val" != "${val#${val%%[![:space:]]*}}" || -z "$val" ]] &&
  die 'value must not start with whitespace'
[[ "$val" != "${val%${val##*[![:space:]]}}" || -z "$val" ]] &&
  die 'value must not end with whitespace'
[[ "$val" != \#* && "$val" != *' #'* ]] || die 'value must not contain dotenv comment syntax'
[[ "$val" != \"* && "$val" != \'* ]] || die 'value must not start with a quote'

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
printf '%s=%s\n' "$key" "$val" >> "$tmp"
chmod 600 "$tmp"

# A same-filesystem rename atomically publishes the mode-0600 file. Leave tmp set so
# cleanup also removes it if mv fails or the process is interrupted during the call.
mv -f "$tmp" "$target"
tmp=''

printf 'set %s in %s\n' "$key" "$file"
