#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

# One-shot k3d capture lab for SKT-0006.03.
#
# This script is intentionally the only owner of the lab's mutable orchestration. It
# creates one fixed, reserved cluster name, deploys the existing e2e receiver, points
# grafana/k8s-monitoring 4.4.0 at that receiver, waits for positive collector-egress
# receipts, writes a provenance-stamped inventory candidate and findings report, then
# tears down only the exact cluster/container names reserved below.

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
readonly VALUES_FILE="$SCRIPT_DIR/k8s-monitoring-values.yaml"
readonly WORKLOAD_MANIFEST="$SCRIPT_DIR/workloads.yaml"
readonly RECEIVER_MANIFEST="$SCRIPT_DIR/receiver.yaml"
readonly CONFORMANCE_SOURCE="$REPO_ROOT/internal/construct/k8scluster/conformance.go"

# These names are deliberately fixed and scoped to this task. Cleanup never uses a
# wildcard, --all, or a user-provided resource name.
readonly LAB_CLUSTER_NAME="synthkit-skt000603-k3d"
readonly AUX_CONTAINER_NAME="synthkit-skt000603-receiver"
readonly RECEIVER_IMAGE="synthkit-skt000603/receiver:4.4.0-lab"
readonly WORKLOAD_IMAGE="docker.io/library/busybox@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"
readonly CHART_REPO_NAME="grafana"
readonly CHART_REPO_URL="https://grafana.github.io/helm-charts"
readonly CHART_REF="grafana/k8s-monitoring"
readonly CHART_VERSION="4.4.0"
readonly HELM_RELEASE="synthkit-k8s-monitoring"
readonly RECEIVER_SERVICE="synthkit-skt000603-receiver"
readonly RECEIVER_NAMESPACE="monitoring"
readonly CAPTURE_SUBSTRATE="k3s"

readonly LAB_OUTPUT_DIR="${LAB_OUTPUT_DIR:-$REPO_ROOT/artifacts/signal-fidelity-k3d}"
readonly RUN_ID="${LAB_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
readonly CAPTURED_AT="${LAB_CAPTURED_AT:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
readonly RECEIVER_LOCAL_PORT="${LAB_RECEIVER_LOCAL_PORT:-19099}"
readonly CAPTURE_TIMEOUT_SECONDS="${LAB_CAPTURE_TIMEOUT_SECONDS:-300}"

LAB_TMP=""
PORT_FORWARD_PID=""
LATEST_INVENTORY=""
RAW_INVENTORY=""
CANDIDATE_JSON=""
FINDINGS_REPORT=""
CAPTURE_STATUS="NOT RUN"
CAPTURE_FAILURE_REASON=""
CONFORMANCE_LITERAL_STATUS=""

log() {
  printf '[skt-0006.03] %s\n' "$*" >&2
}

die() {
  log "ERROR: $*"
  exit 1
}

