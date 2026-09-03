#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Guard for .gitleaks.toml. The config narrows the default `generic-api-key` rule, and every
# failure mode of that narrowing is SILENT: a too-broad allowlist hides a real credential and the
# scan still exits 0. So this asserts four properties against a synthetic fixture tree, using the
# same pinned gitleaks image the secret-scan leg uses.
#
#   1. `generic-api-key` still fires. Merging a `[[rules]]` block by id must not disable the
#      inherited rule.
#   2. Each allowlisted value is suppressed — the two redaction canaries and a construct-test
#      metric name.
#   3. An unrelated rule still fires INSIDE an allowlisted file. This is the property a `paths`
#      allowlist silently breaks: verified on v8.30.1, a `paths` criterion applies on its own even
#      under `condition = "AND"`, so scoping by file rather than by value hides everything in it.
#
# The fixture values are synthetic and are DERIVED AT RUN TIME from the plain seeds below rather
# than written out as literals. A literal here would be a real finding in this tracked file and
# would turn the repository's own full-history secret-scan leg red. Nothing here is or ever was a
# live credential.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image="${1:?usage: gitleaks-config-check.sh <gitleaks-image-ref>}"

fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

mkdir -p "$fixture/internal/construct/vpccni" "$fixture/cmd/synthkit"
cp "$repo_root/.gitleaks.toml" "$fixture/.gitleaks.toml"

# Derived fixtures. `generic_value` is a hex digest — high entropy, matched by no allowlist regex.
# `grafana_value` is the Grafana Cloud access-policy token shape, whose own dedicated rule must
# keep firing regardless of what this config does to generic-api-key.
generic_value="$(python3 -c '
import hashlib
print(hashlib.sha256(b"synthkit gitleaks fixture, not a credential").hexdigest()[:34])
')"
grafana_value="$(python3 -c '
import base64, json
body = json.dumps({"o": "0000000", "n": "fixture-only", "k": "fixture-only-sentinel-value"})
print("glc" + "_" + base64.b64encode(body.encode()).decode())
')"
# The two redaction canaries are base64 of the literal strings "raw-secret" and "self-secret".
canary_raw="$(printf 'raw-secret' | base64)"
canary_self="$(printf 'self-secret' | base64)"

# 1. Must be DETECTED: a generic high-entropy value matching no allowlist regex.
cat >"$fixture/cmd/synthkit/detect_me.go" <<EOF
package main

const apiToken = "${generic_value}"
EOF

# 2. Must be SUPPRESSED: the two redaction canaries, and the construct-test metric names.
cat >"$fixture/cmd/synthkit/canaries_test.go" <<EOF
package main

var canaries = []string{"${canary_raw}", "${canary_self}"}
EOF
cat >"$fixture/internal/construct/vpccni/vpccni_test.go" <<'EOF'
package vpccni

var families = []string{
	"awscni_aws_api_error_count",
	"awscni_ec2api_req_count",
}
EOF

# 3. Must be DETECTED: an unrelated rule's secret in the same construct-test file as the
#    suppressed metric names.
cat >>"$fixture/internal/construct/vpccni/vpccni_test.go" <<EOF

var notACredential = "${grafana_value}"
EOF

docker run --rm -v "$fixture:/scan" -w /scan "$image" \
	dir --no-banner --exit-code 0 --report-format json --report-path /scan/report.json . >/dev/null

detected="$(python3 - "$fixture/report.json" <<'PY'
import json, sys
with open(sys.argv[1]) as fh:
    for f in json.load(fh) or []:
        print(f"{f['RuleID']}\t{f['File']}")
PY
)"

fail=0
expect_detected() {
	if printf '%s\n' "$detected" | grep -qF "$1"; then
		echo "PASS: detected $1"
	else
		echo "FAIL: expected gitleaks to detect $1 — the config is hiding a real finding"
		fail=1
	fi
}
expect_absent() {
	if printf '%s\n' "$detected" | grep -qF "$1"; then
		echo "FAIL: expected $1 to be allowlisted — the secret-scan leg will be red"
		fail=1
	else
		echo "PASS: suppressed $1"
	fi
}

expect_detected "generic-api-key	cmd/synthkit/detect_me.go"
expect_detected "grafana-cloud-api-token	internal/construct/vpccni/vpccni_test.go"
expect_absent "generic-api-key	cmd/synthkit/canaries_test.go"
expect_absent "generic-api-key	internal/construct/vpccni/vpccni_test.go"

if [[ "$fail" -ne 0 ]]; then
	echo "gitleaks-config-check: FAILED"
	exit 1
fi
echo "gitleaks-config-check: OK — rule active, allowlists scoped by value, unrelated rules unaffected"
