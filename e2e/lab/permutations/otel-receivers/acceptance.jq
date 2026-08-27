# SPDX-License-Identifier: AGPL-3.0-only
# Acceptance predicate for the OTel-native receiver permutation. It deliberately asserts only
# on transport receipts and decoded volume: this permutation's metric and attribute NAMES are
# exactly what the capture exists to discover, so naming one here would be asserting the answer
# rather than observing it.
def receipt($protocol): ([.receipts[]? | select(.protocol == $protocol) | (.count // 0)] | add // 0);
def check($name; $ok; $detail): {name: $name, status: (if $ok then "PASS" else "FAIL" end), detail: (if $ok then "" else $detail end)};
[
  check("inventory schema version";
    (.schema_version == "synthkit.telemetry.inventory/v1alpha1");
    "the receiver returned an unrecognised inventory schema"),
  check("otlp_metrics receipt";
    (receipt("otlp_metrics") > 0);
    "no OTLP metrics request was decoded, so neither the hostmetrics nor the k8s_cluster receiver produced anything"),
  check("otlp_logs receipt";
    (receipt("otlp_logs") > 0);
    "no OTLP log request was decoded, so neither the filelog nor the k8sobjects receiver produced anything"),
  check("decoded metric families";
    (((.metrics // []) | length) > 0);
    "the receiver decoded no metric family")
]
