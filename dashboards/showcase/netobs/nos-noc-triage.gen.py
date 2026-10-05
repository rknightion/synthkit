#!/usr/bin/env python3
"""Build nos-noc-triage.json: the NOC shift triage board (active issues across every exporter)."""
import os
import json

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "nos-noc-triage.json")
PROM = {"type": "prometheus", "uid": "grafanacloud-prom"}
LOKI = {"type": "loki", "uid": "grafanacloud-logs"}
E = 'instance=~"$exporter"'

# ---------------------------------------------------------------------------------------------
# Condition building blocks. Series are pushed roughly once a minute and carry no staleness
# markers, so "absent now" uses a 2m last_over_time window instead of the 5m instant lookback
# (a device that vanishes for 3-5 minutes would otherwise never look absent).
# ---------------------------------------------------------------------------------------------
UP2 = f'last_over_time(network_topology_graph_devices_total{{{E}}}[2m])'
BASE0 = f'(max by (instance) ({UP2}) * 0)'
UNREACH = (f'(max by (instance, device_id, site) (last_over_time(network_topology_device_info{{{E}}}[1h]))'
           f' unless on (instance, device_id) last_over_time(network_topology_device_info{{{E}}}[2m]))'
           f' and on (instance) {UP2}')
EKEYS = "instance, src_device, src_port, dst_device, dst_port, discovery_proto"
EDGELOST = (f'(max by ({EKEYS}) (last_over_time(network_topology_edge_info{{{E}}}[1h]))'
            f' unless on ({EKEYS}) last_over_time(network_topology_edge_info{{{E}}}[2m]))'
            f' and on (instance) {UP2}')
CREDX = f'network_topology_credential_trials_total{{status="failed",{E}}}'


def cred_delta(win):
    # failed-trial series are created by the first failure, so a plain increase() misses it:
    # take the delta against the window start, or the whole value when the series is new.
    return f'sum by (instance) ((({CREDX} - {CREDX} offset {win}) > 0) or ({CREDX} unless {CREDX} offset {win}))'


SKIPS = f'network_topology_cycle_budget_skips_total{{{E}}}'
REJ = f'network_topology_graph_updates_rejected_total{{{E}}}'
STALE2 = f'last_over_time(network_topology_graph_stale{{{E}}}[2m])'
MOD2 = f'last_over_time(network_topology_module_last_status{{{E}}}[2m])'
SPOKE2 = f'last_over_time(network_topology_federation_spoke_up{{{E}}}[2m])'
PROBE6 = 'last_over_time(probe_success[6m])'
SITEMAP = ('(max by (instance, site) (topk by (instance) (1, count by (instance, site) '
           f'(last_over_time(network_topology_device_info{{{E}}}[1h])))) * 0 + 1)')


def lr(expr, dst, repl, src="instance", rx=".*"):
    return f'label_replace({expr}, "{dst}", "{repl}", "{src}", "{rx}")'


def tag(expr, sev, cond):
    return lr(lr(expr, "severity", sev), "condition", cond)


def exporter_lbl(expr):
    return lr(expr, "exporter", "$1", "instance", "(.*):[0-9]+")


def drill_device(expr, dev_label="device_id"):
    j = f'label_join({expr}, "k", "|", "instance", "{dev_label}")'
    return lr(j, "drill", "/d/nos-device-investigation?var-exporter=$1&var-device=$2", "k", "(.*)[|](.*)")


def drill_exporter(expr):
    return lr(expr, "drill", "/d/nos-change-and-discovery?var-exporter=$1", "instance", "(.*)")


def with_site(expr):
    return f'({expr}) * on (instance) group_left (site) {SITEMAP}'


def since_last_ok(bad_by, labels, ok_sel):
    """Timestamp (ms) of the last minute the object was healthy; window start when never healthy in 3h."""
    return (f'((max by ({labels}) (max_over_time(timestamp({ok_sel})[3h:1m])) and on ({labels}) {bad_by})'
            f' or on ({labels}) (max by ({labels}) ({bad_by}) * 0 + time() - 10800)) * 1000')


# ---- active issue rows: each yields severity, condition, instance, exporter, site, object, drill; value = since (ms)
ISSUES = []

