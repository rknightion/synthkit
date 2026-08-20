#!/usr/bin/env bash
# Focused metadata checks for operational-skill routing. No checkout or credentials required.
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
pass() { printf 'PASS: %s\n' "$1"; }
description() { sed -n '3p' "$root/$1/agents/openai.yaml"; }

assert_contains() {
  local skill=$1 want=$2 text
  text="$(description "$skill")"
  [[ "$text" == *"$want"* ]] || fail "$skill metadata omits expected trigger wording: $want"
}

assert_not_contains() {
  local skill=$1 unwanted=$2 text
  text="$(description "$skill")"
  [[ "$text" != *"$unwanted"* ]] || fail "$skill metadata over-triggers on: $unwanted"
}

# Positive prompts: each operational action has an explicit synthkit cue.
assert_contains create-blueprint 'blueprint'
assert_contains initial-setup 'Deploy'
assert_contains setup-fleet-management 'Fleet Management'
assert_contains verify-deployment 'Verify'
pass 'positive synthkit operational triggers are present'

# Negative prompts: generic, non-synthkit work must not be advertised as a trigger.
assert_not_contains create-blueprint 'any YAML'
assert_not_contains initial-setup 'any Docker'
assert_not_contains setup-fleet-management 'any collector'
assert_not_contains verify-deployment 'any Grafana'
pass 'generic non-synthkit prompts are excluded'
