#!/usr/bin/env bash
# Focused tests for the initial-setup environment helpers.
# Every test uses a temporary directory and fictional values; it never reads .env.
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
set_env="$script_dir/set-env.sh"
add_secret="$script_dir/add-secret.sh"
original_path="$PATH"
fictional_secret='fictional-secret-value'
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/synthkit-env-helper-tests.XXXXXX")"

cleanup_tests() {
  rm -rf -- "$work_dir"
}
trap cleanup_tests EXIT HUP INT TERM

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

pass() {
  printf 'PASS: %s\n' "$1"
}

mode_of() {
  case "$(uname -s)" in
    Darwin) stat -f '%Lp' "$1" ;;
    *) stat -c '%a' "$1" ;;
  esac
}

assert_mode() {
  local file=$1 expected=$2 actual
  actual="$(mode_of "$file")"
  [[ "$actual" == "$expected" ]] || fail "${file} has mode ${actual}, expected ${expected}"
}

assert_contents() {
  local file=$1 expected=$2 expected_file
  expected_file="$(mktemp "$work_dir/expected.XXXXXX")"
  printf '%s' "$expected" > "$expected_file"
  cmp -s "$expected_file" "$file" || fail "unexpected contents in ${file}"
}

snapshot() {
  local file=$1 snapshot_file=$2
  cp "$file" "$snapshot_file"
  printf '%s' "$(mode_of "$file")"
}

assert_unchanged() {
  local file=$1 snapshot_file=$2 expected_mode=$3
  cmp -s "$snapshot_file" "$file" || fail "${file} bytes changed after a rejected operation"
  assert_mode "$file" "$expected_mode"
}

new_case_dir() {
  mktemp -d "$work_dir/case.XXXXXX"
}

run_add_secret() {
  local output_file=$1 error_file=$2 key=$3 file=$4
  if ! printf '%s\n' "$fictional_secret" |
    "$add_secret" "$key" "$file" >"$output_file" 2>"$error_file"; then
    return 1
  fi
  if grep -Fq "$fictional_secret" "$output_file" "$error_file"; then
    fail 'add-secret exposed the fictional secret in command output'
  fi
}

test_set_env_first_write() {
  local case_dir file
  case_dir="$(new_case_dir)"
  file="$case_dir/env"

  "$set_env" FIRST_WRITE fictional-value "$file" >"$case_dir/out" 2>"$case_dir/err" ||
    fail 'set-env first write failed'
  assert_contents "$file" $'FIRST_WRITE=fictional-value\n'
  assert_mode "$file" 600
  pass 'set-env first write creates a mode-0600 file'
}

test_add_secret_first_write() {
  local case_dir file
  case_dir="$(new_case_dir)"
  file="$case_dir/env"

  run_add_secret "$case_dir/out" "$case_dir/err" FIRST_SECRET "$file" ||
    fail 'add-secret first write failed'
  assert_contents "$file" "FIRST_SECRET=${fictional_secret}"$'\n'
  assert_mode "$file" 600
  pass 'add-secret first write creates a mode-0600 file without exposing the value'
}

test_set_env_replacement_and_duplicates() {
  local case_dir file
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  printf '%s\n' 'KEEP=one' 'DUPLICATE=old' 'DUPLICATE=stale' 'KEEP_TWO=two' > "$file"
  chmod 640 "$file"

  "$set_env" DUPLICATE fresh-value "$file" >"$case_dir/out" 2>"$case_dir/err" ||
    fail 'set-env duplicate replacement failed'
  assert_contents "$file" $'KEEP=one\nKEEP_TWO=two\nDUPLICATE=fresh-value\n'
  assert_mode "$file" 600
  pass 'set-env replaces every duplicate and appends one current value'
}

test_add_secret_replacement_and_duplicates() {
  local case_dir file
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  printf '%s\n' 'KEEP=one' 'DUPLICATE=old' 'DUPLICATE=stale' 'KEEP_TWO=two' > "$file"
  chmod 640 "$file"

  run_add_secret "$case_dir/out" "$case_dir/err" DUPLICATE "$file" ||
    fail 'add-secret duplicate replacement failed'
  assert_contents "$file" "KEEP=one
KEEP_TWO=two
DUPLICATE=${fictional_secret}
"
  assert_mode "$file" 600
  pass 'add-secret replaces every duplicate and keeps the value out of output'
}

test_set_env_sole_line() {
  local case_dir file
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  printf '%s\n' 'SOLE=old-value' > "$file"
  chmod 640 "$file"

  "$set_env" SOLE new-value "$file" >"$case_dir/out" 2>"$case_dir/err" ||
    fail 'set-env sole-line replacement exits non-zero'
  assert_contents "$file" $'SOLE=new-value\n'
  assert_mode "$file" 600
  pass 'set-env replaces a sole line'
}

