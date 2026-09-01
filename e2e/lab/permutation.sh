#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

# One permutation of the k3d capture lab (SKT-0013.04).
#
# This is the WORKER. run.sh is the matrix entrypoint that dispatches several of these in
# parallel under a stated concurrency bound; this script knows nothing about the other jobs.
# It owns exactly one disposable k3d cluster, whose name is fixed and derived from the
# permutation slug, and it always writes exactly one result record — on the success path and on
# every failure path — because a missing result record is indistinguishable from a job that
# never started.
#
# It never writes to reality-corpus/. It writes a normalized candidate inside its own output
# directory; promoting a candidate into the corpus is a separate, deliberate step.

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT

PERMUTATION="${1:-}"
readonly PERMUTATION
if [[ ! "$PERMUTATION" =~ ^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$ ]]; then
  printf 'usage: permutation.sh <permutation-slug>\n' >&2
  exit 2
fi

readonly PERMUTATION_DIR="$SCRIPT_DIR/permutations/$PERMUTATION"
readonly META_FILE="$PERMUTATION_DIR/meta.env"
readonly DEPLOY_SCRIPT="$PERMUTATION_DIR/deploy.sh"
readonly ACCEPTANCE_FILTER="$PERMUTATION_DIR/acceptance.jq"
readonly WORKLOAD_MANIFEST="$SCRIPT_DIR/workloads.yaml"
readonly RECEIVER_MANIFEST="$SCRIPT_DIR/receiver.yaml"
readonly CONFORMANCE_SOURCE="$REPO_ROOT/internal/construct/k8scluster/conformance.go"

# Fixed, collision-resistant, exact resource names. Cleanup never uses a wildcard, --all, or a
# name that came from anywhere but this block.
#
# k3d refuses a cluster name longer than 32 characters, and it refuses it at create time, which
# would present as a create-cluster FAILURE for a permutation whose deployment is fine. The
# prefix is short for that reason and require_commands enforces the limit at preflight.
readonly LAB_CLUSTER_NAME="synthkit-lab-$PERMUTATION"
readonly AUX_CONTAINER_NAME="synthkit-skt000603-receiver-$PERMUTATION"
readonly RECEIVER_IMAGE="${LAB_RECEIVER_IMAGE:-synthkit-skt000603/receiver:4.5.0-lab}"
readonly WORKLOAD_IMAGE="docker.io/library/busybox@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"
readonly RECEIVER_SERVICE="synthkit-skt000603-receiver"
readonly RECEIVER_NAMESPACE="monitoring"
readonly CAPTURE_SUBSTRATE="k3s"

readonly LAB_OUTPUT_DIR="${LAB_OUTPUT_DIR:-$REPO_ROOT/artifacts/signal-fidelity-k3d}"
readonly RUN_ID="${LAB_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
readonly CAPTURED_AT="${LAB_CAPTURED_AT:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
readonly RECEIVER_LOCAL_PORT="${LAB_RECEIVER_LOCAL_PORT:-19099}"
readonly CAPTURE_TIMEOUT_SECONDS="${LAB_CAPTURE_TIMEOUT_SECONDS:-300}"
readonly SKIP_IMAGE_BUILD="${LAB_SKIP_IMAGE_BUILD:-false}"

readonly PERMUTATION_OUTPUT_DIR="$LAB_OUTPUT_DIR/$PERMUTATION"
readonly RESULTS_DIR="${LAB_RESULTS_DIR:-$LAB_OUTPUT_DIR/results}"
readonly RESULT_FILE="$RESULTS_DIR/$PERMUTATION.json"
readonly RAW_INVENTORY="$PERMUTATION_OUTPUT_DIR/inventory-$RUN_ID.json"
readonly CANDIDATE_JSON="$PERMUTATION_OUTPUT_DIR/candidate-$RUN_ID.json"

LAB_TMP=""
PORT_FORWARD_PID=""
LATEST_INVENTORY=""
PHASE="preflight"
OUTCOME=""
FAILURE_REASON=""
CHECKS_JSON="[]"
INSTRUMENT_STATUS="NOT RUN"
TEARDOWN_CONFIRMED="false"
STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
readonly STARTED_AT
STARTED_EPOCH="$(date -u +%s)"
readonly STARTED_EPOCH

