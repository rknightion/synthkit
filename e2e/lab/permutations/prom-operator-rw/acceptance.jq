# Acceptance is intentionally tied to the lab-catalog ServiceMonitor. Merely seeing these keys
# somewhere in kube-prometheus-stack's own telemetry would not prove the ServiceMonitor envelope.
def receipt($protocol): ([.receipts[]? | select(.protocol == $protocol) | (.count // 0)] | add // 0);
def labels: reduce (.labels[]? // empty) as $label ({}; .[$label.key] = ($label.values // []));
def has_value($labels; $key; $value): (($labels[$key] // []) | index($value)) != null;
def check($name; $ok; $detail): {name: $name, status: (if $ok then "PASS" else "FAIL" end), detail: (if $ok then "" else $detail end)};
def service_monitor_recording_rule:
  .metrics[]?
  | select(.name == "count:up0")
  | labels as $labels
  | select(has_value($labels; "service"; "lab-catalog"));
[
  check("inventory schema version";
    (.schema_version == "synthkit.telemetry.inventory/v1alpha1");
    "the receiver returned an unrecognised inventory schema"),
  check("prometheus_remote_write_v1 receipt";
    (receipt("prometheus_remote_write_v1") > 0);
    "no Prometheus Remote-Write v1 request was decoded"),
  check("ServiceMonitor default job is the Service name";
    any(service_monitor_recording_rule; labels as $labels | has_value($labels; "job"; "lab-catalog"));
    "the lab-catalog ServiceMonitor did not retain job=lab-catalog"),
  check("ServiceMonitor retains service label";
    any(service_monitor_recording_rule; labels as $labels | has_value($labels; "service"; "lab-catalog"));
    "the lab-catalog ServiceMonitor did not retain service=lab-catalog"),
  check("Prometheus external labels arrive on the ServiceMonitor series";
    any(service_monitor_recording_rule; labels as $labels | (($labels.prometheus // []) | length > 0) and (($labels.prometheus_replica // []) | length > 0));
    "the lab-catalog ServiceMonitor series lacked prometheus and/or prometheus_replica")
]
