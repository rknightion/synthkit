#!/usr/bin/env bash
# Regenerate (or, with --check, verify) the cross-harness skill symlink farm.
#
# Single source of truth: plugins/synthkit/skills/<name>/ (real files).
# Fan-out (committed symlinks):  .claude/skills/<name>  (Claude Code, priority)
#                                .agents/skills/<name>  (Codex; OpenCode also reads .claude)
# Repository instructions are canonical in AGENTS.md; CLAUDE.md files import them.
#
# Usage:
#   scripts/sync-skills.sh           regenerate the farm to match the canonical dir
#   scripts/sync-skills.sh --check   exit non-zero if the farm is missing/mistargeted (CI/gate)
#
# Windows / no-symlink fallback: set SYNTHKIT_SKILLS_COPY=1 to copy instead of symlink.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

canon="plugins/synthkit/skills"
targets=(".claude/skills" ".agents/skills")
check=0
[ "${1:-}" = "--check" ] && check=1
copy="${SYNTHKIT_SKILLS_COPY:-0}"

fail=0
note() { echo "sync-skills: $*"; }

# Instruction files are not part of the generated skill farm. Every CLAUDE.md must
# have a regular nearest AGENTS.md and import it explicitly; in particular, the
# root AGENTS.md is a regular canonical file. A normal sync refuses to continue
# when this arrangement is broken rather than repairing it by replacing AGENTS.md.
assert_instruction_arrangement() {
  local bad=0 claude agents

  if [ ! -f AGENTS.md ] || [ -L AGENTS.md ]; then
    note "DRIFT: AGENTS.md must be a regular canonical file"
    bad=1
  fi

  while IFS= read -r claude; do
    [ -n "$claude" ] || continue
    agents="$(dirname "$claude")/AGENTS.md"
    if [ ! -f "$claude" ] || [ -L "$claude" ]; then
      note "DRIFT: $claude must be a regular adapter"
      bad=1
    fi
    if [ ! -f "$agents" ] || [ -L "$agents" ]; then
      note "DRIFT: $claude has no regular nearest AGENTS.md"
      bad=1
    fi
    if [ -f "$claude" ] && ! grep -Eq '^[[:space:]]*@AGENTS\.md[[:space:]]*$' "$claude"; then
      note "DRIFT: $claude must import @AGENTS.md"
      bad=1
    fi
  done < <(find . -path './.git' -prune -o -name CLAUDE.md -print | sort)

  if [ "$bad" -ne 0 ]; then
    fail=1
    if [ "$check" -eq 0 ]; then
      note "FAILED — canonical instruction arrangement is invalid"
      exit 1
    fi
  fi
}

assert_instruction_arrangement

# Discover skill names (dirs under canon that contain SKILL.md).
# NOTE: `mapfile` is bash 4+; macOS ships bash 3.2, so use a read loop instead.
skills=()
while IFS= read -r line; do
  [ -n "$line" ] && skills+=("$line")
done < <(find "$canon" -mindepth 1 -maxdepth 1 -type d -exec test -e '{}/SKILL.md' ';' -print | sort | sed "s#^$canon/##")

if [ "${#skills[@]}" -eq 0 ]; then
  note "no skills found under $canon (nothing to sync)"
fi

want_link() {
  # $1 = link path, $2 = canonical dir (relative to repo root)
  local link="$1" dest="$2"
  local linkdir; linkdir="$(dirname "$link")"
  # relative target from the link's directory to the canonical dir
  local rel; rel="$(python3 -c "import os,sys;print(os.path.relpath(sys.argv[1],sys.argv[2]))" "$dest" "$linkdir")"
  if [ "$check" -eq 1 ]; then
    if [ "$copy" = "1" ]; then
      [ -d "$link" ] && diff -r "$link" "$dest" >/dev/null 2>&1 || { note "DRIFT (copy): $link"; fail=1; }
    else
      [ "$(readlink "$link" 2>/dev/null || true)" = "$rel" ] || { note "DRIFT (link): $link -> expected $rel"; fail=1; }
    fi
  else
    mkdir -p "$linkdir"
    rm -rf "$link"
    if [ "$copy" = "1" ]; then cp -R "$dest" "$link"; else ln -s "$rel" "$link"; fi
  fi
}

for base in "${targets[@]}"; do
  if [ "$check" -eq 0 ]; then mkdir -p "$base"; fi
  if [ "${#skills[@]}" -gt 0 ]; then
    for s in "${skills[@]}"; do
      want_link "$base/$s" "$canon/$s"
    done
  fi
  # detect stale entries (present in farm, absent in canon)
  if [ -d "$base" ]; then
    for existing in "$base"/*; do
      [ -e "$existing" ] || continue
      name="$(basename "$existing")"
      # canon list may be empty; build a newline list safely under `set -u`
      canon_list="$(printf '%s\n' ${skills[@]+"${skills[@]}"})"
      if ! printf '%s\n' "$canon_list" | grep -qx "$name"; then
        note "STALE: $existing has no canonical source"; [ "$check" -eq 1 ] && fail=1
      fi
    done
  fi
done

if [ "$check" -eq 1 ] && [ "$fail" -ne 0 ]; then
  note "FAILED — run scripts/sync-skills.sh to fix"; exit 1
fi
note "ok"
