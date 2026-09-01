#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

# k3d capture-lab permutation matrix (SKT-0013.04).
#
# This is the MATRIX ENTRYPOINT. It selects permutations, runs each as an independent job under
# a stated concurrency bound, and ends the run in ONE combined report covering all of them.
# Per-permutation orchestration lives in permutation.sh; every decision with logic in it lives
# in the unit-tested Go tool under cmd/lab-matrix.
#
# Three properties this file exists to protect:
#
#   1. A FAILED permutation is distinguishable from one that CAPTURED NOTHING. Both leave an
#      empty corpus entry and they mean opposite things, so every worker always writes a result
#      record that classifies which it was, and the report renders them differently.
#   2. PARTIAL SUCCESS NEVER READS AS SUCCESS. The run's verdict is CAPTURED only when every
#      selected permutation captured; anything else exits non-zero and says so in the headline.
#   3. PARALLEL JOBS CANNOT COLLIDE ON THE CORPUS. Each job owns a fixed cluster name, a
#      distinct local port and its own output directory, and no job writes to reality-corpus at
#      all. Promotion is a separate, deliberate step taken after reading the report.
#
# Docker is an exclusive resource, so the parallelism is bounded and the bound is stated rather
# than discovered by exhausting the host. See MAX_PARALLEL below.

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
readonly PERMUTATIONS_DIR="$SCRIPT_DIR/permutations"
readonly WORKER="$SCRIPT_DIR/permutation.sh"

readonly LAB_OUTPUT_DIR="${LAB_OUTPUT_DIR:-$REPO_ROOT/artifacts/signal-fidelity-k3d}"
readonly RUN_ID="${LAB_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
readonly CAPTURED_AT="${LAB_CAPTURED_AT:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
readonly RESULTS_DIR="$LAB_OUTPUT_DIR/results-$RUN_ID"
readonly REPORT_MD="$LAB_OUTPUT_DIR/matrix-report-$RUN_ID.md"
readonly REPORT_JSON="$LAB_OUTPUT_DIR/matrix-report-$RUN_ID.json"
readonly RECEIVER_IMAGE="synthkit-skt000603/receiver:4.5.0-lab"
readonly WORKLOAD_IMAGE="docker.io/library/busybox@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"
readonly PORT_BASE="${LAB_RECEIVER_PORT_BASE:-19099}"

# THE CONCURRENCY BOUND.
#
# One permutation is one whole k3d cluster: a server container, an agent container and a
# loadbalancer container, running the capture receiver, the pinned workload deck and that
# permutation's full collector set. MEASURED on this lab's Alloy permutations, running two
# concurrently: 767 MiB server + 499 MiB agent + 12 MiB loadbalancer, so about 1.3 GiB resident
# per permutation at its peak, with each cluster taking roughly one core steady and spiking
# during chart install.
#
# The default is 2. Memory is not the binding constraint at that number and is not the reason:
# the capture window is a FIXED wall-clock deadline, so a permutation starved of CPU reports
# `partial` for a harness reason rather than a deployment one, which is precisely the confusion
# this task exists to prevent. Two clusters install their charts without contending on a
# 4-core host, which is the floor worth supporting.
#
# The hard cap is 4. Past that the k3d clusters contend for the Docker daemon's own API and
# image store during `k3d image import`, and cluster creation starts failing for reasons that
# belong to the host rather than to any permutation. Raising it is a deliberate edit here, not
# an environment variable, so nobody discovers the ceiling by exhausting the host.
readonly MAX_PARALLEL_CAP=4
MAX_PARALLEL="${LAB_MAX_PARALLEL:-2}"
readonly PARALLELISM_NOTE="one 2-node k3d cluster plus a full collector set per permutation, measured at about 1.3 GiB resident and roughly one core each; the default of 2 keeps chart installs from contending for CPU, because the capture window is a fixed wall-clock deadline and a starved job would report partial for a harness reason. The bound is a declared constant with a hard cap of 4, never discovered by exhausting the Docker host."

log() {
  printf '[matrix] %s\n' "$*" >&2
}

die() {
  printf '[matrix] ERROR: %s\n' "$*" >&2
  exit 2
}

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
    die "missing required command(s): ${missing[*]}"
  fi
  docker info >/dev/null 2>&1 || die "Docker daemon is unavailable; start Docker and retry"
  if ! [[ "$MAX_PARALLEL" =~ ^[0-9]+$ ]] || ((MAX_PARALLEL < 1)); then
    die "LAB_MAX_PARALLEL must be an integer >= 1"
  fi
  if ((MAX_PARALLEL > MAX_PARALLEL_CAP)); then
    die "LAB_MAX_PARALLEL=$MAX_PARALLEL exceeds the stated hard cap of $MAX_PARALLEL_CAP; raise the cap in run.sh deliberately if the host really supports it"
  fi
  if ! [[ "$RUN_ID" =~ ^[A-Za-z0-9_.-]+$ ]]; then
    die "LAB_RUN_ID contains unsupported characters: $RUN_ID"
  fi
}