test_add_secret_sole_line() {
  local case_dir file
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  printf '%s\n' 'SOLE_SECRET=old-value' > "$file"
  chmod 640 "$file"

  run_add_secret "$case_dir/out" "$case_dir/err" SOLE_SECRET "$file" ||
    fail 'add-secret sole-line replacement exits non-zero'
  assert_contents "$file" "SOLE_SECRET=${fictional_secret}"$'\n'
  assert_mode "$file" 600
  pass 'add-secret replaces a sole line without exposing the value'
}

assert_invalid_key_for_set_env() {
  local key=$1 label=$2 case_dir file before mode_before
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  printf '%s\n' 'KEEP=untouched' 'BADxKEY=must-survive' > "$file"
  chmod 640 "$file"
  before="$case_dir/before"
  mode_before="$(snapshot "$file" "$before")"

  if "$set_env" "$key" fictional-value "$file" >"$case_dir/out" 2>"$case_dir/err"; then
    fail "set-env accepted ${label} key"
  fi
  assert_unchanged "$file" "$before" "$mode_before"
  pass "set-env rejects ${label} keys before touching the target"
}

assert_invalid_key_for_add_secret() {
  local key=$1 label=$2 case_dir file before mode_before
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  printf '%s\n' 'KEEP=untouched' 'BADxKEY=must-survive' > "$file"
  chmod 640 "$file"
  before="$case_dir/before"
  mode_before="$(snapshot "$file" "$before")"

  if run_add_secret "$case_dir/out" "$case_dir/err" "$key" "$file"; then
    fail "add-secret accepted ${label} key"
  fi
  assert_unchanged "$file" "$before" "$mode_before"
  pass "add-secret rejects ${label} keys before touching the target"
}

test_invalid_keys() {
  assert_invalid_key_for_set_env 'BAD.*KEY' 'regex-metacharacter'
  assert_invalid_key_for_set_env $'BAD\nKEY' 'newline'
  assert_invalid_key_for_set_env '1BAD' 'leading-digit'
  assert_invalid_key_for_set_env 'BAD=KEY' 'equals-sign'
  assert_invalid_key_for_add_secret 'BAD.*KEY' 'regex-metacharacter'
  assert_invalid_key_for_add_secret $'BAD\nKEY' 'newline'
  assert_invalid_key_for_add_secret '1BAD' 'leading-digit'
  assert_invalid_key_for_add_secret 'BAD=KEY' 'equals-sign'
}

assert_invalid_value_for_set_env() {
  local value=$1 label=$2 case_dir file before mode_before
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  printf '%s\n' 'KEEP=untouched' > "$file"
  chmod 640 "$file"
  before="$case_dir/before"
  mode_before="$(snapshot "$file" "$before")"

  if "$set_env" SAFE_KEY "$value" "$file" >"$case_dir/out" 2>"$case_dir/err"; then
    fail "set-env accepted ${label} value"
  fi
  assert_unchanged "$file" "$before" "$mode_before"
  pass "set-env rejects ${label} values before touching the target"
}

test_invalid_values() {
  assert_invalid_value_for_set_env $'first\nINJECTED=second' 'newline'
  assert_invalid_value_for_set_env $'first\rsecond' 'carriage-return'
  assert_invalid_value_for_set_env ' leading' 'leading-whitespace'
  assert_invalid_value_for_set_env 'trailing ' 'trailing-whitespace'
  assert_invalid_value_for_set_env 'value # comment' 'inline-comment'
  assert_invalid_value_for_set_env '#comment' 'comment-only'
  assert_invalid_value_for_set_env '"quoted"' 'double-quoted'
  assert_invalid_value_for_set_env "'quoted'" 'single-quoted'
}

make_failing_mv() {
  local bin_dir=$1
  mkdir -p "$bin_dir"
  printf '%s\n' '#!/usr/bin/env bash' 'exit 73' > "$bin_dir/mv"
  chmod 755 "$bin_dir/mv"
}

make_temp_wrapper() {
  local bin_dir=$1 real_mktemp
  real_mktemp="$(command -v mktemp)"
  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf '%s\n' 'set -euo pipefail'
    printf '%s\n' 'tmp="$($REAL_MKTEMP "$1")"'
    printf '%s\n' 'chmod 600 "$tmp"'
    printf '%s\n' 'printf "%s\\n" "$tmp"'
  } > "$bin_dir/mktemp"
  chmod 755 "$bin_dir/mktemp"
  printf '%s' "$real_mktemp"
}

assert_no_env_temps() {
  local directory=$1
  if find "$directory" -name '.synthkit-env.*' -type f -print -quit | grep -q .; then
    fail "environment temporary files remain in ${directory}"
  fi
}

