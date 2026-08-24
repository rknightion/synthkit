#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

# Static-only validation for the SKT-0006.03 lab scaffolding. This helper deliberately
# never invokes Docker, k3d, Helm, or kubectl; those tools are reserved for the root
# integration lane.

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
readonly RUN_SCRIPT="$SCRIPT_DIR/run.sh"
readonly VALUES_FILE="$SCRIPT_DIR/k8s-monitoring-values.yaml"
readonly WORKLOAD_MANIFEST="$SCRIPT_DIR/workloads.yaml"
readonly RECEIVER_MANIFEST="$SCRIPT_DIR/receiver.yaml"
readonly CONFORMANCE_SOURCE="$REPO_ROOT/internal/construct/k8scluster/conformance.go"
readonly RECEIVER_SOURCE="$REPO_ROOT/e2e/receiver/receiver.go"

failures=0

pass() {
  printf 'PASS: %s\n' "$*"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  failures=$((failures + 1))
}

require_tool() {
  local tool=$1
  if ! command -v "$tool" >/dev/null 2>&1; then
    fail "required static-check tool is missing: $tool"
  fi
}

check_files() {
  local path
  for path in "$RUN_SCRIPT" "$VALUES_FILE" "$WORKLOAD_MANIFEST" "$RECEIVER_MANIFEST" "$CONFORMANCE_SOURCE" "$RECEIVER_SOURCE"; do
    if [[ -e "$path" ]]; then
      pass "exists: $path"
    else
      fail "missing: $path"
    fi
  done
}

check_shell() {
  if bash -n "$RUN_SCRIPT"; then
    pass "bash syntax: $RUN_SCRIPT"
  else
    fail "bash syntax: $RUN_SCRIPT"
  fi
  if bash -n "$0"; then
    pass "bash syntax: $0"
  else
    fail "bash syntax: $0"
  fi

  if command -v shellcheck >/dev/null 2>&1; then
    if shellcheck --severity=warning "$RUN_SCRIPT" "$0"; then
      pass "shellcheck: lab scripts"
    else
      fail "shellcheck: lab scripts"
    fi
  else
    printf 'SKIP: shellcheck is not installed\n'
  fi
}

check_yaml() {
  local path
  for path in "$VALUES_FILE" "$WORKLOAD_MANIFEST" "$RECEIVER_MANIFEST"; do
    if yq eval '.' "$path" >/dev/null; then
      pass "YAML parse: $path"
    else
      fail "YAML parse: $path"
    fi
  done
}

check_chart_pin() {
  if rg -q 'grafana/k8s-monitoring 4\.4\.0' "$VALUES_FILE" \
    && rg -q 'CHART_VERSION="4\.4\.0"' "$RUN_SCRIPT" \
    && rg -q -- '--version "\$CHART_VERSION"' "$RUN_SCRIPT"; then
    pass "chart 4.4.0 is pinned in values and executable Helm invocation"
  else
    fail "chart 4.4.0 pin is incomplete"
  fi

  if rg -q 'km\.ChartVersion' "$CONFORMANCE_SOURCE"; then
    pass "conformance source still emits blueprint-provided km.ChartVersion"
  else
    fail "conformance source does not emit km.ChartVersion"
  fi
  if rg -q '4\.4\.0' "$CONFORMANCE_SOURCE"; then
    pass "finding: conformance.go contains a chart-version literal; review required"
  else
    pass "finding: conformance.go has no hardcoded chart-version literal (expected audit result)"
  fi
}

check_images() {
  local image
  local image_count=0
  while IFS= read -r image; do
    [[ -n "$image" ]] || continue
    image_count=$((image_count + 1))
    if [[ "$image" =~ @sha256:[0-9a-f]{64}$ ]]; then
      pass "immutable workload image: $image"
    else
      fail "workload image is not digest-pinned: $image"
    fi
  done < <(yq -N -r '.. | select(tag == "!!map" and has("image")) | .image' "$WORKLOAD_MANIFEST")
  if ((image_count >= 2)); then
    pass "workload deck has at least two image references"
  else
    fail "workload deck has fewer than two image references"
  fi
}

