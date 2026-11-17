#!/usr/bin/env bash
# Render tests for the synthkit Helm chart.
#
# Rendering permutations is branching logic with real room to be wrong, so the credential and
# exposure permutations are asserted rather than eyeballed. Two halves:
#
#   POSITIVE  charts/synthkit/{ci,ci-ha}/*-values.yaml must render, and assertions check that
#             what came out is what the values asked for.
#   NEGATIVE  charts/synthkit/tests/invalid/*-values.yaml must FAIL to render. A chart that
#             silently accepts a self-obs credential from the synthetic-data Secret, or puts the
#             control plane behind a Service with no acknowledgement, is the specific defect these
#             cover.
#
# Usage: just helm-test (or bash charts/synthkit/tests/render_test.sh)
set -uo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CI_DIR="$CHART_DIR/ci"
HA_CI_DIR="$CHART_DIR/ci-ha"
INVALID_DIR="$CHART_DIR/tests/invalid"

if ! command -v helm >/dev/null 2>&1; then
  echo "FAIL: helm is not on PATH; the chart render tests cannot run" >&2
  exit 1
fi

pass=0
fail=0

ok()   { pass=$((pass+1)); printf '  ok   %s\n' "$1"; }
bad()  { fail=$((fail+1)); printf '  FAIL %s\n' "$1" >&2; }

render() {
  # Helm's compiled-in Kubernetes version is not the chart's compatibility floor.
  # HA fixtures require stable AlwaysAllow; every non-HA fixture retains 1.25.
  local version=1.25.0 arg
  for arg in "$@"; do
    case "$arg" in
      "$HA_CI_DIR"/*-values.yaml|"$INVALID_DIR"/ha-*-values.yaml) version=1.31.0 ;;
    esac
  done
  helm template synthkit-test "$CHART_DIR" --kube-version "$version" "$@" 2>&1
}

# assert_contains <label> <rendered> <pattern...>
assert_contains() {
  local label="$1" out="$2"; shift 2
  local p
  for p in "$@"; do
    if grep -Fq -- "$p" <<<"$out"; then ok "$label: contains '$p'"; else bad "$label: MISSING '$p'"; fi
  done
}

assert_absent() {
  local label="$1" out="$2"; shift 2
  local p
  for p in "$@"; do
    if grep -Fq -- "$p" <<<"$out"; then bad "$label: UNEXPECTED '$p'"; else ok "$label: absent '$p'"; fi
  done
}

# Whole-line match. `kind: Service` is a prefix of `kind: ServiceAccount`, so kind assertions have
# to anchor or they pass for the wrong reason.
assert_absent_kind() {
  local label="$1" out="$2"; shift 2
  local k
  for k in "$@"; do
    if grep -qE "^kind: $k\\$" <<<"$out"; then bad "$label: UNEXPECTED kind $k"; else ok "$label: no $k"; fi
  done
}

# assert_render_failure_contains <label> <expected text> <helm template args...>
# The negative permutation loop below proves every invalid fixture fails. These focused checks
# additionally prove the HTTPRoute/Ingress fixtures fail for the guard they are intended to pin,
# rather than passing because some unrelated validation happened to reject them first.
assert_render_failure_contains() {
  local label="$1" expected="$2"; shift 2
  local out
  if out=$(render "$@"); then
    bad "$label: rendered successfully"
  elif grep -Fq -- "$expected" <<<"$out"; then
    ok "$label: $expected"
  else
    bad "$label: missing '$expected'"
    echo "$out" | tail -5 >&2
  fi
}

# assert_secret_key <label> <rendered> <ENV_VAR> <expected secret name>
# Confirms the env var is projected from exactly the named Secret. The three lines after the env
# entry are `valueFrom:`, `secretKeyRef:` and `name:`.
assert_secret_key() {
  local label="$1" out="$2" env_name="$3" want_secret="$4"
  local got
  got=$(grep -A3 -- "- name: $env_name\$" <<<"$out" | grep -m1 'name: "' | sed 's/.*name: "\(.*\)".*/\1/')
  if [ "$got" = "$want_secret" ]; then
    ok "$label: $env_name <- Secret $want_secret"
  else
    bad "$label: $env_name came from Secret '${got:-<none>}', wanted '$want_secret'"
  fi
}

