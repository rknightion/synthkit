#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Deploys the documented Prometheus Operator remote-write path. The ServiceMonitor is applied
# after Helm installs the CRDs, then Prometheus discovers it during the common capture window.
set -Eeuo pipefail
IFS=$'\n\t'

readonly CHART_REPO_NAME="prometheus-community"
readonly CHART_REPO_URL="https://prometheus-community.github.io/helm-charts"
readonly CHART_REF="prometheus-community/kube-prometheus-stack"
readonly CHART_VERSION="88.6.2"
readonly HELM_RELEASE="synthkit-prometheus-operator"

helm repo add "$CHART_REPO_NAME" "$CHART_REPO_URL" >/dev/null
helm repo update "$CHART_REPO_NAME" >/dev/null
helm upgrade --install "$HELM_RELEASE" "$CHART_REF" \
  --version "$CHART_VERSION" \
  --namespace "$LAB_RECEIVER_NAMESPACE" \
  --values "$LAB_PERMUTATION_DIR/values.yaml" \
  --wait \
  --timeout 15m

kubectl apply --filename "$LAB_PERMUTATION_DIR/servicemonitor.yaml" >/dev/null