# 1 device unreachable
q = (f'(max by (instance, device_id, site) (max_over_time(timestamp(network_topology_device_info{{{E}}})[1h:1m]))'
     f' unless on (instance, device_id) last_over_time(network_topology_device_info{{{E}}}[2m])'
     f' and on (instance) {UP2}) * 1000')
ISSUES.append(drill_device(exporter_lbl(lr(tag(q, "CRITICAL", "Device unreachable"), "object", "$1", "device_id", "(.*)"))))

# 2 exporter not reporting
q = (f'(max by (instance) (max_over_time(timestamp(network_topology_graph_devices_total{{{E}}})[3h:1m]))'
     f' unless on (instance) {UP2}) * 1000')
ISSUES.append(drill_exporter(exporter_lbl(lr(tag(with_site(q), "CRITICAL", "Exporter not reporting"), "object", "discovery exporter"))))

# 3 spoke down
q = (f'max by (instance, spoke_id) (network_topology_federation_spoke_last_push_timestamp_seconds{{{E}}}) * 1000'
     f' and on (instance, spoke_id) ({SPOKE2} == 0)')
q = lr(lr(tag(q, "CRITICAL", "Spoke down"), "object", "$1", "spoke_id", "(.*)"), "site", "$1", "spoke_id", "(.*)")
ISSUES.append(lr(exporter_lbl(q), "drill", "/d/nos-site-investigation?var-site=$1", "spoke_id", "(.*)"))

# 4/5 discovery module hard-failed / degraded
for lvl, sev, cond in ((2, "CRITICAL", "Discovery module hard-failed"), (1, "WARNING", "Discovery module degraded")):
    q = since_last_ok(f'({MOD2} == {lvl})', "instance, module",
                      f'network_topology_module_last_status{{{E}}} == 0')
    ISSUES.append(drill_exporter(exporter_lbl(lr(tag(with_site(q), sev, cond), "object", "$1 module", "module", "(.*)"))))

# 6 graph stale
q = since_last_ok(f'({STALE2} == 1)', "instance", f'network_topology_graph_stale{{{E}}} == 0')
ISSUES.append(drill_exporter(exporter_lbl(lr(tag(with_site(q), "WARNING", "Topology graph stale"), "object", "topology graph"))))

# 7 credential failures in the last 15m
q = (f'min by (instance) (min_over_time(timestamp({CREDX})[15m:1m])) * 1000 and on (instance) ({cred_delta("15m")} > 0)')
ISSUES.append(drill_exporter(exporter_lbl(lr(tag(with_site(q), "WARNING", "SNMP credential failures"), "object", "SNMP credentials"))))

# 8 discovery cycle over budget in the last 15m
q = (f'min by (instance) (min_over_time(timestamp(sum by (instance) (increase({SKIPS}[2m])) > 0)[15m:1m])) * 1000')
ISSUES.append(drill_exporter(exporter_lbl(lr(tag(with_site(q), "WARNING", "Discovery cycle over budget"), "object", "discovery cycle"))))

# 9 federation updates rejected in the last 15m
q = (f'min by (instance, reason) (min_over_time(timestamp(sum by (instance, reason) (increase({REJ}[2m])) > 0.5)[15m:1m])) * 1000')
ISSUES.append(drill_exporter(exporter_lbl(lr(tag(with_site(q), "WARNING", "Federation updates rejected"), "object", "rejected: $1", "reason", "(.*)"))))

# 10 link lost (edge seen in the last hour, absent now)
q = (f'(max by ({EKEYS}) (max_over_time(timestamp(network_topology_edge_info{{{E}}})[1h:1m]))'
     f' unless on ({EKEYS}) last_over_time(network_topology_edge_info{{{E}}}[2m])'
     f' and on (instance) {UP2}) * 1000')
q = f'label_join({q}, "object", " -> ", "src_device", "dst_device")'
ISSUES.append(drill_device(exporter_lbl(with_site(tag(q, "WARNING", "Link lost"))), "src_device"))