cleanup() {
  local status=$?
  trap - EXIT

  if [[ "$PORT_FORWARD_PID" =~ ^[0-9]+$ ]]; then
    kill "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
    wait "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
  fi

  # Delete only the exact reserved auxiliary container name. The normal receiver is
  # deployed inside the lab cluster; this removes a stale helper from an interrupted
  # development iteration without touching any other Docker object.
  if command -v docker >/dev/null 2>&1; then
    docker rm -f "$AUX_CONTAINER_NAME" >/dev/null 2>&1 || true
  fi

  # Delete only the exact fixed lab cluster. Never broaden this to --all.
  if command -v k3d >/dev/null 2>&1; then
    k3d cluster delete "$LAB_CLUSTER_NAME" >/dev/null 2>&1 || true
  fi

  if [[ -n "$LAB_TMP" && -d "$LAB_TMP" ]]; then
    rm -rf -- "$LAB_TMP"
  fi

  if ((status != 0)); then
    log "teardown completed after failure (status $status)"
  else
    log "teardown completed"
  fi
  exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

require_commands() {
  local command_name
  local missing=()
  local required=(docker k3d helm kubectl curl jq openssl rg)
  for command_name in "${required[@]}"; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
      missing+=("$command_name")
    fi
  done
  if ((${#missing[@]} > 0)); then
    die "missing required command(s): ${missing[*]}"
  fi
  if ! docker info >/dev/null 2>&1; then
    die "Docker daemon is unavailable; start Docker and retry"
  fi
  if ! [[ "$RECEIVER_LOCAL_PORT" =~ ^[0-9]+$ ]] || ((RECEIVER_LOCAL_PORT < 1024 || RECEIVER_LOCAL_PORT > 65535)); then
    die "LAB_RECEIVER_LOCAL_PORT must be an integer in 1024..65535"
  fi
  if ! [[ "$CAPTURE_TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] || ((CAPTURE_TIMEOUT_SECONDS < 30)); then
    die "LAB_CAPTURE_TIMEOUT_SECONDS must be an integer >= 30"
  fi
  if ! [[ "$RUN_ID" =~ ^[A-Za-z0-9_.-]+$ ]]; then
    die "LAB_RUN_ID contains unsupported characters: $RUN_ID"
  fi
}

check_inputs() {
  local path
  for path in "$VALUES_FILE" "$WORKLOAD_MANIFEST" "$RECEIVER_MANIFEST" "$CONFORMANCE_SOURCE" "$REPO_ROOT/e2e/receiver/Dockerfile"; do
    [[ -f "$path" ]] || die "required lab input is missing: $path"
  done

  # The source is intentionally blueprint-configured. This assertion is the static
  # conformance check requested by SKT-0006.03: prove that the signal still emits
  # km.ChartVersion, then report whether a hardcoded literal exists (currently absent).
  rg -q 'km\.ChartVersion' "$CONFORMANCE_SOURCE" \
    || die "conformance.go no longer emits the blueprint-provided km.ChartVersion"
  if rg -q '4\.4\.0' "$CONFORMANCE_SOURCE"; then
    CONFORMANCE_LITERAL_STATUS="A 4.4.0 literal is present in conformance.go; review whether it is intentional."
  else
    CONFORMANCE_LITERAL_STATUS="No hardcoded chart-version literal is present in conformance.go; it emits km.ChartVersion as designed."
  fi
}

generate_tls() {
  local tls_config="$LAB_TMP/receiver-openssl.cnf"
  TLS_CERT="$LAB_TMP/receiver.crt"
  TLS_KEY="$LAB_TMP/receiver.key"

  # Keep the certificate in LAB_TMP only. The SANs cover the in-cluster service DNS
  # name and the local port-forward address used to fetch /__inventory.
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
    || die "could not generate temporary receiver TLS material"
  chmod 600 "$TLS_KEY"
  chmod 644 "$TLS_CERT"
}

delete_stale_exact_resources() {
  log "removing any stale exact lab resources before creation"
  docker rm -f "$AUX_CONTAINER_NAME" >/dev/null 2>&1 || true
  k3d cluster delete "$LAB_CLUSTER_NAME" >/dev/null 2>&1 || true
}

build_receiver() {
  log "building existing e2e receiver image: $RECEIVER_IMAGE"
  docker build --pull=false --file "$REPO_ROOT/e2e/receiver/Dockerfile" \
    --tag "$RECEIVER_IMAGE" "$REPO_ROOT"
}

create_cluster() {
  log "creating fixed lab cluster: $LAB_CLUSTER_NAME"
  k3d cluster create "$LAB_CLUSTER_NAME" \
    --servers 1 \
    --agents 1 \
    --wait \
    --k3s-arg "--disable=traefik@server:*"

  export KUBECONFIG="$LAB_TMP/kubeconfig"
  k3d kubeconfig get "$LAB_CLUSTER_NAME" >"$KUBECONFIG"
  chmod 600 "$KUBECONFIG"
  kubectl cluster-info >/dev/null
}

import_images() {
  log "pulling immutable workload image and importing local receiver image"
  # Pulling proves the digest is resolvable. k3d cannot import an image addressed by
  # digest from Docker's local store, so k3s pulls this immutable reference directly.
  docker pull "$WORKLOAD_IMAGE"
  k3d image import "$RECEIVER_IMAGE" --cluster "$LAB_CLUSTER_NAME"
}

deploy_receiver() {
  log "deploying capture receiver with temporary TLS"
  kubectl apply --filename "$RECEIVER_MANIFEST" >/dev/null
  kubectl --namespace "$RECEIVER_NAMESPACE" create secret tls synthkit-skt000603-receiver-tls \
    --cert="$TLS_CERT" --key="$TLS_KEY" --dry-run=client --output=yaml \
    | kubectl apply --filename=- >/dev/null
  kubectl --namespace "$RECEIVER_NAMESPACE" rollout status \
    deployment/synthkit-skt000603-receiver --timeout=180s
}

deploy_workloads() {
  log "deploying pinned two-service workload deck"
  kubectl apply --filename "$WORKLOAD_MANIFEST" >/dev/null
  kubectl --namespace otel-demo rollout status deployment/lab-catalog --timeout=180s
  kubectl --namespace otel-demo rollout status deployment/lab-checkout --timeout=180s
}

start_port_forward() {
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
      sed -n '1,120p' "$LAB_TMP/port-forward.log" >&2 || true
      die "receiver port-forward exited before becoming ready"
    fi
    sleep 1
  done
  sed -n '1,120p' "$LAB_TMP/port-forward.log" >&2 || true
  die "receiver port-forward did not become ready"
}

install_chart() {
  log "installing $CHART_REF version $CHART_VERSION"
  helm repo add "$CHART_REPO_NAME" "$CHART_REPO_URL" >/dev/null
  helm repo update >/dev/null
  helm upgrade --install "$HELM_RELEASE" "$CHART_REF" \
    --version "$CHART_VERSION" \
    --namespace "$RECEIVER_NAMESPACE" \
    --values "$VALUES_FILE" \
    --wait \
    --timeout 15m
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

inventory_meets_acceptance() {
  jq -e '
    def receipt($protocol): any(.receipts[]?; .protocol == $protocol and (.count // 0) > 0);
    def metric_label($key): any(.metrics[]?.labels[]?; .key == $key and ((.values // []) | length) > 0);
    def label_value($key; $value): any(.metrics[]?.labels[]?; .key == $key and (((.values // []) | index($value)) != null));
    (.schema_version == "synthkit.telemetry.inventory/v1alpha1")
      and receipt("prometheus_remote_write_v1")
      and receipt("otlp_logs")
      and metric_label("cluster")
      and metric_label("k8s_cluster_name")
      and metric_label("job")
      and metric_label("instance")
      and label_value("source"; "kubernetes")
  ' "$LATEST_INVENTORY" >/dev/null 2>&1
}

wait_for_capture() {
  local deadline=$((SECONDS + CAPTURE_TIMEOUT_SECONDS))
  local remaining
  log "waiting for real collector egress (RW1, OTLP logs, ambient labels)"
  while ((SECONDS < deadline)); do
    if ! kill -0 "$PORT_FORWARD_PID" >/dev/null 2>&1; then
      CAPTURE_STATUS="FAIL"
      CAPTURE_FAILURE_REASON="The receiver port-forward exited during capture; see $LAB_TMP/port-forward.log."
      sed -n '1,120p' "$LAB_TMP/port-forward.log" >&2 || true
      return 1
    fi
    if fetch_inventory && inventory_meets_acceptance; then
      cp -- "$LATEST_INVENTORY" "$RAW_INVENTORY"
      CAPTURE_STATUS="PASS"
      return 0
    fi
    remaining=$((deadline - SECONDS))
    log "capture not complete; retrying (${remaining}s remaining)"
    sleep 5
  done
  if [[ -s "$LATEST_INVENTORY" ]]; then
    cp -- "$LATEST_INVENTORY" "$RAW_INVENTORY"
  fi
  CAPTURE_STATUS="FAIL"
  CAPTURE_FAILURE_REASON="Timed out after ${CAPTURE_TIMEOUT_SECONDS}s waiting for RW1, OTLP-log, and ambient-label evidence."
  return 1
}

normalize_candidate() {
  [[ -s "$RAW_INVENTORY" ]] || return 1
  jq -S \
    --arg substrate "$CAPTURE_SUBSTRATE" \
    --arg chart_version "$CHART_VERSION" \
    --arg captured_at "$CAPTURED_AT" '
      def strings: ((. // []) | unique | sort);
      def attributes: map(.values |= strings) | sort_by(.key);
      .schema_version = "synthkit.telemetry.inventory/v1alpha1"
      | .provenance = {
          substrate: $substrate,
          chart_version: $chart_version,
          captured_at: $captured_at
        }
      | .metrics = ((.metrics // []) | map(
          .transports |= strings
          | .instrument_types |= strings
          | .labels = ((.labels // []) | attributes)
          | if .histogram then
              .histogram.bucket_bounds = ((.histogram.bucket_bounds // []) | unique | sort)
              | .histogram.native_schemas = ((.histogram.native_schemas // []) | unique | sort)
            else . end
        ) | sort_by(.name))
      | .logs = ((.logs // []) | map(
          .stream_labels = ((.stream_labels // []) | attributes)
          | .structured_metadata_keys |= strings
        ) | sort_by(.transport, .source))
      | .traces = ((.traces // []) | map(
          .resource_attributes = ((.resource_attributes // []) | attributes)
          | .span_names |= strings
          | .span_attribute_keys |= strings
        ) | sort_by(.service))
      | .profiles = ((.profiles // []) | map(.labels = ((.labels // []) | attributes)) | sort_by(.profile_type))
      | .sigil = ((.sigil // []) | map(.operation_names |= strings) | sort_by(.ingest_kind))
      | .receipts = ((.receipts // []) | sort_by(.protocol))
    ' "$RAW_INVENTORY" >"$CANDIDATE_JSON"
}

inventory_value_summary() {
  if [[ ! -s "$RAW_INVENTORY" ]]; then
    printf '%s' '(inventory unavailable)'
    return 0
  fi
  jq -r '
    . as $root
    | reduce ["cluster", "k8s_cluster_name", "job", "instance", "source"][] as $key
      ({}; .[$key] = ([$root.metrics[]?.labels[]? | select(.key == $key) | .values[]?] | unique | sort))
    | to_entries
    | map("- \(.key): \(.value | if length == 0 then "(none)" else join(", ") end)")
    | join("\n")
  ' "$RAW_INVENTORY" 2>/dev/null || printf '%s' '(inventory unavailable)'
}

write_findings_report() {
  local metrics logs traces receipts ambient_values
  metrics='(unavailable)'
  logs='(unavailable)'
  traces='(unavailable)'
  receipts='(unavailable)'
  if [[ -s "$RAW_INVENTORY" ]]; then
    metrics="$(jq -r '.metrics | length' "$RAW_INVENTORY")"
    logs="$(jq -r '.logs | length' "$RAW_INVENTORY")"
    traces="$(jq -r '.traces | length' "$RAW_INVENTORY")"
    receipts="$(jq -r '[.receipts[]? | "\(.protocol)=\(.count)"] | if length == 0 then "(none)" else join(", ") end' "$RAW_INVENTORY")"
  fi
  ambient_values="$(inventory_value_summary)"

  local rw1_status="FAIL"
  local otlp_logs_status="FAIL"
  local ambient_status="FAIL"
  if [[ -s "$RAW_INVENTORY" ]] && jq -e 'any(.receipts[]?; .protocol == "prometheus_remote_write_v1" and (.count // 0) > 0)' "$RAW_INVENTORY" >/dev/null 2>&1; then
    rw1_status="PASS"
  fi
  if [[ -s "$RAW_INVENTORY" ]] && jq -e 'any(.receipts[]?; .protocol == "otlp_logs" and (.count // 0) > 0)' "$RAW_INVENTORY" >/dev/null 2>&1; then
    otlp_logs_status="PASS"
  fi
  if [[ -s "$RAW_INVENTORY" ]] && jq -e '
    def metric_label($key): any(.metrics[]?.labels[]?; .key == $key and ((.values // []) | length) > 0);
    def source: any(.metrics[]?.labels[]?; .key == "source" and (((.values // []) | index("kubernetes")) != null));
    metric_label("cluster") and metric_label("k8s_cluster_name") and metric_label("job") and metric_label("instance") and source
  ' "$RAW_INVENTORY" >/dev/null 2>&1; then
    ambient_status="PASS"
  fi

  cat >"$FINDINGS_REPORT" <<EOF
# SKT-0006.03 k3s capture-lab findings

- run_id: $RUN_ID
- captured_at: $CAPTURED_AT
- substrate: $CAPTURE_SUBSTRATE
- chart: $CHART_REF@$CHART_VERSION
- cluster: $LAB_CLUSTER_NAME
- receiver: https://$RECEIVER_SERVICE.$RECEIVER_NAMESPACE.svc.cluster.local:9099

## Capture checks

- collector egress candidate: $CAPTURE_STATUS
- Prometheus Remote-Write v1 receipt: $rw1_status
- OTLP logs receipt: $otlp_logs_status
- ambient metric labels (cluster, k8s_cluster_name, job, instance, source=kubernetes): $ambient_status
- inventory counts: metrics=$metrics, logs=$logs, traces=$traces
- receipts: $receipts

Ambient label values observed:
$ambient_values

## Conformance audit

- chart pin in this lab: $CHART_VERSION
- source assertion: internal/construct/k8scluster/conformance.go emits km.ChartVersion
- literal-version audit: $CONFORMANCE_LITERAL_STATUS

## Scope and limitations

- The existing e2e receiver is deployed as the sole collector egress destination.
- Profiling and profilesReceiver are disabled because this receiver has no profile HTTP route;
  this lab makes no profile-shape claim.
- autoInstrumentation is disabled because the pinned two-service BusyBox deck has no viable
  eBPF/Beyla workload shape; applicationObservability remains enabled for OTLP receiver parity.
- k3s substrate evidence must not be applied to EKS-only identity claims.
EOF
}

write_failure_context() {
  kubectl --namespace "$RECEIVER_NAMESPACE" get pods --output=wide \
    >"$LAB_OUTPUT_DIR/pods-$RUN_ID.txt" 2>&1 || true
  kubectl --namespace "$RECEIVER_NAMESPACE" get events --sort-by=.lastTimestamp \
    >"$LAB_OUTPUT_DIR/events-$RUN_ID.txt" 2>&1 || true
  helm status "$HELM_RELEASE" --namespace "$RECEIVER_NAMESPACE" \
    >"$LAB_OUTPUT_DIR/helm-$RUN_ID.txt" 2>&1 || true
}

main() {
  require_commands
  mkdir -p -- "$LAB_OUTPUT_DIR"
  check_inputs

  RAW_INVENTORY="$LAB_OUTPUT_DIR/inventory-$RUN_ID.json"
  CANDIDATE_JSON="$LAB_OUTPUT_DIR/candidate-$RUN_ID.json"
  FINDINGS_REPORT="$LAB_OUTPUT_DIR/findings-$RUN_ID.md"

  LAB_TMP="$(mktemp -d "${TMPDIR:-/tmp}/synthkit-skt000603.XXXXXX")"
  chmod 700 "$LAB_TMP"

  delete_stale_exact_resources
  generate_tls
  build_receiver
  create_cluster
  import_images
  deploy_receiver
  deploy_workloads
  start_port_forward
  install_chart

  LATEST_INVENTORY="$LAB_TMP/latest-inventory.json"
  if ! wait_for_capture; then
    normalize_candidate || true
    write_findings_report
    write_failure_context
    die "$CAPTURE_FAILURE_REASON See $FINDINGS_REPORT and the exact-name diagnostics in $LAB_OUTPUT_DIR."
  fi

  normalize_candidate || die "could not normalize the receiver inventory into a candidate"
  write_findings_report
  log "candidate: $CANDIDATE_JSON"
  log "findings: $FINDINGS_REPORT"
  log "raw inventory: $RAW_INVENTORY"
}

main "$@"
