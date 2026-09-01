#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

# Static-only validation for the k3d capture-lab permutation matrix. This helper deliberately
# never invokes Docker, k3d, Helm, or kubectl; it checks the scaffolding a matrix run depends
# on, including the three properties the matrix exists to protect.

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
readonly RUN_SCRIPT="$SCRIPT_DIR/run.sh"
readonly WORKER_SCRIPT="$SCRIPT_DIR/permutation.sh"
readonly VALUES_FILE="$SCRIPT_DIR/k8s-monitoring-values.yaml"
readonly WORKLOAD_MANIFEST="$SCRIPT_DIR/workloads.yaml"
readonly RECEIVER_MANIFEST="$SCRIPT_DIR/receiver.yaml"
readonly PERMUTATIONS_DIR="$SCRIPT_DIR/permutations"
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

permutations() {
  local dir
  for dir in "$PERMUTATIONS_DIR"/*/; do
    [[ -f "$dir/meta.env" ]] || continue
    basename "$dir"
  done | sort
}

check_files() {
  local path
  for path in "$RUN_SCRIPT" "$WORKER_SCRIPT" "$VALUES_FILE" "$WORKLOAD_MANIFEST" \
    "$RECEIVER_MANIFEST" "$CONFORMANCE_SOURCE" "$RECEIVER_SOURCE"; do
    if [[ -e "$path" ]]; then
      pass "exists: $path"
    else
      fail "missing: $path"
    fi
  done
}

check_shell() {
  local script
  local scripts=("$RUN_SCRIPT" "$WORKER_SCRIPT" "$0")
  local permutation
  while IFS= read -r permutation; do
    scripts+=("$PERMUTATIONS_DIR/$permutation/deploy.sh")
  done < <(permutations)

  for script in "${scripts[@]}"; do
    if bash -n "$script"; then
      pass "bash syntax: $script"
    else
      fail "bash syntax: $script"
    fi
  done

  if command -v shellcheck >/dev/null 2>&1; then
    if shellcheck --severity=warning "${scripts[@]}"; then
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
  local permutation
  local paths=("$VALUES_FILE" "$WORKLOAD_MANIFEST" "$RECEIVER_MANIFEST")
  while IFS= read -r permutation; do
    while IFS= read -r path; do
      paths+=("$path")
    done < <(find "$PERMUTATIONS_DIR/$permutation" -maxdepth 1 -name '*.yaml' | sort)
  done < <(permutations)

  for path in "${paths[@]}"; do
    if yq eval '.' "$path" >/dev/null; then
      pass "YAML parse: $path"
    else
      fail "YAML parse: $path"
    fi
  done
}

# Every permutation is a self-describing unit. The matrix reads these fields to build the
# report, so a permutation that omits one would render as a blank row.
check_permutation_contract() {
  local permutation count=0
  while IFS= read -r permutation; do
    count=$((count + 1))
    local dir="$PERMUTATIONS_DIR/$permutation"
    local field
    for field in PERMUTATION_TITLE PERMUTATION_COLLECTOR PERMUTATION_COLLECTOR_VERSION PERMUTATION_CAPTURE_STATUS; do
      if rg -q "^$field=\"" "$dir/meta.env"; then
        pass "$permutation declares $field"
      else
        fail "$permutation does not declare $field"
      fi
    done
    if rg -q '^PERMUTATION_CAPTURE_STATUS="(proven|unproven)"$' "$dir/meta.env"; then
      pass "$permutation declares an explicit capture status"
    else
      fail "$permutation capture status must be exactly proven or unproven"
    fi
    for field in deploy.sh acceptance.jq; do
      if [[ -f "$dir/$field" ]]; then
        pass "$permutation provides $field"
      else
        fail "$permutation is missing $field"
      fi
    done
    if jq -n --from-file "$dir/acceptance.jq" >/dev/null 2>&1; then
      pass "$permutation acceptance predicate parses"
    else
      fail "$permutation acceptance predicate does not parse"
    fi
    # An acceptance predicate must return named checks, not a bare boolean: a partial capture
    # has to be able to say WHICH claim was unmet.
    if rg -Fq 'def check(' "$dir/acceptance.jq" && rg -Fq 'name:' "$dir/acceptance.jq"; then
      pass "$permutation acceptance predicate returns named checks"
    else
      fail "$permutation acceptance predicate does not return named checks"
    fi
    # No permutation may write the corpus. Merging is a deliberate step after the report.
    if rg -q 'reality-corpus' "$dir"; then
      fail "$permutation references reality-corpus; jobs must never write the corpus"
    else
      pass "$permutation does not touch reality-corpus"
    fi
  done < <(permutations)

  if ((count >= 3)); then
    pass "the matrix expresses $count permutations"
  else
    fail "the matrix must express at least the Alloy default, podLogsViaOpenTelemetry and OTel-native receiver permutations; found $count"
  fi

  local expected
  for expected in alloy-default alloy-otlp-podlogs otel-receivers; do
    if [[ -f "$PERMUTATIONS_DIR/$expected/meta.env" ]]; then
      pass "required permutation is expressed: $expected"
    else
      fail "required permutation is missing: $expected"
    fi
  done
}

# The base values carry the chart pin for the Alloy permutations; each deploy.sh carries the
# executable pin. Both must agree with the permutation's declared collector version.
check_chart_pin() {
  local permutation declared
  while IFS= read -r permutation; do
    local dir="$PERMUTATIONS_DIR/$permutation"
    declared="$(sed -n 's/^PERMUTATION_COLLECTOR_VERSION="\(.*\)"$/\1/p' "$dir/meta.env" | head -1)"
    if [[ -n "$declared" ]] && rg -Fq "CHART_VERSION=\"$declared\"" "$dir/deploy.sh"; then
      pass "$permutation pins $declared in both meta.env and its executable Helm invocation"
    else
      fail "$permutation chart pin disagrees between meta.env and deploy.sh"
    fi
    if rg -q -- '--version "\$CHART_VERSION"' "$dir/deploy.sh"; then
      pass "$permutation passes the pin to Helm"
    else
      fail "$permutation does not pass the pin to Helm"
    fi
  done < <(permutations)

  if rg -q 'grafana/k8s-monitoring 4\.5\.0' "$VALUES_FILE"; then
    pass "base values record the Alloy chart pin"
  else
    fail "base values do not record the Alloy chart pin"
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
    pass "base values define exactly the three capture destinations"
  else
    fail "base values contain an unexpected destination or destination omission"
  fi

  while IFS= read -r endpoint; do
    if [[ "$endpoint" == "$expected_host"* ]]; then
      pass "destination endpoint is in-cluster: $endpoint"
    else
      fail "destination endpoint is not the receiver service: $endpoint"
    fi
  done < <(yq -r '.destinations[] | select(has("url")) | .url' "$VALUES_FILE")

  if yq -e '[.. | select(tag == "!!map" and has("urlFrom"))] | length == 0' "$VALUES_FILE" >/dev/null; then
    pass "base values contain no dynamic destination URL"
  else
    fail "base values contain a dynamic destination URL"
  fi

  while IFS= read -r destination; do
    case "$destination" in
      capture-prometheus|capture-loki|capture-otlp) ;;
      *) fail "feature references unsupported destination: $destination" ;;
    esac
  done < <(
    for feature in clusterMetrics annotationAutodiscovery prometheusOperatorObjects clusterEvents nodeLogs applicationObservability kubernetesManifests; do
      yq -r ".${feature}.destinations[]?" "$VALUES_FILE"
    done
  )
  pass "enabled base feature destination references are receiver-only"

  # The permutation axis: exactly one pod-log lane per Alloy overlay, with the other named and
  # explicitly disabled so reading one overlay tells you which lane is under capture.
  local overlay
  for overlay in alloy-default alloy-otlp-podlogs; do
    local path="$PERMUTATIONS_DIR/$overlay/values.yaml"
    [[ -f "$path" ]] || continue
    if yq -e '
      ((.podLogsViaLoki.enabled // false) or (.podLogsViaOpenTelemetry.enabled // false))
      and (((.podLogsViaLoki.enabled // false) and (.podLogsViaOpenTelemetry.enabled // false)) | not)
      and (has("podLogsViaLoki") and has("podLogsViaOpenTelemetry"))
    ' "$path" >/dev/null; then
      pass "$overlay enables exactly one pod-log lane and names the other"
    else
      fail "$overlay must enable exactly one pod-log lane and explicitly disable the other"
    fi
  done

  if yq -e '(has("podLogsViaLoki") | not) and (has("podLogsViaOpenTelemetry") | not)' "$VALUES_FILE" >/dev/null; then
    pass "base values leave the pod-log lane to the permutation overlay"
  else
    fail "base values pin a pod-log lane; that is the permutation axis and belongs in an overlay"
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
  local script
  for script in "$RUN_SCRIPT" "$WORKER_SCRIPT"; do
    if rg -n 'k3d[[:space:]]+cluster[[:space:]]+delete[[:space:]]+--all|docker[[:space:]]+rm[[:space:]]+-f[[:space:]]+[^$" ]*\*|docker[[:space:]]+system[[:space:]]+prune|kubectl[[:space:]]+delete[^\n]*--all' "$script" >/dev/null; then
      fail "broad cleanup command found in $script"
    else
      pass "cleanup is exact-name only: $script"
    fi
  done
  if rg -q 'LAB_CLUSTER_NAME="synthkit-lab-\$PERMUTATION"' "$WORKER_SCRIPT" \
    && rg -q 'AUX_CONTAINER_NAME="synthkit-skt000603-receiver-\$PERMUTATION"' "$WORKER_SCRIPT"; then
    pass "each permutation owns fixed, collision-resistant, permutation-scoped names"
  else
    fail "permutation-scoped fixed lab names are missing"
  fi
  if rg -q 'artifacts/signal-fidelity-k3d' "$RUN_SCRIPT" \
    && rg -q '\$\{LAB_OUTPUT_DIR:-' "$RUN_SCRIPT"; then
    pass "artifacts default outside temp and allow LAB_OUTPUT_DIR override"
  else
    fail "artifact output contract is missing"
  fi
  if rg -q "trap cleanup EXIT" "$WORKER_SCRIPT" \
    && rg -q "trap 'FAILURE_REASON=\"interrupted\"; exit 130' INT" "$WORKER_SCRIPT" \
    && rg -q "trap 'FAILURE_REASON=\"terminated\"; exit 143' TERM" "$WORKER_SCRIPT"; then
    pass "worker EXIT/INT/TERM traps are installed"
  else
    fail "required worker teardown traps are missing"
  fi
  # k3d rejects a cluster name over 32 characters at create time, which would surface as a
  # create-cluster failure for a permutation whose deployment is fine.
  local permutation
  while IFS= read -r permutation; do
    local derived="synthkit-lab-$permutation"
    if ((${#derived} <= 32)); then
      pass "$permutation derives a cluster name k3d accepts (${#derived} chars)"
    else
      fail "$permutation derives cluster name $derived at ${#derived} chars; k3d rejects anything over 32"
    fi
  done < <(permutations)
  if rg -q 'the derived cluster name' "$WORKER_SCRIPT"; then
    pass "the worker enforces the k3d cluster-name limit at preflight"
  else
    fail "the worker does not enforce the k3d cluster-name limit"
  fi
  if rg -q 'k3d cluster list --output json' "$WORKER_SCRIPT"; then
    pass "the worker confirms teardown rather than assuming it"
  else
    fail "the worker does not confirm teardown"
  fi
}

# The three properties SKT-0013.04 exists to protect, checked statically so a later edit cannot
# quietly remove one.
check_matrix_properties() {
  if rg -q 'MAX_PARALLEL="\$\{LAB_MAX_PARALLEL:-[0-9]+\}"' "$RUN_SCRIPT" \
    && rg -q 'MAX_PARALLEL_CAP=[0-9]+' "$RUN_SCRIPT"; then
    pass "the concurrency bound is a declared default with a hard cap"
  else
    fail "the concurrency bound is not declared with a hard cap"
  fi
  if rg -q -- '-parallelism-note' "$RUN_SCRIPT"; then
    pass "the bound's rationale reaches the combined report"
  else
    fail "the combined report is not told why the bound is what it is"
  fi
  # The result record is written from the worker's EXIT trap, so a job that dies anywhere still
  # classifies itself. Without this, a failure and an empty capture both leave nothing behind.
  if rg -q 'write_result "\$status"' "$WORKER_SCRIPT"; then
    pass "every worker exit path writes a result record"
  else
    fail "a worker exit path could leave no result record, making failure indistinguishable from an empty capture"
  fi
  if rg -q 'OUTCOME="empty"' "$WORKER_SCRIPT" && rg -q 'OUTCOME="failed"' "$WORKER_SCRIPT" \
    && rg -q 'OUTCOME="partial"' "$WORKER_SCRIPT" && rg -q 'OUTCOME="captured"' "$WORKER_SCRIPT"; then
    pass "the worker classifies captured, partial, empty and failed distinctly"
  else
    fail "the worker does not distinguish all four outcomes"
  fi
  # Comment lines are allowed to explain the rule; executable lines are not allowed to break it.
  if rg -N --no-filename -e 'reality-corpus' "$RUN_SCRIPT" "$WORKER_SCRIPT" | rg -qv '^\s*#'; then
    fail "a matrix script references reality-corpus outside a comment; parallel jobs must never write the corpus"
  else
    pass "no matrix script writes to reality-corpus; promotion stays a deliberate step"
  fi
  # A worker that dies before its own trap must still leave a row. The dispatcher stakes a
  # pre-classified claim before launching, so a missing row can never be mistaken for a
  # permutation that was not part of the run.
  if rg -q 'write_launched_placeholder "\$candidate"' "$RUN_SCRIPT"; then
    pass "the dispatcher stakes a result record before launching each job"
  else
    fail "a job that dies before its own teardown would vanish from the matrix"
  fi
  if rg -q 'PORT_BASE \+ index' "$RUN_SCRIPT"; then
    pass "each parallel job gets its own local receiver port"
  else
    fail "parallel jobs could contend for one local receiver port"
  fi
}

check_receiver_contract() {
  local route
  for route in 'POST /api/prom/push' 'POST /otlp/v1/logs' 'POST /otlp/v1/metrics' 'GET /__inventory'; do
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
  local path
  local permutation
  local paths=("$VALUES_FILE")
  while IFS= read -r permutation; do
    while IFS= read -r path; do
      paths+=("$path")
    done < <(find "$PERMUTATIONS_DIR/$permutation" -maxdepth 1 -name '*.yaml' | sort)
  done < <(permutations)

  for path in "${paths[@]}"; do
    if rg -ni 'grafana\.net|prometheus-prod|loki-prod|otlp-gateway|GRAFANA_|GC_' "$path" >/dev/null; then
      fail "hosted Grafana endpoint or cloud credential marker found in $path"
    else
      pass "no hosted endpoint or cloud credential marker: $path"
    fi
    if rg -n '(^|[[:space:]])(password|token|username|bearerToken|secretKeyRef):|GRAFANA_|GC_' "$path" >/dev/null; then
      fail "credential-bearing key found in $path"
    else
      pass "no credential-bearing key: $path"
    fi
  done

  if yq -e '.profiling.enabled == false and .profilesReceiver.enabled == false and .autoInstrumentation.enabled == false' "$VALUES_FILE" >/dev/null; then
    pass "unsupported profiling/profile receiver and auto-instrumentation lanes are disabled"
  else
    fail "unsupported profiling/profile receiver or auto-instrumentation lane is enabled"
  fi
}

main() {
  local tool
  for tool in bash yq rg jq; do
    require_tool "$tool"
  done
  check_files
  check_shell
  check_yaml
  check_permutation_contract
  check_chart_pin
  check_images
  check_destinations
  check_safety_and_scope
  check_matrix_properties
  check_receiver_contract
  check_values_sanitized

  if ((failures > 0)); then
    printf 'Static validation failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi
  printf 'Static validation passed. No Docker, k3d, Helm, or kubectl command was run.\n'
}

main "$@"