# 11 failed probe check
q = (f'(max by (job, probe) (max_over_time(timestamp(probe_success == 1)[3h:1m])) and on (job, probe) ({PROBE6} == 0)'
     f' or on (job, probe) (max by (job, probe) ({PROBE6} == 0) * 0 + time() - 10800)) * 1000')
q = f'({q}) * on (job, probe) group_left (region) (max by (job, probe, region) (sm_check_info) * 0 + 1)'
q = lr(lr(lr(tag(q, "CRITICAL", "Probe check failing"), "object", "$1", "job", "(.*)"),
          "exporter", "probe $1", "probe", "synthkit-(.*)"), "site", "$1", "region", "(.*)")
ISSUES.append(lr(q, "drill", "/d/nos-interface-performance?var-check=$1", "job", "(.*)"))

ISSUE_COLS = "severity, condition, exporter, instance, site, object, drill"
ISSUES = [f'max by ({ISSUE_COLS}) ({x})' for x in ISSUES]


# ---------------------------------------------------------------------------------------------
# panels
# ---------------------------------------------------------------------------------------------
_id = [0]


def nid():
    _id[0] += 1
    return _id[0]


def gp(x, y, w, h):
    return {"x": x, "y": y, "w": w, "h": h}


def prom(expr, legend="__auto", ref="A", instant=False, fmt="time_series", interval=""):
    t = {"datasource": PROM, "refId": ref, "expr": expr, "legendFormat": legend,
         "range": not instant, "instant": instant, "format": fmt, "editorMode": "code"}
    if interval:
        t["interval"] = interval
    return t


def refs(n):
    return [chr(ord("A") + i) for i in range(n)]


panels = []
GREEN, AMBER, RED = "green", "orange", "red"

# ---- active issues strip
STATS = [
    ("Unreachable", f'count({UNREACH}) or vector(0)', RED,
     "Devices polled in the last hour that no longer report network_topology_device_info, on exporters that are themselves still reporting."),
    ("Stale graphs", f'count({STALE2} == 1) or vector(0)', AMBER,
     "Exporters whose network_topology_graph_stale is 1: the last discovery cycle did not complete, so the graph is being served from the previous cycle."),
    ("Modules not OK", f'count({MOD2} > 0) or vector(0)', AMBER,
     "Discovery modules (lldp, cdp, bgp, ospf, fdb, isis, mpls_te) with network_topology_module_last_status 1 (degraded) or 2 (hard-failed). The issues table splits the two."),
    ("Spokes down", f'count({SPOKE2} == 0) or vector(0)', RED,
     "Federated spokes the global hub marks network_topology_federation_spoke_up = 0."),
    ("Auth fails 15m", f'round(sum({cred_delta("15m")})) or vector(0)', AMBER,
     "Failed SNMP credential trials in the last 15 minutes, from network_topology_credential_trials_total{status=\"failed\"}."),
    ("Links lost 1h", f'count({EDGELOST}) or vector(0)', AMBER,
     "Adjacencies (network_topology_edge_info) discovered in the last hour that are absent from the current graph."),
    ("Probes failing", f'count({PROBE6} == 0) or vector(0)', RED,
     "External probe checks whose latest probe_success is 0."),
    ("Over budget 15m", f'round(sum(increase({SKIPS}[15m]))) or vector(0)', AMBER,
     "Discovery cycles skipped in the last 15 minutes because the previous cycle overran its budget (network_topology_cycle_budget_skips_total)."),
]
for i, (title, expr, col, desc) in enumerate(STATS):
    panels.append({
        "id": nid(), "type": "stat", "title": title, "description": desc, "datasource": PROM,
        "gridPos": gp(i * 3, 0, 3, 4),
        "targets": [prom(expr, title, "A", interval="1m")],
        "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "colorMode": "background", "graphMode": "area", "justifyMode": "center",
                    "textMode": "value", "wideLayout": True, "showPercentChange": False,
                    "orientation": "auto", "text": {"valueSize": 40}},
        "fieldConfig": {"defaults": {
            "unit": "none", "decimals": 0, "min": 0, "color": {"mode": "thresholds"},
            "thresholds": {"mode": "absolute", "steps": [{"color": GREEN, "value": None}, {"color": col, "value": 1}]}},
            "overrides": []},
    })