check_destinations() {
  local endpoint
  local destination
  local expected_host='https://synthkit-skt000603-receiver.monitoring.svc.cluster.local:9099/'

  if yq -e '(.destinations | keys | sort | join("|")) == "capture-loki|capture-otlp|capture-prometheus"' "$VALUES_FILE" >/dev/null; then
    pass "values define exactly the three capture destinations"
  else
    fail "values contain an unexpected destination or destination omission"
  fi

  while IFS= read -r endpoint; do
    if [[ "$endpoint" == "$expected_host"* ]]; then
      pass "destination endpoint is in-cluster: $endpoint"
    else
      fail "destination endpoint is not the receiver service: $endpoint"
    fi
  done < <(yq -r '.destinations[] | select(has("url")) | .url' "$VALUES_FILE")

  if yq -e '[.. | select(tag == "!!map" and has("urlFrom"))] | length == 0' "$VALUES_FILE" >/dev/null; then
    pass "values contain no dynamic destination URL"
  else
    fail "values contain a dynamic destination URL"
  fi

  while IFS= read -r destination; do
    case "$destination" in
      capture-prometheus|capture-loki|capture-otlp) ;;
      *) fail "feature references unsupported destination: $destination" ;;
    esac
  done < <(
    for feature in clusterMetrics annotationAutodiscovery prometheusOperatorObjects clusterEvents nodeLogs podLogsViaOpenTelemetry applicationObservability kubernetesManifests; do
      yq -r ".${feature}.destinations[]?" "$VALUES_FILE"
    done
  )
  pass "enabled feature destination references are receiver-only"

  if yq -e '
    (.clusterMetrics.destinations | join("|")) == "capture-prometheus"
    and (.annotationAutodiscovery.destinations | join("|")) == "capture-prometheus"
    and (.podLogsViaOpenTelemetry.destinations | join("|")) == "capture-otlp"
    and (.applicationObservability.destinations | join("|")) == "capture-otlp"
  ' "$VALUES_FILE" >/dev/null; then
    pass "metrics and OTLP-log/application lanes are explicitly scoped"
  else
    fail "metrics or OTLP feature destination scope is incorrect"
  fi

  local destination_count
  local tls_destination_count
  destination_count="$(yq -r '.destinations | length' "$VALUES_FILE")"
  tls_destination_count="$(yq -r '[.destinations[] | select(.tls.insecureSkipVerify == true)] | length' "$VALUES_FILE")"
  if ((destination_count > 0 && tls_destination_count == destination_count)); then
    pass "all in-cluster TLS destinations skip verification for ephemeral certificate"
  else
    fail "in-cluster destinations do not all configure ephemeral TLS handling"
  fi
}

check_safety_and_scope() {
  if rg -n 'k3d[[:space:]]+cluster[[:space:]]+delete[[:space:]]+--all|docker[[:space:]]+rm[[:space:]]+-f[[:space:]]+[^$" ]*\*|docker[[:space:]]+system[[:space:]]+prune|kubectl[[:space:]]+delete[^\n]*--all' "$RUN_SCRIPT" >/dev/null; then
    fail "broad cleanup command found in run.sh"
  else
    pass "cleanup is exact-name only"
  fi
  if rg -q 'LAB_CLUSTER_NAME="synthkit-skt000603-k3d"' "$RUN_SCRIPT" \
    && rg -q 'AUX_CONTAINER_NAME="synthkit-skt000603-receiver"' "$RUN_SCRIPT"; then
    pass "fixed collision-resistant lab names are declared"
  else
    fail "fixed lab names are missing"
  fi
  if rg -q 'artifacts/signal-fidelity-k3d' "$RUN_SCRIPT" \
    && rg -q '\$\{LAB_OUTPUT_DIR:-' "$RUN_SCRIPT"; then
    pass "artifacts default outside temp and allow LAB_OUTPUT_DIR override"
  else
    fail "artifact output contract is missing"
  fi
  if rg -q "trap cleanup EXIT" "$RUN_SCRIPT" \
    && rg -q "trap 'exit 130' INT" "$RUN_SCRIPT" \
    && rg -q "trap 'exit 143' TERM" "$RUN_SCRIPT"; then
    pass "EXIT/INT/TERM traps are installed"
  else
    fail "required teardown traps are missing"
  fi
}

check_receiver_contract() {
  local route
  for route in 'POST /api/prom/push' 'POST /otlp/v1/logs' 'GET /__inventory'; do
    if rg -Fq "$route" "$RECEIVER_SOURCE"; then
      pass "receiver route present: $route"
    else
      fail "receiver route missing: $route"
    fi
  done
  if rg -q 'TransportPrometheusRW1' "$RECEIVER_SOURCE" \
    && rg -q 'TransportOTLPLogs' "$RECEIVER_SOURCE"; then
    pass "receiver records RW1 and OTLP-log receipts through inventory constants"
  else
    fail "receiver receipt names are not visible in source"
  fi
}

check_values_sanitized() {
  if rg -ni 'grafana\.net|prometheus-prod|loki-prod|otlp-gateway|GRAFANA_|GC_' "$VALUES_FILE" >/dev/null; then
    fail "hosted Grafana endpoint or cloud credential marker found in values"
  else
    pass "values contain no hosted Grafana endpoint or cloud credential marker"
  fi
  if rg -n '(^|[[:space:]])(password|token|username|bearerToken|secretKeyRef):|GRAFANA_|GC_' "$VALUES_FILE" >/dev/null; then
    fail "credential-bearing key found in values"
  else
    pass "values contain no credential-bearing key"
  fi
  if yq -e '.profiling.enabled == false and .profilesReceiver.enabled == false and .autoInstrumentation.enabled == false' "$VALUES_FILE" >/dev/null; then
    pass "unsupported profiling/profile receiver and auto-instrumentation lanes are disabled"
  else
    fail "unsupported profiling/profile receiver or auto-instrumentation lane is enabled"
  fi
}

main() {
  local tool
  for tool in bash yq rg; do
    require_tool "$tool"
  done
  check_files
  check_shell
  check_yaml
  check_chart_pin
  check_images
  check_destinations
  check_safety_and_scope
  check_receiver_contract
  check_values_sanitized

  if ((failures > 0)); then
    printf 'Static validation failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi
  printf 'Static validation passed. No Docker, k3d, Helm, or kubectl command was run.\n'
}

main "$@"