test_set_env_failure_cleanup() {
  local case_dir file before mode_before tmp_dir bin_dir real_mktemp
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  tmp_dir="$case_dir/tmp"
  bin_dir="$case_dir/bin"
  mkdir "$tmp_dir"
  printf '%s\n' 'TARGET=old-value' 'KEEP=one' > "$file"
  chmod 640 "$file"
  before="$case_dir/before"
  mode_before="$(snapshot "$file" "$before")"
  make_failing_mv "$bin_dir"
  real_mktemp="$(make_temp_wrapper "$bin_dir")"

  if env PATH="$bin_dir:$original_path" TMPDIR="$tmp_dir" REAL_MKTEMP="$real_mktemp" \
    "$set_env" TARGET new-value "$file" >"$case_dir/out" 2>"$case_dir/err"; then
    fail 'set-env unexpectedly succeeded when atomic rename failed'
  fi
  assert_unchanged "$file" "$before" "$mode_before"
  assert_no_env_temps "$case_dir"
  pass 'set-env cleans its temporary file and preserves the target on rename failure'
}

test_add_secret_failure_cleanup() {
  local case_dir file before mode_before tmp_dir bin_dir real_mktemp
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  tmp_dir="$case_dir/tmp"
  bin_dir="$case_dir/bin"
  mkdir "$tmp_dir"
  printf '%s\n' 'TARGET_SECRET=old-value' 'KEEP=one' > "$file"
  chmod 640 "$file"
  before="$case_dir/before"
  mode_before="$(snapshot "$file" "$before")"
  make_failing_mv "$bin_dir"
  real_mktemp="$(make_temp_wrapper "$bin_dir")"

  if env PATH="$bin_dir:$original_path" TMPDIR="$tmp_dir" REAL_MKTEMP="$real_mktemp" \
    bash -c 'printf "%s\\n" "$1" | "$2" "$3" "$4"' bash "$fictional_secret" "$add_secret" TARGET_SECRET "$file" \
    >"$case_dir/out" 2>"$case_dir/err"; then
    fail 'add-secret unexpectedly succeeded when atomic rename failed'
  fi
  if grep -Fq "$fictional_secret" "$case_dir/out" "$case_dir/err"; then
    fail 'add-secret exposed the fictional secret during rename failure'
  fi
  assert_unchanged "$file" "$before" "$mode_before"
  assert_no_env_temps "$case_dir"
  pass 'add-secret cleans its temporary file and preserves the target on rename failure'
}

make_hanging_mv() {
  local bin_dir=$1
  mkdir -p "$bin_dir"
  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf '%s\n' 'printf "%s" "$$" > "$MV_PID_FILE"'
    printf '%s\n' "trap 'exit 143' TERM INT"
    printf '%s\n' 'while :; do sleep 1; done'
  } > "$bin_dir/mv"
  chmod 755 "$bin_dir/mv"
}

run_and_interrupt_set_env() {
  local case_dir=$1 file=$2 tmp_dir=$3 bin_dir=$4 real_mktemp=$5
  local pid mv_pid found=0
  env PATH="$bin_dir:$original_path" TMPDIR="$tmp_dir" REAL_MKTEMP="$real_mktemp" MV_PID_FILE="$case_dir/mv.pid" \
    "$set_env" TARGET new-value "$file" >"$case_dir/out" 2>"$case_dir/err" &
  pid=$!
  for _ in {1..500}; do
    if [[ -s "$case_dir/mv.pid" ]] && find "$case_dir" -name '.synthkit-env.*' -type f -print -quit | grep -q .; then
      found=1
      break
    fi
    sleep 0.01
  done
  [[ "$found" == 1 ]] || {
    kill -KILL "$pid" 2>/dev/null || :
    wait "$pid" 2>/dev/null || :
    fail 'set-env did not reach the atomic rename before interruption test timeout'
  }
  kill -TERM "$pid" 2>/dev/null || :
  mv_pid="$(<"$case_dir/mv.pid")"
  kill -TERM "$mv_pid" 2>/dev/null || :
  wait "$pid" 2>/dev/null || :
}