# ---- ranked active issues table
links_cfg = [{"title": "Open drilldown for ${__data.fields.object}",
              "url": "${__data.fields.drill}&${__url_time_range}"}]
panels.append({
    "id": nid(), "type": "table", "title": "Active issues",
    "description": "Every open condition across all discovery exporters, federated spokes and external probes, critical first. "
                   "Since is the last healthy sample (last seen for a device or link, last push for a spoke, first failure in the window for counters). "
                   "Click the object to open its drilldown: devices and links open Device Investigation, spokes open Site Investigation, "
                   "exporter-level conditions open Change Intelligence and Discovery Health, probe checks open Interface and Path Performance.",
    "datasource": PROM,
    "gridPos": gp(0, 4, 16, 12),
    "targets": [prom(x, "", r, instant=True, fmt="table") for x, r in zip(ISSUES, refs(len(ISSUES)))],
    "transformations": [
        {"id": "merge", "options": {}},
        {"id": "calculateField", "options": {"mode": "reduceRow", "alias": "Since", "replaceFields": False,
                                             "reduce": {"reducer": "max", "include": [f"Value #{r}" for r in refs(len(ISSUES))]}}},
        {"id": "organize", "options": {
            "excludeByName": dict({"Time": True, "instance": True}, **{f"Value #{r}": True for r in refs(len(ISSUES))}),
            "indexByName": {"severity": 0, "condition": 1, "exporter": 2, "site": 3, "object": 4, "Since": 5, "drill": 6},
            "renameByName": {"severity": "Severity", "condition": "Condition", "exporter": "Exporter / probe",
                             "site": "Site / region", "object": "Object"}}},
        {"id": "sortBy", "options": {"fields": {}, "sort": [{"field": "Severity", "desc": False}]}},
    ],
    "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False, "reducer": ["count"], "countRows": True},
                "sortBy": []},
    "fieldConfig": {"defaults": {"custom": {"align": "left", "cellOptions": {"type": "auto"}, "filterable": True},
                                 "noValue": "No active issues"},
                    "overrides": [
        {"matcher": {"id": "byName", "options": "Severity"}, "properties": [
            {"id": "custom.width", "value": 110},
            {"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "basic"}},
            {"id": "mappings", "value": [{"type": "value", "options": {
                "CRITICAL": {"color": "red", "index": 0}, "WARNING": {"color": "orange", "index": 1}}}]}]},
        {"matcher": {"id": "byName", "options": "Condition"}, "properties": [{"id": "custom.width", "value": 250}]},
        {"matcher": {"id": "byName", "options": "Exporter / probe"}, "properties": [{"id": "custom.width", "value": 190}]},
        {"matcher": {"id": "byName", "options": "Site / region"}, "properties": [{"id": "custom.width", "value": 130}]},
        {"matcher": {"id": "byName", "options": "Object"}, "properties": [
            {"id": "links", "value": links_cfg},
            {"id": "custom.cellOptions", "value": {"type": "auto"}}]},
        {"matcher": {"id": "byName", "options": "Since"}, "properties": [
            {"id": "unit", "value": "dateTimeFromNow"}, {"id": "custom.width", "value": 150}]},
        {"matcher": {"id": "byName", "options": "drill"}, "properties": [{"id": "custom.hidden", "value": True}]},
    ]},
})

# ---- how to triage
TRIAGE = """### How to triage

1. **Read the strip left to right.** Red is a service-affecting condition (device, spoke or probe path down). Amber means discovery or polling is impaired and the graph may be out of date. The sparkline shows the last three hours: a green tile with a spike is a fault that has cleared.
2. **Work the Active issues table top down.** Critical rows sort first. *Since* is when the object was last healthy. Click the object to open the right drilldown with the time range carried over.
3. **Check whether it is one fault or many.** Several unreachable devices behind one exporter, or every spoke dropping at once, usually means the poller or the federation path, not the devices. Look at *Exporter not reporting* and *Spokes down* before opening device tickets.
4. **Use Condition history for duration and flapping.** A condition that clears and returns on a regular cycle is a recurring fault: raise a problem record, not another incident.
5. **Confirm with the event feed.** Topology change lines show adjacencies being added or removed, and conflict lines show LLDP and CDP disagreeing about a neighbour. Disagreement on a host port is usually a mis-patched server, not a switch fault."""
panels.append({
    "id": nid(), "type": "text", "title": "Shift triage guide",
    "gridPos": gp(16, 4, 8, 12),
    "options": {"mode": "markdown", "content": TRIAGE, "code": {"language": "markdown", "showLineNumbers": False, "showMiniMap": False}},
})

