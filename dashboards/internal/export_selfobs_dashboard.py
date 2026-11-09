#!/usr/bin/env python3
"""Export deployment-scoped operational dashboards without contacting a backend."""

import argparse
import copy
import hashlib
import json
import re
import sys
from pathlib import Path

from build_selfobs_dashboard import dashboard as source_dashboard

UID = re.compile(r"[A-Za-z0-9_-]{1,40}\Z")
DEPLOYMENT = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}\Z")
# One attempt per metric token; quoted labels keep escaped quotes and braces together.
SELECTOR = re.compile(
    r'(?<![A-Za-z0-9_:])([A-Za-z_:][A-Za-z0-9_:]*+)\{((?:[^"{}]|"(?:\\.|[^"\\])*+")*+)\}'
)
SIGNALS = {"prometheus": "metrics", "loki": "logs", "tempo": "traces",
           "grafana-pyroscope-datasource": "profiles"}
LANGUAGES = {"metrics": "PromQL", "logs": "LogQL", "traces": "TraceQL", "profiles": "Pyroscope"}


def resource_uid(namespace, role, base=None):
    """Keep resource names bounded without truncating namespace identity."""
    digest = hashlib.sha256(f"{namespace}\0{base or role}".encode()).hexdigest()[:16]
    return f"{namespace[:12]}-{role}-{digest}"


def scope_metrics(expr, deployment):
    """Scope each source selector before rate or aggregation can lose its job."""
    job = json.dumps(f"{deployment}/synthkit")

    def selector(match):
        metric, labels = match.groups()
        if metric == "target_info":
            labels += f", job={job}"
        elif 'job="synthkit"' in labels:
            labels = labels.replace('job="synthkit"', f"job={job}")
        else:
            raise ValueError(f"unsupported operational metric selector: {metric}")
        return f"{metric}{{{labels}}}"

    scoped, count = SELECTOR.subn(selector, expr)
    if not count:
        raise ValueError("operational metric query has no supported selector")
    return scoped


def scope_query(kind, model, deployment):
    value = json.dumps(deployment)
    if kind == "prometheus":
        model["expr"] = scope_metrics(model["expr"], deployment)
        return model["expr"]
    if kind == "loki":
        source = '{service_name="synthkit"}'
        if model["expr"].count(source) != 1:
            raise ValueError("unsupported operational log selector")
        model["expr"] = model["expr"].replace(source, source + f" | service_namespace={value}")
        return model["expr"]
    if kind == "tempo":
        if model["query"] != '{ resource.service.name="synthkit" }':
            raise ValueError("unsupported operational trace selector")
        model["query"] = '{ resource.service.name="synthkit" && resource.service.namespace=' + value + " }"
        return model["query"]
    if kind == "grafana-pyroscope-datasource":
        if model["labelSelector"] != '{service_name="synthkit"}':
            raise ValueError("unsupported operational profile selector")
        model["labelSelector"] = '{service_name="synthkit", env=' + value + "}"
        return model["labelSelector"]
    raise ValueError(f"unsupported query kind: {kind}")


def prune_layout(value, removed):
    """Remove layout references to omitted panels, including empty tabs."""
    if isinstance(value, list):
        return [item for child in value if (item := prune_layout(child, removed)) is not None]
    if not isinstance(value, dict):
        return value
    if value.get("kind") == "GridLayoutItem" and value["spec"]["element"]["name"] in removed:
        return None
    value = {key: prune_layout(child, removed) for key, child in value.items()}
    if value.get("kind") == "GridLayout" and value["spec"]["items"]:
        top = min(item["spec"]["y"] for item in value["spec"]["items"])
        for item in value["spec"]["items"]:
            item["spec"]["y"] -= top
    if value.get("kind") == "TabsLayoutTab":
        layout = value["spec"]["layout"]
        if layout["kind"] == "GridLayout" and not layout["spec"]["items"]:
            return None
    return value


