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
readonly CONTAINERD_NAMESPACE="k8s.io"

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

containerd_image_matches_reference() {
  local image_listing=$1
  local image_reference=$2
  local canonical_reference
  local registry_prefix

  # ctr normalizes Docker's unqualified names on import. Accept the exact input reference or
  # the one canonical containerd name it maps to, but never a digest-only or unrelated image.
  canonical_reference="$image_reference"
  if [[ "$image_reference" == */* ]]; then
    registry_prefix="${image_reference%%/*}"
    if [[ "$registry_prefix" != *.* && "$registry_prefix" != *:* && "$registry_prefix" != localhost ]]; then
      canonical_reference="docker.io/$image_reference"
    fi
  else
    canonical_reference="docker.io/library/$image_reference"
  fi
  if [[ "$canonical_reference" != *@* && "${canonical_reference##*/}" != *:* ]]; then
    canonical_reference+=":latest"
  fi

  printf '%s\n' "$image_listing" | grep -Fqx -- "$image_reference" \
    || printf '%s\n' "$image_listing" | grep -Fqx -- "$canonical_reference"
}

format_missing_nodes() {
  local joined=""
  local missing_node
  for missing_node in "${IMAGE_RESIDENCY_MISSING[@]}"; do
    if [[ -n "$joined" ]]; then
      joined+=","
    fi
    joined+="$missing_node"
  done
  printf '%s' "$joined"
}

check_image_residency() {
  local attempt=$1
  local node_list_json node_names node image_listing ctr_status
  local node_count=0
  local diagnostic="$run_dir/image-residency.txt"
  IMAGE_RESIDENCY_MISSING=()

  {
    printf '%s\n' "attempt=$attempt"
    printf '%s\n' "reference=$IMAGE"
    printf '%s\n' "namespace=$CONTAINERD_NAMESPACE"
    printf '%s\n' 'node_command=k3d node list --output json (filtered to this cluster)'
    printf '%s\n' 'residency_command=docker exec <node> ctr --namespace k8s.io images list --quiet'
  } >>"$diagnostic"

  if ! node_list_json="$(k3d node list --output json 2>&1)"; then
    printf '%s\n' "node-list=ERROR $node_list_json" >>"$diagnostic"
    IMAGE_RESIDENCY_MISSING=("node-list")
    return 1
  fi
  printf '%s\n' "node-list-json=$node_list_json" >>"$diagnostic"
  if ! node_names="$(jq -r --arg cluster "$CLUSTER_NAME" '.[] | select((.cluster // "") == $cluster or ((.name // "") | startswith("k3d-" + $cluster + "-"))) | select(.role == "server" or .role == "agent") | .name' <<<"$node_list_json")"; then
    printf '%s\n' 'node-list=ERROR invalid k3d node-list JSON' >>"$diagnostic"
    IMAGE_RESIDENCY_MISSING=("node-list")
    return 1
  fi

  while IFS= read -r node; do
    [[ -n "$node" ]] || continue
    node_count=$((node_count + 1))
    if image_listing="$(docker exec "$node" ctr --namespace "$CONTAINERD_NAMESPACE" images list --quiet 2>&1)"; then
      ctr_status=0
    else
      ctr_status=$?
    fi
    {
      printf '%s\n' "node=$node"
      printf '%s\n' "command=docker exec $node ctr --namespace $CONTAINERD_NAMESPACE images list --quiet"
      printf '%s\n' "exit=$ctr_status"
      printf '%s\n' 'listing<<EOF'
      printf '%s\n' "$image_listing"
      printf '%s\n' 'EOF'
    } >>"$diagnostic"
    if ((ctr_status == 0)) && containerd_image_matches_reference "$image_listing" "$IMAGE"; then
      printf '%s\n' "node=$node residency=PASS reference=$IMAGE" >>"$diagnostic"
    else
      printf '%s\n' "node=$node residency=MISS reference=$IMAGE" >>"$diagnostic"
      IMAGE_RESIDENCY_MISSING+=("$node")
    fi
  done <<<"$node_names"

  if ((node_count == 0)); then
    printf '%s\n' 'node-list=ERROR no server or agent nodes were returned' >>"$diagnostic"
    IMAGE_RESIDENCY_MISSING=("node-list")
    return 1
  fi
  if ((${#IMAGE_RESIDENCY_MISSING[@]} == 0)); then
    printf '%s\n' "attempt=$attempt result=PASS" >>"$diagnostic"
    return 0
  fi
  printf '%s\n' "attempt=$attempt result=MISS missing_nodes=$(format_missing_nodes)" >>"$diagnostic"
  return 1
}

run_image_import() {
  local attempt=$1
  local import_status=0
  local import_log="$run_dir/k3d-import.log"

  printf '%s\n' "--- attempt=$attempt ---" >>"$import_log"
  if k3d image import "$IMAGE" --cluster "$CLUSTER_NAME" >>"$import_log" 2>&1; then
    :
  else
    import_status=$?
  fi
  printf '%s\n' "attempt=$attempt exit=$import_status" >>"$import_log"
  return "$import_status"
}

import_image() {
  if ! run_image_import 1; then
    log "k3d image import returned non-zero; checking direct containerd residency"
  fi
  if check_image_residency 1; then
    printf '%s\n' 'retry=not-needed' >>"$run_dir/image-residency.txt"
    return 0
  fi

  log "image reference $IMAGE was missing from node(s) $(format_missing_nodes); retrying k3d image import once"
  printf '%s\n' "retry=performed reason=missing_nodes=$(format_missing_nodes)" >>"$run_dir/image-residency.txt"
  if ! run_image_import 2; then
    log "retry k3d image import returned non-zero; checking direct containerd residency"
  fi
  if check_image_residency 2; then
    printf '%s\n' 'retry=result=PASS' >>"$run_dir/image-residency.txt"
    return 0
  fi

  local missing_nodes
  missing_nodes="$(format_missing_nodes)"
  die "image reference $IMAGE is absent from node(s) $missing_nodes after exactly one retry; see $run_dir/image-residency.txt"
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
		printf '%s\n' "- image residency diagnostic: $run_dir/image-residency.txt"
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
import_image

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