# ---- per-exporter health matrix
MATRIX = [
    ("Reporting", f'max by (instance) ({UP2} * 0 + 1) or on (instance) (max by (instance) (last_over_time(network_topology_graph_devices_total{{{E}}}[3h])) * 0)', "bool"),
    ("Devices", f'max by (instance) (last_over_time(network_topology_graph_devices_total{{{E}}}[2m]))', "info"),
    ("Unreachable", f'count by (instance) ({UNREACH}) or on (instance) {BASE0}', RED),
    ("Graph stale", f'max by (instance) ({STALE2})', "stale"),
    ("Worst module", f'max by (instance) ({MOD2})', "module"),
    ("Spokes down", f'count by (instance) ({SPOKE2} == 0) or on (instance) (max by (instance) ({SPOKE2}) * 0)', RED),
    ("Auth fails 15m", f'round({cred_delta("15m")}) or on (instance) {BASE0}', AMBER),
    ("Links lost 1h", f'count by (instance) ({EDGELOST}) or on (instance) {BASE0}', AMBER),
    ("Overruns 15m", f'round(sum by (instance) (increase({SKIPS}[15m])))', AMBER),
    ("Rejected 15m", f'round(sum by (instance) (increase({REJ}[15m])))', AMBER),
    ("Conflicts 1h", "LOKI", AMBER),
]
LOKI_CONFLICTS = f'sum by (instance) (count_over_time({{source="network-topology-exporter", conflict_type=~".+", {E}}}[1h]))'

mtargets, mrename, mover = [], {}, []
order = {"instance": 0, "site": 1}
for i, ((name, expr, kind), r) in enumerate(zip(MATRIX, refs(len(MATRIX)))):
    if expr == "LOKI":
        mtargets.append({"datasource": LOKI, "refId": r, "expr": LOKI_CONFLICTS, "queryType": "instant",
                         "instant": True, "range": False, "editorMode": "code", "legendFormat": ""})
    else:
        mtargets.append(prom(expr, "", r, instant=True, fmt="table"))
    mrename[f"Value #{r}"] = name
    order[f"Value #{r}"] = i + 2
# exporter -> site lookup
mtargets.append(prom(f'max by (instance, site) (topk by (instance) (1, count by (instance, site) (last_over_time(network_topology_device_info{{{E}}}[1h]))))',
                     "", "Z", instant=True, fmt="table"))