run_and_interrupt_add_secret() {
  local case_dir=$1 file=$2 tmp_dir=$3 bin_dir=$4 real_mktemp=$5
  local pid mv_pid found=0
  printf '%s\n' "$fictional_secret" > "$case_dir/input"
  env PATH="$bin_dir:$original_path" TMPDIR="$tmp_dir" REAL_MKTEMP="$real_mktemp" MV_PID_FILE="$case_dir/mv.pid" \
    "$add_secret" TARGET_SECRET "$file" <"$case_dir/input" >"$case_dir/out" 2>"$case_dir/err" &
  pid=$!
  for _ in {1..500}; do
    if [[ -s "$case_dir/mv.pid" ]] && find "$case_dir" -name '.synthkit-env.*' -type f -print -quit | grep -q .; then
      found=1
      break
    fi
    sleep 0.01
  done
  [[ "$found" == 1 ]] || {
    kill -KILL "$pid" 2>/dev/null || :
    wait "$pid" 2>/dev/null || :
    fail 'add-secret did not reach the atomic rename before interruption test timeout'
  }
  kill -TERM "$pid" 2>/dev/null || :
  mv_pid="$(<"$case_dir/mv.pid")"
  kill -TERM "$mv_pid" 2>/dev/null || :
  wait "$pid" 2>/dev/null || :
}

test_set_env_interrupt_cleanup() {
  local case_dir file before mode_before tmp_dir bin_dir real_mktemp
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  tmp_dir="$case_dir/tmp"
  bin_dir="$case_dir/bin"
  mkdir "$tmp_dir"
  printf '%s\n' 'TARGET=old-value' 'KEEP=one' > "$file"
  chmod 640 "$file"
  before="$case_dir/before"
  mode_before="$(snapshot "$file" "$before")"
  make_hanging_mv "$bin_dir"
  real_mktemp="$(make_temp_wrapper "$bin_dir")"

  run_and_interrupt_set_env "$case_dir" "$file" "$tmp_dir" "$bin_dir" "$real_mktemp"
  assert_unchanged "$file" "$before" "$mode_before"
  assert_no_env_temps "$case_dir"
  pass 'set-env removes the temporary file when interrupted during rename'
}

test_add_secret_interrupt_cleanup() {
  local case_dir file before mode_before tmp_dir bin_dir real_mktemp
  case_dir="$(new_case_dir)"
  file="$case_dir/env"
  tmp_dir="$case_dir/tmp"
  bin_dir="$case_dir/bin"
  mkdir "$tmp_dir"
  printf '%s\n' 'TARGET_SECRET=old-value' 'KEEP=one' > "$file"
  chmod 640 "$file"
  before="$case_dir/before"
  mode_before="$(snapshot "$file" "$before")"
  make_hanging_mv "$bin_dir"
  real_mktemp="$(make_temp_wrapper "$bin_dir")"

  run_and_interrupt_add_secret "$case_dir" "$file" "$tmp_dir" "$bin_dir" "$real_mktemp"
  if grep -Fq "$fictional_secret" "$case_dir/out" "$case_dir/err"; then
    fail 'add-secret exposed the fictional secret during interruption'
  fi
  assert_unchanged "$file" "$before" "$mode_before"
  assert_no_env_temps "$case_dir"
  pass 'add-secret removes the temporary file when interrupted during rename'
}

test_control_token_recipe() {
  local skill_file="$script_dir/../SKILL.md"
  if grep -Eq 'grep -v .*CONTROL_TOKEN|\.env\.tmp' "$skill_file"; then
    fail 'SKILL.md still contains the unsafe CONTROL_TOKEN .env.tmp recipe'
  fi
  grep -Fq 'set -o pipefail; openssl rand -hex 24 | bash plugins/synthkit/skills/initial-setup/scripts/add-secret.sh CONTROL_TOKEN .env' "$skill_file" ||
    fail 'SKILL.md does not use a pipefail-protected add-secret.sh CONTROL_TOKEN pipeline'
  pass 'SKILL.md routes CONTROL_TOKEN through a pipefail-protected add-secret.sh pipeline'
}

run_all() {
  test_set_env_first_write
  test_add_secret_first_write
  test_set_env_replacement_and_duplicates
  test_add_secret_replacement_and_duplicates
  test_set_env_sole_line
  test_add_secret_sole_line
  test_invalid_keys
  test_invalid_values
  test_set_env_failure_cleanup
  test_add_secret_failure_cleanup
  test_set_env_interrupt_cleanup
  test_add_secret_interrupt_cleanup
  test_control_token_recipe
}

case "${1:-all}" in
  all) run_all ;;
  first-write) test_set_env_first_write; test_add_secret_first_write ;;
  replacement) test_set_env_replacement_and_duplicates; test_add_secret_replacement_and_duplicates ;;
  sole-line) test_set_env_sole_line; test_add_secret_sole_line ;;
  invalid) test_invalid_keys; test_invalid_values ;;
  cleanup) test_set_env_failure_cleanup; test_add_secret_failure_cleanup; test_set_env_interrupt_cleanup; test_add_secret_interrupt_cleanup ;;
  skill) test_control_token_recipe ;;
  *)
    printf 'usage: %s [all|first-write|replacement|sole-line|invalid|cleanup|skill]\n' "$0" >&2
    exit 2
    ;;
esac