echo "== HA lifecycle and runtime contract =="
if (cd "$CHART_DIR/../.." && go test ./charts/synthkit/tests -run '^TestHA' -count=1); then
  ok "HA render contract"
else
  bad "HA render contract"
fi

echo "== helm lint =="
if helm lint "$CHART_DIR" >/dev/null 2>&1; then ok "helm lint"; else bad "helm lint"; helm lint "$CHART_DIR"; fi

echo
echo "== positive permutations render =="
for f in "$CI_DIR"/*-values.yaml "$HA_CI_DIR"/*-values.yaml; do
  if out=$(render -f "$f") && [ -n "$out" ]; then
    ok "renders $(basename "$f")"
  else
    bad "renders $(basename "$f")"
    echo "$out" | tail -5 >&2
  fi
done

echo
echo "== default values: control plane closed, no credentials =="
OUT=$(render)
assert_contains "default" "$OUT" \
  'value: "127.0.0.1:8088"' \
  'name: SYNTHKIT_BIND' \
  'value: "127.0.0.1"' \
  'DRY_RUN: "true"' \
  'replicas: 1' \
  'type: Recreate' \
  'kind: PersistentVolumeClaim' \
  'kind: NetworkPolicy' \
  'ingress: []' \
  'readOnlyRootFilesystem: true' \
  'fsGroup: 65532'
assert_absent_kind "default" "$OUT" Service Ingress Job
assert_absent "default" "$OUT" \
  'CONTROL_EXPOSURE_ACK' \
  'secretKeyRef' \
  'livenessProbe' \
  'unhealthyPodEvictionPolicy'

echo
echo "== startup command: compatibility default, bounded opt-in, no readiness change =="
OLD_STARTUP=$'startupProbe:\n            exec:\n              command: ["/app/synthkit", "-healthcheck"]'
NEW_STARTUP=$'startupProbe:\n            exec:\n              command: ["/app/synthkit", "-startupcheck"]'
# grep treats newline-separated patterns as alternatives; use a literal substring for blocks.
assert_probe_block() {
  local label="$1" out="$2" block="$3"
  if [[ "$out" == *"$block"* ]]; then ok "$label"; else bad "$label"; fi
}
assert_probe_block "default startup" "$OUT" "$OLD_STARTUP"
assert_probe_block "default readiness" "$OUT" $'readinessProbe:\n            exec:\n              command: ["/app/synthkit", "-healthcheck"]'
# Compare whole manifests, changing ONLY the startup command. Retain all existing HA/non-HA,
# state and exposure fixtures, including their image selections and probe timing/defaults.
for f in "$CI_DIR"/*-values.yaml "$HA_CI_DIR"/*-values.yaml; do
  BASE=$(render -f "$f")
  if OPT=$(render -f "$f" --set probes.startupCommand=-startupcheck) &&
     [ "$OPT" = "${BASE/"$OLD_STARTUP"/"$NEW_STARTUP"}" ]; then
    ok "startup-only delta $(basename "$f")"
  else
    bad "startup-only delta $(basename "$f")"
  fi
done
# An explicit image is render input, NOT evidence that its binary supports the flag.
IMAGE_ARGS=(--set image.repository=example.invalid/synthkit --set image.tag=startup-fixture --set image.digest=)
BASE=$(render "${IMAGE_ARGS[@]}" --set probes.liveness.enabled=true \
  --set 'extraEnv[0].name=CONTROL_BASE_PATH' --set 'extraEnv[0].value=/synthkit')
if OPT=$(render "${IMAGE_ARGS[@]}" --set probes.liveness.enabled=true \
    --set 'extraEnv[0].name=CONTROL_BASE_PATH' --set 'extraEnv[0].value=/synthkit' \
    --set probes.startupCommand=-startupcheck) &&
   [ "$OPT" = "${BASE/"$OLD_STARTUP"/"$NEW_STARTUP"}" ]; then
  ok "explicit image, base path and liveness: startup-only delta"
else
  bad "explicit image, base path and liveness: startup-only delta"
fi
assert_contains "explicit image" "$OPT" 'example.invalid/synthkit:startup-fixture'
assert_probe_block "liveness unchanged" "$OPT" $'livenessProbe:\n            exec:\n              command: ["/app/synthkit", "-healthcheck"]'
if EXPLICIT=$(render --set probes.startupCommand=-healthcheck) && [ "$EXPLICIT" = "$OUT" ]; then
  ok "explicit compatibility command equals default"
else
  bad "explicit compatibility command equals default"
fi
assert_render_failure_contains "startup command injection" \
  "startupCommand" --set-string 'probes.startupCommand=-startupcheck;echo injected'
assert_render_failure_contains "startup arbitrary command" \
  "startupCommand" --set-string probes.startupCommand=/bin/sh

echo
echo "== credential separation: self-obs never shares the data path =="
OUT=$(render -f "$CI_DIR/03-live-selfobs-separate-stack-values.yaml")
assert_secret_key "selfobs" "$OUT" GC_TOKEN                synthkit-data
assert_secret_key "selfobs" "$OUT" GC_PROM_RW              synthkit-data
assert_secret_key "selfobs" "$OUT" GC_SELF_OTLP_PASSWORD   synthkit-selfobs
assert_secret_key "selfobs" "$OUT" GC_SELF_OTLP_USER       synthkit-selfobs
assert_secret_key "selfobs" "$OUT" GC_PYROSCOPE_PASSWORD   synthkit-selfobs
assert_contains  "selfobs" "$OUT" 'SELFOBS_ENABLED: "true"'

echo
echo "== every lane keeps its own Secret =="
OUT=$(render -f "$CI_DIR/06-all-lanes-values.yaml")
assert_secret_key "all-lanes" "$OUT" GC_TOKEN              synthkit-data
assert_secret_key "all-lanes" "$OUT" GC_PROFILES_USER      synthkit-data
assert_secret_key "all-lanes" "$OUT" GC_SELF_OTLP_PASSWORD synthkit-selfobs
assert_secret_key "all-lanes" "$OUT" GC_FARO_APP_KEY       synthkit-rum
assert_secret_key "all-lanes" "$OUT" GC_SM_TOKEN           synthkit-sm
assert_secret_key "all-lanes" "$OUT" GC_FM_TOKEN           synthkit-fm
assert_secret_key "all-lanes" "$OUT" GC_SIGIL_TOKEN        synthkit-sigil
assert_secret_key "all-lanes" "$OUT" GIT_TOKEN             synthkit-git

echo
echo "== exposure: trusted-network =="
OUT=$(render -f "$CI_DIR/04-exposed-trusted-network-values.yaml")
assert_contains "trusted-network" "$OUT" \
  'kind: Service' \
  'value: "0.0.0.0:8088"' \
  'name: CONTROL_EXPOSURE_ACK' \
  'value: "trusted-network"' \
  '- port: 8088' \
  'kubernetes.io/metadata.name: observability'
assert_secret_key "trusted-network" "$OUT" CONTROL_TOKEN synthkit-control
assert_absent_kind "trusted-network" "$OUT" Ingress
assert_absent "trusted-network" "$OUT" 'ingress: []'

echo
echo "== exposure: tls-proxy behind an Ingress =="
OUT=$(render -f "$CI_DIR/05-exposed-tls-proxy-ingress-values.yaml")
assert_contains "tls-proxy" "$OUT" \
  'kind: Ingress' \
  'kind: Service' \
  'value: "tls-proxy"' \
  'value: "0.0.0.0:8088"' \
  'ingressClassName: "nginx"' \
  'secretName: synthkit-tls'

echo
echo "== exposure: tls-proxy behind a Gateway API HTTPRoute =="
OUT=$(render -f "$CI_DIR/10-exposed-tls-proxy-httproute-values.yaml")
ROUTE=$(awk '/^# Source: synthkit\/templates\/httproute.yaml/,/^---$/' <<<"$OUT")
assert_contains "http-route" "$ROUTE" \
  'apiVersion: gateway.networking.k8s.io/v1' \
  'kind: HTTPRoute' \
  'name: "tailscale"' \
  'namespace: "envoy-gateway-system"' \
  'sectionName: "https"' \
  '- "synthkit.k8s.m7kni.io"' \
  'type: PathPrefix' \
  'value: "/"' \
  'backendRefs:' \
  'name: "synthkit-test"' \
  'port: 8088'
assert_absent "http-route" "$ROUTE" 'filters:'
assert_absent_kind "http-route" "$OUT" Ingress

echo
echo "== state volume =="
OUT=$(render -f "$CI_DIR/07-ephemeral-values.yaml")
assert_contains "ephemeral" "$OUT" 'emptyDir: {}'
assert_absent_kind "ephemeral" "$OUT" PersistentVolumeClaim
assert_absent  "ephemeral" "$OUT" 'persistentVolumeClaim'

OUT=$(render -f "$CI_DIR/09-existing-claim-digest-values.yaml")
assert_contains "existing-claim" "$OUT" \
  'claimName: my-synthkit-state' \
  'ghcr.io/rknightion/synthkit@sha256:0000000000000000000000000000000000000000000000000000000000000000'
assert_absent_kind "existing-claim" "$OUT" PersistentVolumeClaim

echo
echo "== state paths always point at the mounted volume =="
OUT=$(render -f "$CI_DIR/02-live-data-only-values.yaml")
assert_contains "state-paths" "$OUT" \
  'value: /data/control-state.json' \
  'value: /data/blueprints' \
  'mountPath: /data'

echo
echo "== sm-provision Job: least privilege, preview by default =="
OUT=$(render -f "$CI_DIR/08-sm-provision-values.yaml")
JOB=$(awk '/^# Source: synthkit\/templates\/sm-provision-job.yaml/,/^# Source: synthkit\/templates\/(deployment|configmap|pvc|service|networkpolicy|serviceaccount)/' <<<"$OUT")
assert_contains "sm-provision" "$JOB" \
  'kind: Job' \
  'command: ["/app/sm-provision"]' \
  'SM_PROVISION_APPLY' \
  'value: "false"' \
  'podAffinity'
assert_secret_key "sm-provision" "$JOB" GC_SM_TOKEN synthkit-sm
# The Job must NOT receive the emitter's other credentials.
assert_absent "sm-provision" "$JOB" \
  'GC_TOKEN' 'GC_PROM_RW' 'GC_LOKI' 'GC_SELF_OTLP_PASSWORD' 'GC_FM_TOKEN' 'GIT_TOKEN' 'CONTROL_TOKEN'

echo
echo "== rendered manifests validate against the Kubernetes API schemas =="
# KUBE_VERSION pins the schema set. Without it kubeconform validates against the newest schemas
# it can fetch, which passes a manifest that would be rejected by the oldest cluster the chart
# claims to support in Chart.yaml's kubeVersion.
KUBE_VERSION="1.25.0"
# NO -ignore-missing-schemas. That flag skips any resource kubeconform has no schema for, so a leg
# carrying it can pass while validating nothing — the exact failure this suite already had, one
# level down. Built-in resources use kubeconform's default schema; the current HTTPRoute CRD uses
# the catalog location below. If a future template adds another CRD, add its schema location rather
# than reinstating the flag and losing coverage of everything else.
# The Gateway API HTTPRoute is a CRD, so the catalog schema location is required. Leaving this
# out makes kubeconform report "could not find schema" (or adding -ignore-missing-schemas would
# silently skip the route, which is the false pass this leg is meant to prevent).
KUBECONFORM_ARGS=(
  -strict
  -kubernetes-version "$KUBE_VERSION"
  -schema-location default
  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
)
if command -v kubeconform >/dev/null 2>&1; then
  for f in "$CI_DIR"/*-values.yaml "$HA_CI_DIR"/*-values.yaml; do
    # Disjoint groups mirror the reusable CI inputs; no fixture is skipped.
    version="$KUBE_VERSION"
    case "$f" in "$HA_CI_DIR"/*) version=1.31.0 ;; esac
    KUBECONFORM_ARGS[2]="$version"
    if render -f "$f" --kube-version "$version" | kubeconform "${KUBECONFORM_ARGS[@]}" -summary - >/dev/null 2>&1; then
      ok "kubeconform $(basename "$f") against $version"
    else
      bad "kubeconform $(basename "$f") against $version"
      render -f "$f" --kube-version "$version" | kubeconform "${KUBECONFORM_ARGS[@]}" - 2>&1 | tail -5 >&2
    fi
  done
elif [ -n "${REQUIRE_KUBECONFORM:-}" ]; then
  # CI sets this. A leg that silently skips is a leg that has never run: before this, kubeconform
  # was absent on every runner, so the chart's manifests had never once been schema-validated
  # despite the check appearing to be part of the suite.
  bad "kubeconform is required (REQUIRE_KUBECONFORM set) but is not installed"
else
  echo "  skip kubeconform is not installed (set REQUIRE_KUBECONFORM to make this fatal)"
fi

echo
echo "== negative permutations must be refused =="
for f in "$INVALID_DIR"/*-values.yaml; do
  name=$(basename "$f")
  if out=$(render -f "$f"); then
    bad "refuses $name (rendered successfully — the guard is missing)"
  else
    # Two guards can legitimately refuse, and which one fired is worth seeing. `execution error`
    # is a template guard in synthkit.validate — a semantically wrong combination. A schema
    # rejection is values.schema.json catching a key that was never valid to set at all, which the
    # template guards cannot see because the key simply is not there.
    if grep -q 'execution error' <<<"$out"; then
      ok "refuses $name (template guard)"
    elif grep -q "don't meet the specifications of the schema" <<<"$out"; then
      ok "refuses $name (values schema)"
    else
      bad "refuses $name (failed, but not with a chart validation error)"
      echo "$out" | tail -3 >&2
    fi
  fi
done

echo
echo "== exposure guard messages =="
assert_render_failure_contains "same-host Ingress/HTTPRoute" \
  "both publish host" \
  -f "$INVALID_DIR/httproute-ingress-same-host-values.yaml"
assert_render_failure_contains "HTTPRoute trusted-network" \
  "Routed TLS termination requires tls-proxy" \
  -f "$INVALID_DIR/httproute-trusted-network-values.yaml"
assert_render_failure_contains "HTTPRoute without NetworkPolicy peer" \
  "requires at least one networkPolicy.ingressFrom peer" \
  -f "$INVALID_DIR/httproute-without-network-peer-values.yaml"
assert_render_failure_contains "Ingress trusted-network" \
  "a hostname is published through Ingress or HTTPRoute" \
  -f "$INVALID_DIR/ingress-trusted-network-values.yaml"
assert_render_failure_contains "Ingress without NetworkPolicy peer" \
  "requires at least one networkPolicy.ingressFrom peer" \
  -f "$INVALID_DIR/ingress-without-network-peer-values.yaml"

echo
echo "-------------------------------------------"
printf 'passed %d, failed %d\n' "$pass" "$fail"
[ "$fail" -eq 0 ] || exit 1