def cellbg(steps, mappings=None):
    props = [{"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "basic"}},
             {"id": "color", "value": {"mode": "thresholds"}},
             {"id": "thresholds", "value": {"mode": "absolute", "steps": steps}},
             {"id": "custom.align", "value": "center"}]
    if mappings:
        props.append({"id": "mappings", "value": mappings})
    return props


for name, expr, kind in MATRIX:
    if kind == "bool":
        p = cellbg([{"color": RED, "value": None}, {"color": GREEN, "value": 1}],
                   [{"type": "value", "options": {"0": {"text": "SILENT"}, "1": {"text": "YES"}}}])
    elif kind == "info":
        p = [{"id": "custom.align", "value": "center"}]
    elif kind == "stale":
        p = cellbg([{"color": GREEN, "value": None}, {"color": AMBER, "value": 1}],
                   [{"type": "value", "options": {"0": {"text": "fresh"}, "1": {"text": "STALE"}}}])
    elif kind == "module":
        p = cellbg([{"color": GREEN, "value": None}, {"color": AMBER, "value": 1}, {"color": RED, "value": 2}],
                   [{"type": "value", "options": {"0": {"text": "OK"}, "1": {"text": "DEGRADED"}, "2": {"text": "HARD-FAILED"}}}])
    else:
        p = cellbg([{"color": GREEN, "value": None}, {"color": kind, "value": 1}])
    mover.append({"matcher": {"id": "byName", "options": name}, "properties": p})
mover.append({"matcher": {"id": "byName", "options": "Exporter"}, "properties": [
    {"id": "custom.width", "value": 200},
    {"id": "links", "value": [{"title": "Discovery health for ${__value.raw}",
                               "url": "/d/nos-change-and-discovery?var-exporter=${__value.raw}&${__url_time_range}"}]}]})
mover.append({"matcher": {"id": "byName", "options": "Site"}, "properties": [{"id": "custom.width", "value": 100}]})
mrename.update({"instance": "Exporter", "site": "Site"})
excl = {f"Time {i}": True for i in range(1, 16)}
excl.update({"Time": True, "Value #Z": True})
panels.append({
    "id": nid(), "type": "table", "title": "Exporter health matrix",
    "description": "One row per discovery exporter, one column per condition, as of now. Reporting: the exporter pushed network_topology_graph_devices_total in the last 2 minutes. "
                   "Spokes down and Rejected (federation updates rejected) only apply to the global federation hub and are blank elsewhere. Click an exporter for its discovery health board.",
    "datasource": {"type": "datasource", "uid": "-- Mixed --"},
    "gridPos": gp(0, 16, 24, 6),
    "targets": mtargets,
    "transformations": [
        {"id": "labelsToFields", "options": {"mode": "columns"}},
        {"id": "joinByField", "options": {"byField": "instance", "mode": "outer"}},
        {"id": "organize", "options": {"excludeByName": excl, "indexByName": order, "renameByName": mrename}},
    ],
    "options": {"showHeader": True, "cellHeight": "md", "footer": {"show": False, "reducer": ["sum"]}},
    "fieldConfig": {"defaults": {"custom": {"align": "center", "cellOptions": {"type": "auto"}, "width": 128}, "noValue": "-"},
                    "overrides": mover},
})

# ---- condition history, repeated per exporter
HIST = [
    ("Exporter reporting", f'(max by (instance) ({UP2}) * 0) or on (instance) (max by (instance) (last_over_time(network_topology_graph_devices_total{{{E}}}[3h])) * 0 + 2)'),
    ("Devices unreachable", f'((count by (instance) ({UNREACH}) > 0) * 0 + 2) or on (instance) {BASE0}'),
    ("Discovery modules", f'max by (instance) ({MOD2})'),
    ("Topology graph", f'max by (instance) ({STALE2})'),
    ("SNMP credentials", f'(({cred_delta("5m")} > 0) * 0 + 1) or on (instance) {BASE0}'),
    ("Discovery cycle budget", f'((sum by (instance) (increase({SKIPS}[5m])) > 0) * 0 + 1) or on (instance) {BASE0}'),
    ("Links", f'((count by (instance) ({EDGELOST}) > 0) * 0 + 1) or on (instance) {BASE0}'),
    ("Federated spokes", f'((count by (instance) ({SPOKE2} == 0) > 0) * 0 + 2) or on (instance) (max by (instance) ({SPOKE2}) * 0)'),
    ("Federation push", f'((sum by (instance) (increase(network_topology_federation_spoke_push_failures_total{{{E}}}[5m])) > 0) * 0 + 1)'
                        f' or on (instance) (sum by (instance) (increase(network_topology_federation_spoke_push_failures_total{{{E}}}[5m])) * 0)'),
    ("Federation updates", f'((sum by (instance) (increase({REJ}[5m])) > 0.5) * 0 + 1) or on (instance) (sum by (instance) (increase({REJ}[5m])) * 0)'),
]
STATE_MAP = [{"type": "value", "options": {"0": {"text": "OK", "color": "green", "index": 0},
                                           "1": {"text": "WARN", "color": "orange", "index": 1},
                                           "2": {"text": "CRIT", "color": "red", "index": 2}}}]
panels.append({
    "id": nid(), "type": "state-timeline", "title": "Condition history - $exporter",
    "description": "Each condition for this exporter over the selected range: green OK, amber impaired, red service-affecting. Gaps mean the condition does not apply to this exporter. "
                   "Use it to judge duration and spot conditions that clear and return on a cycle.",
    "datasource": PROM,
    "repeat": "exporter", "repeatDirection": "h", "maxPerRow": 3,
    "gridPos": gp(0, 22, 8, 10),
    "targets": [prom(x.replace('instance=~"$exporter"', 'instance="$exporter"'), name, r, interval="1m")
                for (name, x), r in zip(HIST, refs(len(HIST)))],
    "options": {"showValue": "never", "mergeValues": True, "alignValue": "center", "rowHeight": 0.82,
                "legend": {"showLegend": False, "displayMode": "list", "placement": "bottom"},
                "tooltip": {"mode": "single", "sort": "none"}},
    "fieldConfig": {"defaults": {
        "color": {"mode": "thresholds"},
        "thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": None}, {"color": "orange", "value": 1}, {"color": "red", "value": 2}]},
        "mappings": STATE_MAP, "custom": {"fillOpacity": 85, "lineWidth": 0}}, "overrides": []},
})