# Permutation metadata. Every field is declared by the permutation, never inferred here.
PERMUTATION_TITLE=""
PERMUTATION_SUMMARY=""
PERMUTATION_COLLECTOR=""
PERMUTATION_COLLECTOR_VERSION=""
PERMUTATION_CORPUS_AREAS=""
PERMUTATION_CAPTURE_STATUS=""
PERMUTATION_DEVIATIONS=""

log() {
  printf '[%s] %s\n' "$PERMUTATION" "$*" >&2
}

fail_phase() {
  OUTCOME="failed"
  FAILURE_REASON="$1"
  log "FAILED in phase $PHASE: $1"
  exit 1
}

json_string_array() {
  # Turns a newline-separated list on stdin into a JSON array.
  jq -R -s 'split("\n") | map(select(length > 0))'
}

write_result() {
  local exit_code=$1
  local finished_at duration receipts counts candidate raw
  finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  duration=$(( $(date -u +%s) - STARTED_EPOCH ))

  receipts='{}'
  counts='{"metrics":0,"logs":0,"traces":0}'
  if [[ -s "$RAW_INVENTORY" ]]; then
    receipts="$(jq -c '[.receipts[]? | {key: .protocol, value: (.count // 0)}] | from_entries' "$RAW_INVENTORY" 2>/dev/null || printf '{}')"
    counts="$(jq -c '{metrics: ((.metrics // []) | length), logs: ((.logs // []) | length), traces: ((.traces // []) | length)}' "$RAW_INVENTORY" 2>/dev/null || printf '{"metrics":0,"logs":0,"traces":0}')"
  fi
  candidate=""
  [[ -s "$CANDIDATE_JSON" ]] && candidate="$CANDIDATE_JSON"
  raw=""
  [[ -s "$RAW_INVENTORY" ]] && raw="$RAW_INVENTORY"

  mkdir -p -- "$RESULTS_DIR"
  jq -n \
    --arg result_version "synthkit.lab.permutation-result/v1alpha1" \
    --arg permutation "$PERMUTATION" \
    --arg title "$PERMUTATION_TITLE" \
    --arg summary "$PERMUTATION_SUMMARY" \
    --arg collector "$PERMUTATION_COLLECTOR" \
    --arg collector_version "$PERMUTATION_COLLECTOR_VERSION" \
    --arg substrate "$CAPTURE_SUBSTRATE" \
    --arg cluster "$LAB_CLUSTER_NAME" \
    --arg capture_status "$PERMUTATION_CAPTURE_STATUS" \
    --arg run_id "$RUN_ID" \
    --arg started_at "$STARTED_AT" \
    --arg finished_at "$finished_at" \
    --argjson duration "$duration" \
    --arg outcome "$OUTCOME" \
    --arg phase "$PHASE" \
    --arg failure_reason "$FAILURE_REASON" \
    --argjson exit_code "$exit_code" \
    --argjson capture_window "$CAPTURE_TIMEOUT_SECONDS" \
    --argjson checks "$CHECKS_JSON" \
    --argjson receipts "$receipts" \
    --argjson counts "$counts" \
    --arg candidate "$candidate" \
    --arg raw "$raw" \
    --argjson corpus_areas "$(printf '%s' "$PERMUTATION_CORPUS_AREAS" | tr ' ' '\n' | json_string_array)" \
    --argjson deviations "$(printf '%s' "$PERMUTATION_DEVIATIONS" | tr '|' '\n' | json_string_array)" \
    --argjson diagnostics "$(find "$PERMUTATION_OUTPUT_DIR" -maxdepth 1 -name "diagnostic-*-$RUN_ID.txt" 2>/dev/null | sort | json_string_array)" \
    --arg teardown "$TEARDOWN_CONFIRMED" \
    --arg instrument_status "$INSTRUMENT_STATUS" \
    '{
      result_version: $result_version,
      permutation: $permutation,
      title: $title,
      summary: $summary,
      collector: $collector,
      collector_version: $collector_version,
      substrate: $substrate,
      cluster: $cluster,
      corpus_areas: $corpus_areas,
      capture_status: $capture_status,
      deviations: $deviations,
      run_id: $run_id,
      started_at: $started_at,
      finished_at: $finished_at,
      duration_seconds: $duration,
      outcome: $outcome,
      phase: $phase,
      failure_reason: $failure_reason,
      exit_code: $exit_code,
      capture_window_seconds: $capture_window,
      checks: $checks,
      receipts: $receipts,
      counts: $counts,
      candidate_path: $candidate,
      raw_path: $raw,
      diagnostic_paths: $diagnostics,
      teardown_confirmed: $teardown,
      instrument_evidence: $instrument_status
    }' >"$RESULT_FILE"
}

