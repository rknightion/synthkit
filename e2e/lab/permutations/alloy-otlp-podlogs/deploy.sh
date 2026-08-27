#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Deploys the grafana/k8s-monitoring chart with the shared base values plus this
# permutation's overlay. The chart version is pinned twice: in meta.env and here.
set -Eeuo pipefail
IFS=$'\n\t'

readonly CHART_REPO_NAME="grafana"
readonly CHART_REPO_URL="https://grafana.github.io/helm-charts"
readonly CHART_REF="grafana/k8s-monitoring"
readonly CHART_VERSION="4.4.0"
readonly HELM_RELEASE="synthkit-k8s-monitoring"

helm repo add "$CHART_REPO_NAME" "$CHART_REPO_URL" >/dev/null
helm repo update "$CHART_REPO_NAME" >/dev/null
helm upgrade --install "$HELM_RELEASE" "$CHART_REF" \
  --version "$CHART_VERSION" \
  --namespace "$LAB_RECEIVER_NAMESPACE" \
  --values "$LAB_SHARED_DIR/k8s-monitoring-values.yaml" \
  --values "$LAB_PERMUTATION_DIR/values.yaml" \
  --wait \
  --timeout 15m
