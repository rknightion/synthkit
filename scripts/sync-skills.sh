#!/usr/bin/env bash
# Regenerate (or, with --check, verify) the cross-harness skill symlink farm.
#
# Single source of truth: plugins/synthkit/skills/<name>/ (real files).
# Fan-out (committed symlinks):  .claude/skills/<name>  (Claude Code, priority)
#                                .agents/skills/<name>  (Codex; OpenCode also reads .claude)
# Plus AGENTS.md -> CLAUDE.md (Codex/OpenCode house rules).
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

# AGENTS.md -> CLAUDE.md
if [ "$check" -eq 1 ]; then
  [ "$(readlink AGENTS.md 2>/dev/null || true)" = "CLAUDE.md" ] || { note "DRIFT: AGENTS.md should symlink CLAUDE.md"; fail=1; }
else
  [ -e CLAUDE.md ] && { rm -f AGENTS.md; ln -s CLAUDE.md AGENTS.md; }
fi

if [ "$check" -eq 1 ] && [ "$fail" -ne 0 ]; then
  note "FAILED — run scripts/sync-skills.sh to fix"; exit 1
fi
note "ok"
