#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

# Exercise skcapture's shipped in-cluster Job and skforge's deterministic path against a
# disposable k3d cluster. This is deliberately separate from the k8s-monitoring permutation
# lab: it captures the Kubernetes API through the Job's own ServiceAccount, not collector egress.

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"
readonly REPO_ROOT
readonly CLUSTER_NAME="${SKCAPTURE_K3D_CLUSTER_NAME:-skcapture-k3d}"
readonly OUTPUT_ROOT="${SKCAPTURE_K3D_OUTPUT_DIR:-$REPO_ROOT/artifacts/skcapture-k3d}"
readonly IMAGE="skcapture:dev"
readonly NAMESPACE="skcapture"

created_cluster=false
tmp_dir=""

log() {
  printf '[skcapture-k3d] %s\n' "$*" >&2
}

die() {
  log "ERROR: $*"
  exit 2
}

cleanup() {
  local status=$?
  if [[ "$created_cluster" == true ]]; then
    k3d cluster delete "$CLUSTER_NAME" >/dev/null 2>&1 || log "WARNING: could not delete disposable cluster $CLUSTER_NAME"
  fi
  [[ -z "$tmp_dir" ]] || rm -rf -- "$tmp_dir"
  exit "$status"
}
trap cleanup EXIT

require_commands() {
  local command_name
  local missing=()
  local required=(docker k3d kubectl jq openssl go rg)
  for command_name in "${required[@]}"; do
    command -v "$command_name" >/dev/null 2>&1 || missing+=("$command_name")
  done
  ((${#missing[@]} == 0)) || die "missing required command(s): ${missing[*]}"
  docker info >/dev/null 2>&1 || die "Docker daemon is unavailable"
  [[ "$CLUSTER_NAME" =~ ^[a-z0-9][a-z0-9-]*$ ]] || die "SKCAPTURE_K3D_CLUSTER_NAME must be lowercase letters, digits, and hyphens"
  ((${#CLUSTER_NAME} <= 32)) || die "SKCAPTURE_K3D_CLUSTER_NAME exceeds k3d's 32-character limit"
}

cluster_exists() {
  k3d cluster list --output json | jq -e --arg name "$CLUSTER_NAME" 'any(.[]?; .name == $name)' >/dev/null
}

assert_rbac() {
  local resource
  for resource in nodes namespaces services deployments statefulsets daemonsets ingresses; do
    kubectl auth can-i --as="system:serviceaccount:$NAMESPACE:skcapture" list "$resource" | grep -qx yes ||
      die "shipped RBAC does not grant list $resource to the skcapture ServiceAccount"
  done
  for resource in secrets configmaps pods; do
    if kubectl auth can-i --as="system:serviceaccount:$NAMESPACE:skcapture" list "$resource" | grep -qx yes; then
      die "shipped RBAC unexpectedly grants list $resource to the skcapture ServiceAccount"
    fi
  done
}

wait_and_copy_capture() {
  local pod attempt
  pod=""
  # The Job object is accepted before its Pod exists. Poll only this label for a bounded
  # interval, then use the operator-documented container-completion wait below.
  for attempt in {1..30}; do
    pod="$(kubectl -n "$NAMESPACE" get pods -l job-name=skcapture -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    [[ -z "$pod" ]] || break
    sleep 1
  done
  [[ -n "$pod" ]] || die "skcapture Job created no pod"
  kubectl -n "$NAMESPACE" wait \
    --for=jsonpath='{.status.containerStatuses[?(@.name=="skcapture")].state.terminated.reason}'=Completed \
    "pod/$pod" --timeout=120s
  kubectl -n "$NAMESPACE" cp -c output-hold "$pod:/out/capture.age" "$run_dir/capture.age"
}

record_result() {
	local provider name_source
	provider="$(jq -r '.clusters[0].provider' "$run_dir/capture.json")"
	name_source="$(jq -r '.clusters[0].name_source' "$run_dir/capture.json")"
	{
    printf '%s\n\n' '# skcapture k3d result'
    printf '%s\n' "- cluster: $CLUSTER_NAME (k3s via k3d)"
    printf '%s\n' '- Job: completed under the shipped skcapture ServiceAccount and skcapture-reader RBAC'
    printf '%s\n' '- encrypted output: retrieved using the documented output-hold kubectl cp path'
		printf '%s\n' "- captured provider: $provider"
		printf '%s\n' "- captured cluster-name source: $name_source"
		printf '%s\n\n' '- forge behaviour: refused non-EKS skeleton without --assume-aws (proved in forge-refusal.txt)'
		printf '%s\n\n' '## Non-EKS conclusion'
		printf '%s\n' "The k3d capture reports provider $provider. blueprint.Load requires a cloud block whenever an"
		printf '%s\n' 'environment declares a cluster, so a cloudless skeleton cannot load or run as a pure Kubernetes'
		printf '%s\n' 'estate today. skforge therefore stopped before writing a blueprint and named the captured provider'
		printf '%s\n' 'and --assume-aws in forge-refusal.txt. No AWS-shaped skeleton, validation, inventory, or fidelity'
		printf '%s\n' 'result was created from this non-EKS capture.'
	} >"$run_dir/result.md"
}

require_commands
cluster_exists && die "k3d cluster $CLUSTER_NAME already exists; refusing to delete a cluster this run did not create"

mkdir -p -- "$OUTPUT_ROOT"
run_dir="$(mktemp -d "$OUTPUT_ROOT/run.XXXXXX")"
tmp_dir="$(mktemp -d)"
readonly run_dir tmp_dir
readonly KUBECONFIG="$tmp_dir/kubeconfig"
export KUBECONFIG

log "building $IMAGE from Dockerfile.skcapture"
docker build -f "$REPO_ROOT/Dockerfile.skcapture" -t "$IMAGE" "$REPO_ROOT"

log "creating disposable k3d cluster $CLUSTER_NAME"
k3d cluster create "$CLUSTER_NAME" --servers 1 --agents 1 --wait
created_cluster=true
k3d kubeconfig get "$CLUSTER_NAME" >"$KUBECONFIG"

# A fresh apiserver serves 503 from /openapi/v3 for a few seconds after `--wait` returns, and
# `kubectl apply` validates against it by default; wait for the readiness the applies depend on
# (same race as e2e/lab/permutation.sh wait_for_apiserver).
log "waiting for the apiserver to serve /readyz and /openapi/v3"
deadline=$((SECONDS + 120))
until kubectl get --raw='/readyz' --request-timeout=10s >/dev/null 2>&1 \
  && kubectl get --raw='/openapi/v3' --request-timeout=10s >/dev/null 2>&1; do
  if ((SECONDS >= deadline)); then
    echo "the apiserver did not become ready within 120s" >&2
    exit 1
  fi
  sleep 2
done

log "importing local $IMAGE into $CLUSTER_NAME"
k3d image import "$IMAGE" --cluster "$CLUSTER_NAME"

kubectl apply -f "$REPO_ROOT/deploy/skcapture/rbac.yaml"
assert_rbac
openssl rand -base64 32 >"$tmp_dir/passphrase"
chmod 600 "$tmp_dir/passphrase"
kubectl -n "$NAMESPACE" create secret generic skcapture-pass --from-file=passphrase="$tmp_dir/passphrase"
# The shipped Job has two containers. Rewrite both image references for this local-only proof,
# so no registry pull can mask the image this run built and imported into k3d.
sed -E "s|^([[:space:]]*image:[[:space:]]*).*|\\1$IMAGE|" "$REPO_ROOT/deploy/skcapture/job.yaml" >"$tmp_dir/job.yaml"
if [[ "$(rg -c "^[[:space:]]*image: $IMAGE$" "$tmp_dir/job.yaml")" -ne 2 ]]; then
  die "local Job manifest did not rewrite both shipped image references to $IMAGE"
fi
kubectl apply -f "$tmp_dir/job.yaml"
wait_and_copy_capture
kubectl -n "$NAMESPACE" logs "job/skcapture" -c skcapture >"$run_dir/skcapture.log"

# A k3d cluster is non-EKS. The default forge path must reject it before any AWS-shaped skeleton
# is written. The refusal itself is the repeatable non-EKS proof.
go run "$REPO_ROOT/cmd/skforge" inspect "$run_dir/capture.age" --key "$tmp_dir/passphrase" >"$run_dir/capture.json"
if go run "$REPO_ROOT/cmd/skforge" prompt "$run_dir/capture.age" --key "$tmp_dir/passphrase" \
	>"$run_dir/blueprint-prompt.txt" 2>"$run_dir/forge-refusal.txt"; then
	die "skforge unexpectedly forged an AWS/EKS skeleton for non-EKS provider $(jq -r '.clusters[0].provider' "$run_dir/capture.json")"
fi
provider="$(jq -r '.clusters[0].provider' "$run_dir/capture.json")"
rg -F -- "provider \"$provider\"" "$run_dir/forge-refusal.txt" >/dev/null ||
	die "skforge refusal did not name the captured provider"
rg -F -- '--assume-aws' "$run_dir/forge-refusal.txt" >/dev/null ||
	die "skforge refusal did not name --assume-aws"
record_result
log "result: $run_dir/result.md"
