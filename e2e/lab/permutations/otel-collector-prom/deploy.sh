#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Deploy the documented OTel Collector with Prometheus exporters method. The Collector chart
# pin is duplicated in meta.env. kube-state-metrics and node-exporter use the exact Helm
# installations prescribed by the Grafana documentation; they are metric sources, not a second
# collector implementation.
set -Eeuo pipefail
IFS=$'\n\t'

readonly OTEL_CHART_REPO_NAME="open-telemetry"
readonly OTEL_CHART_REPO_URL="https://open-telemetry.github.io/opentelemetry-helm-charts"
readonly OTEL_CHART_REF="open-telemetry/opentelemetry-collector"
readonly CHART_VERSION="0.171.0"
readonly PROMETHEUS_CHART_REPO_NAME="prometheus-community"
readonly PROMETHEUS_CHART_REPO_URL="https://prometheus-community.github.io/helm-charts"

helm repo add "$OTEL_CHART_REPO_NAME" "$OTEL_CHART_REPO_URL" >/dev/null
helm repo add "$PROMETHEUS_CHART_REPO_NAME" "$PROMETHEUS_CHART_REPO_URL" >/dev/null
helm repo update >/dev/null

# These source deployments are the documented setup for the two exporters that kubelet does not
# embed. Their release names intentionally match the documentation and their labels are selected
# by the documented Prometheus receiver relabel rules in values-deployment.yaml.
helm upgrade --install ksm prometheus-community/kube-state-metrics \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 10m

helm upgrade --install nodeexporter prometheus-community/prometheus-node-exporter \
  --namespace default \
  --create-namespace \
  --wait \
  --timeout 10m

helm upgrade --install synthkit-otel-prom-deployment "$OTEL_CHART_REF" \
  --version "$CHART_VERSION" \
  --namespace "$LAB_RECEIVER_NAMESPACE" \
  --values "$LAB_PERMUTATION_DIR/values-deployment.yaml" \
  --wait \
  --timeout 10m

helm upgrade --install synthkit-otel-prom-daemonset "$OTEL_CHART_REF" \
  --version "$CHART_VERSION" \
  --namespace "$LAB_RECEIVER_NAMESPACE" \
  --values "$LAB_PERMUTATION_DIR/values-daemonset.yaml" \
  --wait \
  --timeout 10m