write_diagnostics() {
  # Named per phase and run so the combined report can point an operator at the exact file.
  # Guarded on this permutation's own kubeconfig: a job that failed before creating its cluster
  # must never fall back to the operator's current kube context.
  [[ -d "$PERMUTATION_OUTPUT_DIR" ]] || return 0
  if [[ -n "${KUBECONFIG:-}" && -f "${KUBECONFIG:-}" ]]; then
    kubectl --request-timeout=30s --namespace "$RECEIVER_NAMESPACE" get pods --output=wide \
      >"$PERMUTATION_OUTPUT_DIR/diagnostic-pods-$RUN_ID.txt" 2>&1 || true
    kubectl --request-timeout=30s --all-namespaces get events --sort-by=.lastTimestamp \
      >"$PERMUTATION_OUTPUT_DIR/diagnostic-events-$RUN_ID.txt" 2>&1 || true
  fi
  # Phase logs live in LAB_TMP, which teardown removes. Copy them out before that happens, or a
  # failure reason points an operator at a file that no longer exists.
  local phase_log
  for phase_log in docker-build k3d-create k3d-import deploy port-forward; do
    if [[ -n "$LAB_TMP" && -f "$LAB_TMP/$phase_log.log" ]]; then
      cp -- "$LAB_TMP/$phase_log.log" "$PERMUTATION_OUTPUT_DIR/diagnostic-$phase_log-$RUN_ID.txt" || true
    fi
  done
}

cleanup() {
  local status=$?
  trap - EXIT

  # Diagnostics first: they need the cluster still alive, and a failed permutation is exactly
  # the case where an operator needs them.
  write_diagnostics

  if [[ "$PORT_FORWARD_PID" =~ ^[0-9]+$ ]]; then
    kill "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
    wait "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
  fi

  # Delete only the exact reserved names for THIS permutation. Never broaden to --all.
  if command -v docker >/dev/null 2>&1; then
    docker rm -f "$AUX_CONTAINER_NAME" >/dev/null 2>&1 || true
  fi
  if command -v k3d >/dev/null 2>&1; then
    k3d cluster delete "$LAB_CLUSTER_NAME" >/dev/null 2>&1 || true
    if k3d cluster list --output json 2>/dev/null | jq -e --arg name "$LAB_CLUSTER_NAME" 'any(.[]?; .name == $name)' >/dev/null 2>&1; then
      TEARDOWN_CONFIRMED="false"
      log "WARNING: cluster $LAB_CLUSTER_NAME still exists after delete"
    else
      TEARDOWN_CONFIRMED="true"
    fi
  fi

  if [[ -n "$LAB_TMP" && -d "$LAB_TMP" ]]; then
    rm -rf -- "$LAB_TMP"
  fi

  # An exit that reached here without a decided outcome is a harness failure by definition:
  # the worker stopped somewhere it did not classify.
  if [[ -z "$OUTCOME" ]]; then
    OUTCOME="failed"
    if [[ -z "$FAILURE_REASON" ]]; then
      FAILURE_REASON="The worker exited in phase $PHASE with status $status without classifying an outcome."
    fi
  fi
  write_result "$status" || true

  log "outcome=$OUTCOME phase=$PHASE teardown_confirmed=$TEARDOWN_CONFIRMED"
  exit "$status"
}

