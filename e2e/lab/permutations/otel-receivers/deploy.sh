#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Deploys the two OTel Collector releases this permutation is made of. The chart version is
# pinned twice: in meta.env and here.
set -Eeuo pipefail
IFS=$'\n\t'

: "${LAB_KUBECTL_CONTEXT:?LAB_KUBECTL_CONTEXT must name the disposable k3d context}"

readonly CHART_REPO_NAME="open-telemetry"
readonly CHART_REPO_URL="https://open-telemetry.github.io/opentelemetry-helm-charts"
readonly CHART_REF="open-telemetry/opentelemetry-collector"
readonly CHART_VERSION="0.171.0"

helm repo add "$CHART_REPO_NAME" "$CHART_REPO_URL" >/dev/null
helm repo update "$CHART_REPO_NAME" >/dev/null

helm --kube-context "$LAB_KUBECTL_CONTEXT" upgrade --install synthkit-otel-daemonset "$CHART_REF" \
  --version "$CHART_VERSION" \
  --namespace "$LAB_RECEIVER_NAMESPACE" \
  --values "$LAB_PERMUTATION_DIR/values-daemonset.yaml" \
  --wait \
  --timeout 10m

helm --kube-context "$LAB_KUBECTL_CONTEXT" upgrade --install synthkit-otel-deployment "$CHART_REF" \
  --version "$CHART_VERSION" \
  --namespace "$LAB_RECEIVER_NAMESPACE" \
  --values "$LAB_PERMUTATION_DIR/values-deployment.yaml" \
  --wait \
  --timeout 10m
