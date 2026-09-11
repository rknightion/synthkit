# SPDX-License-Identifier: AGPL-3.0-only
# The documented method has Prometheus Remote-Write metrics and OTLP logs/events. The four
# producer-identity checks are source configuration claims, not inferred metric family names, and
# the log check accepts either documented OTLP-producing lane so an event-only lab is still
# diagnosed honestly.
def receipt($protocol): ([.receipts[]? | select(.protocol == $protocol) | (.count // 0)] | add // 0);
def metric_label_key($key): any(.metrics[]?.labels[]?; .key == $key);
def metric_producer($producer): any(.metrics[]?.producers[]?; .name == $producer);
def check($name; $ok; $detail): {name: $name, status: (if $ok then "PASS" else "FAIL" end), detail: (if $ok then "" else $detail end)};
[
  check("inventory schema version";
    (.schema_version == "synthkit.telemetry.inventory/v1alpha1");
    "the receiver returned an unrecognised inventory schema"),
  check("prometheus_remote_write_v1 receipt";
    (receipt("prometheus_remote_write_v1") > 0);
    "no Prometheus Remote-Write v1 request was decoded"),
  check("documented Prometheus scrape jobs";
    (metric_producer("promrw/integrations/kubernetes/cadvisor")
     and metric_producer("promrw/integrations/kubernetes/kubelet")
     and metric_producer("promrw/integrations/kubernetes/kube-state-metrics")
     and metric_producer("promrw/integrations/node_exporter"));
    "the required producer identities promrw/integrations/kubernetes/cadvisor, promrw/integrations/kubernetes/kube-state-metrics, promrw/integrations/kubernetes/kubelet, and promrw/integrations/node_exporter were not all observed"),
  check("consumed metric job labels";
    (metric_label_key("job") | not);
    "a captured metric retained a job label after producer attribution"),
  check("otlp log or event receipt";
    (receipt("otlp_logs") > 0);
    "no OTLP log request was decoded from the documented pod-log or cluster-event lane"),
  check("decoded metric families";
    (((.metrics // []) | length) > 0);
    "the receiver decoded no metric family")
]