trap cleanup EXIT
trap 'FAILURE_REASON="interrupted"; exit 130' INT
trap 'FAILURE_REASON="terminated"; exit 143' TERM

require_commands() {
  local command_name
  local missing=()
  local required=(docker k3d helm kubectl curl jq openssl grep go)
  for command_name in "${required[@]}"; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
      missing+=("$command_name")
    fi
  done
  if ((${#missing[@]} > 0)); then
    fail_phase "missing required command(s): ${missing[*]}"
  fi
  if ! docker info >/dev/null 2>&1; then
    fail_phase "Docker daemon is unavailable; start Docker and retry"
  fi
  if ! [[ "$RECEIVER_LOCAL_PORT" =~ ^[0-9]+$ ]] || ((RECEIVER_LOCAL_PORT < 1024 || RECEIVER_LOCAL_PORT > 65535)); then
    fail_phase "LAB_RECEIVER_LOCAL_PORT must be an integer in 1024..65535"
  fi
  if ! [[ "$CAPTURE_TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] || ((CAPTURE_TIMEOUT_SECONDS < 30)); then
    fail_phase "LAB_CAPTURE_TIMEOUT_SECONDS must be an integer >= 30"
  fi
  if ! [[ "$RUN_ID" =~ ^[A-Za-z0-9_.-]+$ ]]; then
    fail_phase "LAB_RUN_ID contains unsupported characters: $RUN_ID"
  fi
  if ((${#LAB_CLUSTER_NAME} > 32)); then
    fail_phase "the derived cluster name $LAB_CLUSTER_NAME is ${#LAB_CLUSTER_NAME} characters; k3d rejects anything over 32, so shorten the permutation slug"
  fi
}

load_metadata() {
  local path
  for path in "$META_FILE" "$DEPLOY_SCRIPT" "$ACCEPTANCE_FILTER" "$WORKLOAD_MANIFEST" "$RECEIVER_MANIFEST" "$CONFORMANCE_SOURCE" "$REPO_ROOT/e2e/receiver/Dockerfile"; do
    [[ -f "$path" ]] || fail_phase "required lab input is missing: $path"
  done
  # shellcheck source=/dev/null
  source "$META_FILE"
  [[ -n "$PERMUTATION_TITLE" ]] || fail_phase "$META_FILE does not declare PERMUTATION_TITLE"
  [[ -n "$PERMUTATION_COLLECTOR" ]] || fail_phase "$META_FILE does not declare PERMUTATION_COLLECTOR"
  [[ -n "$PERMUTATION_COLLECTOR_VERSION" ]] || fail_phase "$META_FILE does not declare PERMUTATION_COLLECTOR_VERSION"
  case "$PERMUTATION_CAPTURE_STATUS" in
    proven|unproven) ;;
    *) fail_phase "$META_FILE must declare PERMUTATION_CAPTURE_STATUS as proven or unproven" ;;
  esac

  # The source is intentionally blueprint-configured. Prove the signal still emits
  # km.ChartVersion rather than a literal.
  grep -q 'km\.ChartVersion' "$CONFORMANCE_SOURCE" \
    || fail_phase "conformance.go no longer emits the blueprint-provided km.ChartVersion"
}

generate_tls() {
  PHASE="tls"
  local tls_config="$LAB_TMP/receiver-openssl.cnf"
  TLS_CERT="$LAB_TMP/receiver.crt"
  TLS_KEY="$LAB_TMP/receiver.key"

  cat >"$tls_config" <<EOF
[req]
distinguished_name = req_distinguished_name
x509_extensions = v3_req
prompt = no

[req_distinguished_name]
CN = $RECEIVER_SERVICE

[v3_req]
subjectAltName = @alt_names

[alt_names]
DNS.1 = $RECEIVER_SERVICE
DNS.2 = $RECEIVER_SERVICE.$RECEIVER_NAMESPACE.svc
DNS.3 = $RECEIVER_SERVICE.$RECEIVER_NAMESPACE.svc.cluster.local
DNS.4 = localhost
IP.1 = 127.0.0.1
EOF

  openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 1 \
    -keyout "$TLS_KEY" -out "$TLS_CERT" -config "$tls_config" >/dev/null 2>&1 \
    || fail_phase "could not generate temporary receiver TLS material"
  chmod 600 "$TLS_KEY"
  chmod 644 "$TLS_CERT"
}

delete_stale_exact_resources() {
  PHASE="cleanup-stale"
  log "removing any stale exact lab resources before creation"
  docker rm -f "$AUX_CONTAINER_NAME" >/dev/null 2>&1 || true
  k3d cluster delete "$LAB_CLUSTER_NAME" >/dev/null 2>&1 || true
}

build_receiver() {
  PHASE="build-receiver"
  if [[ "$SKIP_IMAGE_BUILD" == "true" ]]; then
    log "reusing the matrix-built receiver image: $RECEIVER_IMAGE"
    docker image inspect "$RECEIVER_IMAGE" >/dev/null 2>&1 \
      || fail_phase "LAB_SKIP_IMAGE_BUILD=true but $RECEIVER_IMAGE is absent"
    return 0
  fi
  log "building e2e receiver image: $RECEIVER_IMAGE"
  docker build --pull=false --file "$REPO_ROOT/e2e/receiver/Dockerfile" \
    --tag "$RECEIVER_IMAGE" "$REPO_ROOT" >"$LAB_TMP/docker-build.log" 2>&1 \
    || fail_phase "receiver image build failed; see the docker-build diagnostic"
}

create_cluster() {
  PHASE="create-cluster"
  log "creating fixed lab cluster: $LAB_CLUSTER_NAME"
  k3d cluster create "$LAB_CLUSTER_NAME" \
    --servers 1 \
    --agents 1 \
    --wait \
    --k3s-arg "--disable=traefik@server:*" \
    >"$LAB_TMP/k3d-create.log" 2>&1 \
    || fail_phase "k3d cluster create failed; see the k3d-create diagnostic"

  export KUBECONFIG="$LAB_TMP/kubeconfig"
  k3d kubeconfig get "$LAB_CLUSTER_NAME" >"$KUBECONFIG"
  chmod 600 "$KUBECONFIG"
  kubectl cluster-info >/dev/null || fail_phase "the new cluster did not answer cluster-info"
}

import_images() {
  PHASE="import-images"
  log "importing the receiver image and resolving the pinned workload digest"
  docker pull "$WORKLOAD_IMAGE" >/dev/null 2>&1 || fail_phase "could not resolve the pinned workload image digest"
  k3d image import "$RECEIVER_IMAGE" --cluster "$LAB_CLUSTER_NAME" >"$LAB_TMP/k3d-import.log" 2>&1 \
    || fail_phase "k3d image import failed; see the k3d-import diagnostic"
}

deploy_receiver() {
  PHASE="deploy-receiver"
  log "deploying capture receiver with temporary TLS"
  kubectl apply --filename "$RECEIVER_MANIFEST" >/dev/null || fail_phase "receiver manifest apply failed"
  kubectl --namespace "$RECEIVER_NAMESPACE" create secret tls synthkit-skt000603-receiver-tls \
    --cert="$TLS_CERT" --key="$TLS_KEY" --dry-run=client --output=yaml \
    | kubectl apply --filename=- >/dev/null || fail_phase "receiver TLS secret apply failed"
  kubectl --namespace "$RECEIVER_NAMESPACE" rollout status \
    deployment/synthkit-skt000603-receiver --timeout=180s >/dev/null \
    || fail_phase "the capture receiver never became ready"
}

deploy_workloads() {
  PHASE="deploy-workloads"
  log "deploying pinned two-service workload deck"
  kubectl apply --filename "$WORKLOAD_MANIFEST" >/dev/null || fail_phase "workload manifest apply failed"
  kubectl --namespace otel-demo rollout status deployment/lab-catalog --timeout=180s >/dev/null \
    || fail_phase "workload lab-catalog never became ready"
  kubectl --namespace otel-demo rollout status deployment/lab-checkout --timeout=180s >/dev/null \
    || fail_phase "workload lab-checkout never became ready"
}

start_port_forward() {
  PHASE="port-forward"
  log "starting local receiver port-forward on 127.0.0.1:$RECEIVER_LOCAL_PORT"
  kubectl --namespace "$RECEIVER_NAMESPACE" port-forward \
    --address 127.0.0.1 \
    "service/$RECEIVER_SERVICE" "$RECEIVER_LOCAL_PORT:9099" \
    >"$LAB_TMP/port-forward.log" 2>&1 &
  PORT_FORWARD_PID=$!

  local attempts=0
  while ((attempts < 30)); do
    attempts=$((attempts + 1))
    if curl --fail --silent --show-error --cacert "$TLS_CERT" \
      "https://127.0.0.1:$RECEIVER_LOCAL_PORT/__inventory" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$PORT_FORWARD_PID" >/dev/null 2>&1; then
      fail_phase "the receiver port-forward exited before becoming ready; see the port-forward diagnostic"
    fi
    sleep 1
  done
  fail_phase "the receiver port-forward did not become ready on port $RECEIVER_LOCAL_PORT"
}

deploy_collector() {
  PHASE="deploy-collector"
  log "deploying the permutation's collector: $PERMUTATION_COLLECTOR@$PERMUTATION_COLLECTOR_VERSION"
  LAB_PERMUTATION="$PERMUTATION" \
  LAB_PERMUTATION_DIR="$PERMUTATION_DIR" \
  LAB_SHARED_DIR="$SCRIPT_DIR" \
  LAB_RECEIVER_NAMESPACE="$RECEIVER_NAMESPACE" \
  LAB_RECEIVER_SERVICE="$RECEIVER_SERVICE" \
  LAB_RECEIVER_ENDPOINT="https://$RECEIVER_SERVICE.$RECEIVER_NAMESPACE.svc.cluster.local:9099" \
  LAB_CLUSTER_LABEL="synthkit-k3s-lab" \
  LAB_TMP="$LAB_TMP" \
  KUBECONFIG="$KUBECONFIG" \
    bash "$DEPLOY_SCRIPT" >"$LAB_TMP/deploy.log" 2>&1 \
    || fail_phase "the permutation's collector deploy failed; see the deploy diagnostics"
}

fetch_inventory() {
  local temporary="$LAB_TMP/inventory.json.tmp"
  if curl --fail --silent --show-error --cacert "$TLS_CERT" \
    "https://127.0.0.1:$RECEIVER_LOCAL_PORT/__inventory" >"$temporary"; then
    mv -- "$temporary" "$LATEST_INVENTORY"
    return 0
  fi
  return 1
}

evaluate_acceptance() {
  # The permutation's own predicate file returns an array of {name, status, detail}.
  jq -c --from-file "$ACCEPTANCE_FILTER" "$LATEST_INVENTORY" 2>/dev/null || printf '[]'
}

acceptance_satisfied() {
  printf '%s' "$CHECKS_JSON" | jq -e 'length > 0 and all(.[]; .status == "PASS")' >/dev/null 2>&1
}

# record_instrument_evidence never changes the outcome. Whether the pinned collector sends
# remote-write metadata at all is one of the things this lab measures, so an absent declaration
# is a recorded finding, not a broken capture.
#
# It reads the capture that already happened rather than polling for longer. An extra grace
# period here would extend one permutation's observation past another's and quietly break the
# common dwell the cross-permutation comparison depends on; remote-write v1 metadata arrives on
# the producer's own cadence well inside the capture window, and if it does not, that absence is
# the finding.
record_instrument_evidence() {
  PHASE="instruments"
  if [[ -s "$RAW_INVENTORY" ]] && jq -e 'any(.metrics[]?; any(.instrument_types[]?; . != "unknown"))' "$RAW_INVENTORY" >/dev/null 2>&1; then
    INSTRUMENT_STATUS="PASS"
  else
    INSTRUMENT_STATUS="FAIL"
  fi
}

# wait_for_capture is where failure, emptiness and partial evidence are separated. Reaching the
# deadline is NOT a failure: the harness completed its own steps, so what it saw is evidence.
#
# Every permutation observes the WHOLE capture window even after its acceptance predicate is
# already satisfied. Stopping early would make capture depth a function of how quickly a
# permutation happened to satisfy its own checks, and the combined report would then read a
# scrape-timing artefact as a permutation disagreement — a family "present in one and absent in
# another" that is really just a shorter look. A common fixed dwell is what makes the
# cross-permutation comparison mean anything.
wait_for_capture() {
  PHASE="capture"
  local deadline=$((SECONDS + CAPTURE_TIMEOUT_SECONDS))
  local remaining
  local met="false"
  log "observing collector egress for the full ${CAPTURE_TIMEOUT_SECONDS}s capture window"
  while ((SECONDS < deadline)); do
    if ! kill -0 "$PORT_FORWARD_PID" >/dev/null 2>&1; then
      fail_phase "the receiver port-forward exited during the capture window, so no honest observation was made; see the port-forward diagnostics"
    fi
    if fetch_inventory; then
      CHECKS_JSON="$(evaluate_acceptance)"
      if acceptance_satisfied && [[ "$met" == "false" ]]; then
        met="true"
        log "acceptance predicate satisfied; continuing to the end of the common capture window"
      fi
    fi
    remaining=$((deadline - SECONDS))
    log "capture window open (${remaining}s remaining, acceptance_met=$met)"
    sleep 5
  done

  # One last honest read. If the receiver cannot be reached at the end, the harness cannot
  # claim to have observed anything, so this is a failure rather than an empty capture.
  if ! fetch_inventory; then
    fail_phase "the receiver became unreachable at the end of the capture window, so this run cannot distinguish an empty capture from a broken harness"
  fi
  CHECKS_JSON="$(evaluate_acceptance)"
  cp -- "$LATEST_INVENTORY" "$RAW_INVENTORY"

  if acceptance_satisfied; then
    OUTCOME="captured"
    log "CAPTURED: the acceptance predicate held at the end of the common capture window"
    return 0
  fi

  local decoded
  decoded="$(jq -r '([.receipts[]?.count] | add // 0) + ((.metrics // []) | length) + ((.logs // []) | length) + ((.traces // []) | length)' "$RAW_INVENTORY" 2>/dev/null || printf '0')"
  if [[ "$decoded" == "0" ]]; then
    OUTCOME="empty"
    log "EMPTY: the collector deployed and reported ready, then sent nothing for ${CAPTURE_TIMEOUT_SECONDS}s"
  else
    OUTCOME="partial"
    log "PARTIAL: evidence arrived but the acceptance predicate was not satisfied in ${CAPTURE_TIMEOUT_SECONDS}s"
  fi
  return 0
}

normalize_candidate() {
  PHASE="normalize"
  [[ -s "$RAW_INVENTORY" ]] || return 0
  go run "$REPO_ROOT/e2e/lab/cmd/lab-matrix" normalize \
    -in "$RAW_INVENTORY" \
    -out "$CANDIDATE_JSON" \
    -substrate "$CAPTURE_SUBSTRATE" \
    -collector-version "$PERMUTATION_COLLECTOR_VERSION" \
    -captured-at "$CAPTURED_AT" \
    || fail_phase "could not normalize the receiver inventory into a candidate"
}


main() {
  mkdir -p -- "$PERMUTATION_OUTPUT_DIR" "$RESULTS_DIR"
  LAB_TMP="$(mktemp -d "${TMPDIR:-/tmp}/synthkit-lab-$PERMUTATION.XXXXXX")"
  chmod 700 "$LAB_TMP"

  require_commands
  load_metadata
  delete_stale_exact_resources
  generate_tls
  build_receiver
  create_cluster
  import_images
  deploy_receiver
  deploy_workloads
  start_port_forward
  deploy_collector

  LATEST_INVENTORY="$LAB_TMP/latest-inventory.json"
  wait_for_capture
  record_instrument_evidence
  normalize_candidate
  PHASE="complete"
  log "candidate: $CANDIDATE_JSON"
}

main "$@"