all_permutations() {
  local dir
  for dir in "$PERMUTATIONS_DIR"/*/; do
    [[ -f "$dir/meta.env" ]] || continue
    basename "$dir"
  done | sort
}

permutation_capture_status() {
  # Reads one declared field without sourcing the file into this shell.
  sed -n 's/^PERMUTATION_CAPTURE_STATUS="\{0,1\}\([a-z]*\)"\{0,1\}$/\1/p' \
    "$PERMUTATIONS_DIR/$1/meta.env" | head -1
}

permutation_field() {
  sed -n "s/^$2=\"\(.*\)\"$/\1/p" "$PERMUTATIONS_DIR/$1/meta.env" | head -1
}

# A permutation the run did not select is still part of the matrix. It is recorded as skipped so
# the combined report always shows the whole estate, and an unproven permutation is never
# quietly missing from the table.
write_skipped_result() {
  local permutation=$1
  mkdir -p -- "$RESULTS_DIR"
  jq -n \
    --arg result_version "synthkit.lab.permutation-result/v1alpha1" \
    --arg permutation "$permutation" \
    --arg title "$(permutation_field "$permutation" PERMUTATION_TITLE)" \
    --arg summary "$(permutation_field "$permutation" PERMUTATION_SUMMARY)" \
    --arg collector "$(permutation_field "$permutation" PERMUTATION_COLLECTOR)" \
    --arg collector_version "$(permutation_field "$permutation" PERMUTATION_COLLECTOR_VERSION)" \
    --arg capture_status "$(permutation_capture_status "$permutation")" \
    --arg run_id "$RUN_ID" \
    '{
      result_version: $result_version,
      permutation: $permutation,
      title: $title,
      summary: $summary,
      collector: $collector,
      collector_version: $collector_version,
      substrate: "k3s",
      cluster: "",
      capture_status: $capture_status,
      run_id: $run_id,
      outcome: "skipped",
      phase: "not selected",
      exit_code: 0,
      capture_window_seconds: 0,
      counts: {metrics: 0, logs: 0, traces: 0}
    }' >"$RESULTS_DIR/$permutation.json"
}

# A selected permutation that dies before its worker can write anything would otherwise vanish
# from the matrix entirely, and a missing row is the most dangerous shape of all: it reads as
# though the permutation was never part of the run. The dispatcher therefore stakes a claim
# before launching, pre-classified as failed. The worker overwrites it on every exit path, so
# this record survives only if the worker never ran its own teardown.
write_launched_placeholder() {
  local permutation=$1
  mkdir -p -- "$RESULTS_DIR"
  jq -n \
    --arg result_version "synthkit.lab.permutation-result/v1alpha1" \
    --arg permutation "$permutation" \
    --arg title "$(permutation_field "$permutation" PERMUTATION_TITLE)" \
    --arg summary "$(permutation_field "$permutation" PERMUTATION_SUMMARY)" \
    --arg collector "$(permutation_field "$permutation" PERMUTATION_COLLECTOR)" \
    --arg collector_version "$(permutation_field "$permutation" PERMUTATION_COLLECTOR_VERSION)" \
    --arg capture_status "$(permutation_capture_status "$permutation")" \
    --arg run_id "$RUN_ID" \
    '{
      result_version: $result_version,
      permutation: $permutation,
      title: $title,
      summary: $summary,
      collector: $collector,
      collector_version: $collector_version,
      substrate: "k3s",
      cluster: "",
      capture_status: $capture_status,
      run_id: $run_id,
      outcome: "failed",
      phase: "launch",
      failure_reason: "The worker never wrote a result record, so it died before its own teardown ran. Read this permutation'"'"'s worker log.",
      exit_code: 0,
      capture_window_seconds: 0,
      counts: {metrics: 0, logs: 0, traces: 0}
    }' >"$RESULTS_DIR/$permutation.json"
}

build_shared_images() {
  # Built ONCE, here, rather than in every worker: parallel workers writing the same image tag
  # would race the Docker image store, and a failure there is a harness failure that would
  # otherwise be charged to whichever permutation lost.
  log "building the shared capture receiver image: $RECEIVER_IMAGE"
  docker build --pull=false --file "$REPO_ROOT/e2e/receiver/Dockerfile" \
    --tag "$RECEIVER_IMAGE" "$REPO_ROOT" >"$LAB_OUTPUT_DIR/receiver-build-$RUN_ID.log" 2>&1 \
    || die "receiver image build failed; see $LAB_OUTPUT_DIR/receiver-build-$RUN_ID.log"
  log "resolving the pinned workload image digest"
  docker pull "$WORKLOAD_IMAGE" >/dev/null 2>&1 \
    || die "could not resolve the pinned workload image digest"
}

running_count() {
  local pid
  local count=0
  for pid in "${JOB_PIDS[@]:-}"; do
    [[ -n "$pid" ]] || continue
    if kill -0 "$pid" >/dev/null 2>&1; then
      count=$((count + 1))
    fi
  done
  printf '%d' "$count"
}

main() {
  require_commands
  mkdir -p -- "$LAB_OUTPUT_DIR" "$RESULTS_DIR"
  [[ -x "$WORKER" || -f "$WORKER" ]] || die "worker script is missing: $WORKER"

  local available selected candidate status
  available=()
  while IFS= read -r candidate; do
    available+=("$candidate")
  done < <(all_permutations)
  ((${#available[@]} > 0)) || die "no permutation is defined under $PERMUTATIONS_DIR"

  selected=()
  if (($# > 0)); then
    for candidate in "$@"; do
      [[ -f "$PERMUTATIONS_DIR/$candidate/meta.env" ]] \
        || die "unknown permutation: $candidate (available: ${available[*]})"
      selected+=("$candidate")
    done
  elif [[ -n "${LAB_PERMUTATIONS:-}" ]]; then
    for candidate in $(printf '%s' "$LAB_PERMUTATIONS" | tr ',' ' '); do
      [[ -f "$PERMUTATIONS_DIR/$candidate/meta.env" ]] \
        || die "unknown permutation: $candidate (available: ${available[*]})"
      selected+=("$candidate")
    done
  else
    # Default selection is the permutations whose capture has been proven end to end. An
    # unproven permutation is a real part of the matrix but running it by default would make
    # every default run report PARTIAL for a reason that is not a regression.
    for candidate in "${available[@]}"; do
      status="$(permutation_capture_status "$candidate")"
      if [[ "$status" == "proven" ]]; then
        selected+=("$candidate")
      fi
    done
    ((${#selected[@]} > 0)) || die "no proven permutation is defined; select one explicitly"
  fi

  log "run_id=$RUN_ID"
  log "available permutations: $(printf '%s ' "${available[@]}")"
  log "selected permutations: $(printf '%s ' "${selected[@]}")"
  log "concurrency bound: $MAX_PARALLEL (hard cap $MAX_PARALLEL_CAP)"
  log "output: $LAB_OUTPUT_DIR"

  for candidate in "${available[@]}"; do
    if ! printf '%s\n' "${selected[@]}" | grep -Fxq "$candidate"; then
      log "not selected this run: $candidate (run it with: bash e2e/lab/run.sh $candidate)"
      write_skipped_result "$candidate"
    fi
  done

  build_shared_images

  JOB_PIDS=()
  local index=0
  local port
  for candidate in "${selected[@]}"; do
    while (($(running_count) >= MAX_PARALLEL)); do
      sleep 2
    done
    port=$((PORT_BASE + index))
    index=$((index + 1))
    log "starting $candidate (local receiver port $port)"
    write_launched_placeholder "$candidate"
    (
      env \
        LAB_OUTPUT_DIR="$LAB_OUTPUT_DIR" \
        LAB_RESULTS_DIR="$RESULTS_DIR" \
        LAB_RUN_ID="$RUN_ID" \
        LAB_CAPTURED_AT="$CAPTURED_AT" \
        LAB_RECEIVER_LOCAL_PORT="$port" \
        LAB_RECEIVER_IMAGE="$RECEIVER_IMAGE" \
        LAB_SKIP_IMAGE_BUILD=true \
        bash "$WORKER" "$candidate" >"$LAB_OUTPUT_DIR/worker-$candidate-$RUN_ID.log" 2>&1
    ) &
    JOB_PIDS+=($!)
  done

  # A worker's non-zero exit is expected and already recorded in its own result record, so the
  # matrix waits for every job and never lets one job's failure abort the others.
  local pid
  for pid in "${JOB_PIDS[@]:-}"; do
    wait "$pid" || true
  done

  log "all jobs finished; building the combined report"
  local report_status=0
  go run "$REPO_ROOT/e2e/lab/cmd/lab-matrix" report \
    -results "$RESULTS_DIR" \
    -out "$REPORT_MD" \
    -json "$REPORT_JSON" \
    -run-id "$RUN_ID" \
    -generated-at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -max-parallel "$MAX_PARALLEL" \
    -parallelism-note "$PARALLELISM_NOTE" || report_status=$?

  log "combined report: $REPORT_MD"
  log "machine-readable report: $REPORT_JSON"
  log "per-permutation worker logs: $LAB_OUTPUT_DIR/worker-*-$RUN_ID.log"
  exit "$report_status"
}

main "$@"