# ---- issues over time
TREND = [
    ("Devices unreachable", f'count({UNREACH})', "red"),
    ("Spokes down", f'count({SPOKE2} == 0)', "dark-red"),
    ("Probe checks failing", f'count({PROBE6} == 0)', "purple"),
    ("Modules not OK", f'count({MOD2} > 0)', "orange"),
    ("Stale graphs", f'count({STALE2} == 1)', "yellow"),
    ("Exporters with credential failures", f'count({cred_delta("5m")} > 0)', "blue"),
    ("Exporters over cycle budget", f'count(sum by (instance) (increase({SKIPS}[5m])) > 0)', "light-blue"),
    ("Links lost", f'count({EDGELOST})', "dark-orange"),
]
TREND_ID = nid()
panels.append({
    "id": TREND_ID, "type": "timeseries", "title": "Open issues over time",
    "description": "Number of objects in each condition per minute across the selected exporters and all probe checks. Annotation markers show unreachable-device, spoke-down and stale-graph windows.",
    "datasource": PROM,
    "gridPos": gp(0, 32, 14, 9),
    "targets": [prom(x, name, r, interval="1m") for (name, x, _), r in zip(TREND, refs(len(TREND)))],
    "options": {"legend": {"showLegend": True, "displayMode": "table", "placement": "right", "calcs": ["max"]},
                "tooltip": {"mode": "multi", "sort": "desc"}},
    "fieldConfig": {"defaults": {
        "unit": "none", "decimals": 0, "min": 0,
        "custom": {"drawStyle": "bars", "fillOpacity": 80, "lineWidth": 0, "stacking": {"mode": "normal", "group": "A"},
                   "barAlignment": 0, "showPoints": "never", "axisSoftMax": 4}},
        "overrides": [{"matcher": {"id": "byName", "options": n},
                       "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": c}}]} for n, _, c in TREND]},
})

# ---- probe check history
panels.append({
    "id": nid(), "type": "status-history", "title": "External probe checks",
    "description": "probe_success per check and probe location over the range: green pass, red fail. Check identity is the check job; the probe location is shown in brackets.",
    "datasource": PROM,
    "gridPos": gp(14, 32, 10, 9),
    "targets": [prom(lr('min by (job, probe) (probe_success)', "loc", "$1", "probe", "synthkit-(.*)"),
                     "{{job}} ({{loc}})", "A", interval="2m")],
    "maxDataPoints": 90,
    "options": {"showValue": "never", "rowHeight": 0.85, "colWidth": 0.95,
                "legend": {"showLegend": False, "displayMode": "list", "placement": "bottom"},
                "tooltip": {"mode": "single", "sort": "none"}},
    "fieldConfig": {"defaults": {
        "color": {"mode": "thresholds"},
        "thresholds": {"mode": "absolute", "steps": [{"color": "red", "value": None}, {"color": "green", "value": 1}]},
        "mappings": [{"type": "value", "options": {"0": {"text": "FAIL", "color": "red", "index": 0},
                                                    "1": {"text": "PASS", "color": "green", "index": 1}}}],
        "custom": {"fillOpacity": 85, "lineWidth": 0}}, "overrides": []},
})

