# SPDX-License-Identifier: AGPL-3.0-only
# Acceptance predicate for the Alloy default permutation, evaluated against the raw receiver
# inventory. It returns one named check per claim so a partial capture can say WHICH claim was
# not met rather than only that the run timed out.
def receipt($protocol): ([.receipts[]? | select(.protocol == $protocol) | (.count // 0)] | add // 0);
def metric_label($key): any(.metrics[]?.labels[]?; .key == $key and ((.values // []) | length) > 0);
def label_value($key; $value): any(.metrics[]?.labels[]?; .key == $key and (((.values // []) | index($value)) != null));
def check($name; $ok; $detail): {name: $name, status: (if $ok then "PASS" else "FAIL" end), detail: (if $ok then "" else $detail end)};
[
  check("inventory schema version";
    (.schema_version == "synthkit.telemetry.inventory/v1alpha1");
    "the receiver returned an unrecognised inventory schema"),
  check("prometheus_remote_write_v1 receipt";
    (receipt("prometheus_remote_write_v1") > 0);
    "no Prometheus Remote-Write v1 request was decoded"),
  check("loki receipt";
    (receipt("loki") > 0);
    "no Loki push was decoded, so the Loki-native pod-log lane produced nothing"),
  check("ambient metric labels";
    (metric_label("cluster") and metric_label("k8s_cluster_name") and metric_label("job") and metric_label("instance") and label_value("source"; "kubernetes"));
    "cluster, k8s_cluster_name, job, instance and source=kubernetes were not all observed"),
  check("loki pod-log stream";
    (any(.logs[]?; .transport == "loki"));
    "no Loki-transport log stream was decoded")
]