def export(args):
    result = copy.deepcopy(source_dashboard)
    folder_uid = resource_uid(args.namespace, "folder", args.folder or "synthkit-selfmon")
    result["metadata"]["name"] = resource_uid(args.namespace, "selfobs")
    result["metadata"]["annotations"]["grafana.app/folder"] = folder_uid
    result["spec"]["title"] += f" ({args.namespace})"
    result["spec"]["description"] = (
        f"Operational telemetry for deployment {args.deployment}. Metrics, logs and traces require "
        "the matching service.namespace resource attribute; profiles require the matching env tag. "
        "The process picker applies to metrics. See queries.json for coverage and omissions."
    )
    inventory = {"dashboard": result["metadata"]["name"], "deployment": args.deployment,
                 "resource_attribute": "service.namespace", "queries": [], "omissions": []}
    bindings = {"metrics": args.metrics_datasource, "logs": args.logs_datasource,
                "traces": args.traces_datasource, "profiles": args.profiles_datasource}
    scopes = {"metrics": {"dimension": "job", "value": args.deployment + "/synthkit", "process_variable": "instance"},
              "logs": {"dimension": "service_namespace", "value": args.deployment},
              "traces": {"dimension": "resource.service.namespace", "value": args.deployment},
              "profiles": {"dimension": "env", "value": args.deployment}}

    def record(owner_kind, owner, ref_id, signal, expression):
        inventory["queries"].append({"owner_kind": owner_kind, "owner": owner, "ref_id": ref_id,
            "signal": signal, "language": LANGUAGES[signal], "datasource": bindings[signal],
            "scope": scopes[signal], "expression": expression})

    removed = set()
    for key, panel in result["spec"]["elements"].items():
        queries = panel["spec"]["data"]["spec"]["queries"]
        signal = None
        reason = None
        for query in queries:
            kind = query["spec"]["query"]["kind"]
            signal = SIGNALS[kind]
            model = query["spec"]["query"]["spec"]
            if kind == "prometheus" and "traces_spanmetrics_" in model["expr"]:
                signal, reason = "spanmetrics", "backend-derived spanmetrics have no guaranteed deployment dimension"
                break
            if signal not in args.signals:
                reason = "signal not selected for this export"
                break
        if reason:
            removed.add(key)
            inventory["omissions"].append({"panel": key, "signal": signal, "reason": reason})
            continue
        for query in queries:
            spec = query["spec"]
            kind = spec["query"]["kind"]
            signal = SIGNALS[kind]
            expression = scope_query(kind, spec["query"]["spec"], args.deployment)
            spec["datasource"]["uid"] = bindings[signal]
            record("panel", key, spec["refId"], signal, expression)
    for key in removed:
        del result["spec"]["elements"][key]
    result["spec"]["layout"] = prune_layout(result["spec"]["layout"], removed)
    for variable in result["spec"]["variables"]:
        spec = variable["spec"]
        query = spec["query"]["spec"]["query"]
        # Bare metric selectors in label_values need the same job constraint as panel queries.
        query = re.sub(r"label_values\(([A-Za-z_:][A-Za-z0-9_:]*),", r'label_values(\1{job="synthkit"},', query)
        query = scope_metrics(query, args.deployment)
        spec["definition"] = spec["query"]["spec"]["query"] = query
        spec["datasource"]["uid"] = args.metrics_datasource
        record("variable", spec["name"], spec["name"], "metrics", query)
    result["spec"]["elements"]["cf_note"]["spec"]["vizConfig"]["spec"]["options"]["content"] = (
        f"Operational telemetry for **{args.deployment}**. Deployment grouping uses the stable "
        "`service.namespace` resource attribute. The instance picker selects metric processes "
        "inside that deployment; logs, traces and profiles remain deployment-wide. Profiles "
        "require a matching `env` tag. Backend-derived spanmetrics are omitted because the "
        "default projection does not establish deployment identity. Query filtering is not "
        "datasource authorization. Consult the export's `queries.json` for all queries and omissions."
    )
    folder = {"apiVersion": "folder.grafana.app/v1", "kind": "Folder",
              "metadata": {"name": folder_uid}, "spec": {
                  "title": (args.folder_title or "synthkit - Self-Monitoring") + f" ({args.namespace})"}}
    return {"folder.json": folder, "dashboard.json": result, "queries.json": inventory}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--namespace", required=True, help="resource identity namespace, independent of telemetry scope")
    parser.add_argument("--deployment", required=True, help="exact service.namespace value of this deployment")
    parser.add_argument("--out", required=True, type=Path, help="new directory for offline resources and query inventory")
    parser.add_argument("--folder", help="base folder UID, always namespaced; defaults to synthkit-selfmon")
    parser.add_argument("--folder-title", help="base folder title, always suffixed by the namespace")
    parser.add_argument("--metrics-datasource", default="grafanacloud-prom")
    parser.add_argument("--logs-datasource", default="grafanacloud-logs")
    parser.add_argument("--traces-datasource", default="grafanacloud-traces")
    parser.add_argument("--profiles-datasource", default="grafanacloud-profiles")
    parser.add_argument("--signals", default="metrics,logs,traces,profiles", help="comma-separated enabled signals; metrics is required")
    args = parser.parse_args()
    try:
        if not UID.fullmatch(args.namespace):
            raise ValueError("namespace must be 1..40 letters, digits, underscores or hyphens")
        if not DEPLOYMENT.fullmatch(args.deployment):
            raise ValueError("deployment must be 1..128 letters, digits, dots, underscores or hyphens, starting with a letter or digit")
        for name in ("folder", "metrics_datasource", "logs_datasource", "traces_datasource", "profiles_datasource"):
            value = getattr(args, name)
            if value is not None and not UID.fullmatch(value):
                raise ValueError(f"{name} must be a UID of 1..40 letters, digits, underscores or hyphens")
        if args.folder_title is not None and not args.folder_title.strip():
            raise ValueError("folder title must not be blank")
        args.signals = set(args.signals.split(","))
        if "metrics" not in args.signals or args.signals - set(SIGNALS.values()):
            raise ValueError("signals must include metrics and use only metrics,logs,traces,profiles; spanmetrics scope is unsupported")
        resources = export(args)
        # Finish all scope and binding validation before publishing any output.
        args.out.mkdir(parents=True, exist_ok=False)
        for name, resource in resources.items():
            (args.out / name).write_text(json.dumps(resource, indent=2) + "\n")
    except (ValueError, OSError) as exc:
        print(json.dumps({"error": str(exc)}), file=sys.stderr)
        return 2
    print(json.dumps({"output": str(args.out), "dashboard": resources["dashboard.json"]["metadata"]["name"],
                      "omissions": resources["queries.json"]["omissions"]}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