# ---- recent events feed
panels.append({
    "id": nid(), "type": "logs", "title": "Recent topology events",
    "description": "Newest first from {source=\"network-topology-exporter\"}: event=topology_change (adjacency added or removed) and event=topology_conflict (discovery protocols disagree about a neighbour).",
    "datasource": LOKI,
    "gridPos": gp(0, 41, 24, 11),
    "targets": [{"datasource": LOKI, "refId": "A", "queryType": "range", "editorMode": "code",
                 "expr": f'{{source="network-topology-exporter", {E}}} | logfmt '
                         '| line_format "{{.instance}}  {{if .change_kind}}LINK {{.change_kind}} {{.proto}}  {{.src_device}} {{.src_port}} -> {{.dst_device}} {{.dst_port}} ({{.direction}}){{else}}CONFLICT {{.conflict_type}}  {{.src_device}} {{.src_port}}  sources={{.sources}}{{end}}"'}],
    "options": {"showTime": True, "showLabels": False, "showCommonLabels": False, "wrapLogMessage": False,
                "prettifyLogMessage": False, "enableLogDetails": True, "dedupStrategy": "none",
                "sortOrder": "Descending", "enableInfiniteScrolling": False},
})


def prom_anno(name, expr, color, title, text):
    return {"name": name, "datasource": PROM, "enable": True, "hide": False, "iconColor": color,
            "expr": expr, "step": "60s", "titleFormat": title, "textFormat": text, "tagKeys": "instance",
            "useValueForTime": False, "filter": {"exclude": False, "ids": [TREND_ID]},
            "target": {"refId": "Anno", "expr": expr, "interval": "60s", "datasource": PROM}}


annotations = [
    {"builtIn": 1, "datasource": {"type": "grafana", "uid": "-- Grafana --"}, "enable": True, "hide": True,
     "iconColor": "rgba(0, 211, 255, 1)", "name": "Annotations & Alerts", "type": "dashboard"},
    prom_anno("Devices unreachable", f'count by (instance) ({UNREACH})', "red", "Devices unreachable", "{{instance}}"),
    prom_anno("Spokes down", f'count by (instance) ({SPOKE2} == 0)', "purple", "Federated spokes down", "{{instance}}"),
    prom_anno("Graph stale", f'max by (instance) ({STALE2} == 1)', "orange", "Topology graph stale", "{{instance}}"),
]

dash = {
    "apiVersion": "dashboard.grafana.app/v1beta1",
    "kind": "Dashboard",
    "metadata": {"name": "nos-noc-triage", "annotations": {"grafana.app/folder": "netobs-showcase"}},
    "spec": {
        "title": "Network Observability - NOC Triage",
        "description": "Shift triage for the global network: what is broken right now, where, since when, and which drilldown to open next, across every discovery exporter, federated spoke and external probe.",
        "uid": "nos-noc-triage",
        "tags": ["netobs-showcase", "noc", "triage"],
        "editable": True, "graphTooltip": 1, "refresh": "1m", "schemaVersion": 41,
        "timezone": "utc",
        "time": {"from": "now-3h", "to": "now"},
        "timepicker": {"refresh_intervals": ["30s", "1m", "5m"]},
        "links": [{"type": "dashboards", "tags": ["netobs-showcase"], "asDropdown": True, "title": "Network Operations",
                   "includeVars": False, "keepTime": True, "targetBlank": False, "icon": "external link"}],
        "templating": {"list": [{
            "name": "exporter", "label": "Discovery exporter", "type": "query", "datasource": PROM,
            "query": {"qryType": 1, "query": "label_values(network_topology_graph_devices_total, instance)", "refId": "PrometheusVariableQueryEditor-VariableQuery"},
            "definition": "label_values(network_topology_graph_devices_total, instance)",
            "current": {"selected": True, "text": ["All"], "value": ["$__all"]}, "refresh": 2, "sort": 1,
            "multi": True, "includeAll": True, "allValue": ".*", "options": [],
        }]},
        "annotations": {"list": annotations},
        "panels": panels,
    },
}
with open(OUT, "w") as f:
    json.dump(dash, f, indent=2)
print("wrote", OUT, len(panels), "panels")
