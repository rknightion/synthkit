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
#   4. The exact teardown prose is suppressed, but replacing its apparent value with synthetic
#      keys still fires, including lowercase slash-separated keys of the same lengths.
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

# Captured Datadog attribute names are keys, not credentials. Suppression must be exact:
# a neighboring version remains a finding, and a real token shape in this file still fires.
cat >"$fixture/cmd/synthkit/datadog_attributes.json" <<EOF
{"key": "_sampling_priority_v1"}
{"key": "_sampling_priority_rate_v1"}
{"credential": "${grafana_value}"}
{"api_key": "${generic_value}"}
EOF
near_miss_value="$(printf '%s%s' '_sampling_priority_rate_' 'v2')"
cat >"$fixture/cmd/synthkit/datadog_near_miss.json" <<EOF
{"key": "${near_miss_value}"}
EOF

# Read the actual source counterexample instead of copying scanner-matched text into this guard.
# Each negative control retains its surrounding sentence, punctuation and unquoted placement.
python3 - "$repo_root" "$fixture" <<'PY'
import base64
import hashlib
import pathlib
import re
import sys

repo, fixture = map(pathlib.Path, sys.argv[1:])
source = repo.joinpath("docs", "fleet-management.md").read_text()
suffix = " state or an unregister failure exits nonzero. The HA-off teardown"
lines = [line for line in source.splitlines() if line.endswith(suffix)]
assert len(lines) == 1, "expected exactly one teardown source counterexample"
prose = lines[0]
parts = re.fullmatch(r"([^,]+, )([^ ]+)( .*)", prose)
assert parts, "teardown counterexample no longer has its expected comma-separated shape"
prefix, apparent_value, tail = parts.groups()
lengths = [len(word) for word in apparent_value.split("/")]
assert len(lengths) == 2, "expected two slash-separated prose words"

keys = [
    hashlib.sha256(b"teardown hex negative control").hexdigest(),
    base64.b64encode(hashlib.sha256(b"teardown base64 negative control").digest()).decode(),
]
for seed in (b"teardown lowercase control one", b"teardown lowercase control two"):
    digest = hashlib.sha256(seed).digest()
    letters = "".join(chr(ord("a") + byte % 26) for byte in digest)
    keys.append(letters[:lengths[0]] + "/" + letters[lengths[0]:sum(lengths)])
source_lines = [prose] + [prefix + key + tail for key in keys]
fixture.joinpath("cmd", "synthkit", "teardown_prose.md").write_text(
    "\n".join(source_lines + ["+" + line for line in source_lines]) + "\n"
)
PY

docker run --rm -v "$fixture:/scan" -w /scan "$image" \
	dir --redact --no-banner --exit-code 0 --report-format json --report-path /scan/report.json . >/dev/null

detected="$(python3 - "$fixture/report.json" <<'PY'
import json, sys
with open(sys.argv[1]) as fh:
    for f in json.load(fh) or []:
        print(f"{f['RuleID']}\t{f['File']}")
        print(f"{f['RuleID']}\t{f['File']}\t{f['StartLine']}")
PY
)"

fail=0
expect_detected() {
	if printf '%s\n' "$detected" | grep -qxF "$1"; then
		echo "PASS: detected $1"
	else
		echo "FAIL: expected gitleaks to detect $1 — the config is hiding a real finding"
		fail=1
	fi
}
expect_absent() {
	if printf '%s\n' "$detected" | grep -qxF "$1"; then
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
expect_absent "generic-api-key	cmd/synthkit/datadog_attributes.json	1"
expect_absent "generic-api-key	cmd/synthkit/datadog_attributes.json	2"
expect_detected "generic-api-key	cmd/synthkit/datadog_attributes.json	4"
expect_detected "generic-api-key	cmd/synthkit/datadog_near_miss.json	1"
expect_detected "grafana-cloud-api-token	cmd/synthkit/datadog_attributes.json	3"
expect_absent "generic-api-key	cmd/synthkit/teardown_prose.md	1"
expect_absent "generic-api-key	cmd/synthkit/teardown_prose.md	6"
for line in 2 3 4 5 7 8 9 10; do
	expect_detected "generic-api-key	cmd/synthkit/teardown_prose.md	${line}"
done

if [[ "$fail" -ne 0 ]]; then
	echo "gitleaks-config-check: FAILED"
	exit 1
fi
echo "gitleaks-config-check: OK — rule active, exact prose suppressed, synthetic replacements detected, unrelated rules unaffected"
