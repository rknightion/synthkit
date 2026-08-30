#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$SCRIPT_DIR/check.py" "$@"
