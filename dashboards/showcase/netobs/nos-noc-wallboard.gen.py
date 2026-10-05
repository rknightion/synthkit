#!/usr/bin/env python3
"""Build nos-noc-wallboard.json (NOC wallboard) with a hand-authored flow-panel SVG."""
import os
import json

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "nos-noc-wallboard.json")
PROM = {"type": "prometheus", "uid": "grafanacloud-prom"}
LOKI = {"type": "loki", "uid": "grafanacloud-logs"}
DC1 = "netobs-ent-dc1:9100"

# Adjacency taken from live network_topology_edge_info{instance="netobs-ent-dc1:9100"} (2026-10-05).
LEAVES = [f"leaf-{i:02d}" for i in range(1, 7)]
SPINES = ["spine-01", "spine-02"]
FW = "edge-fw-01"
EDGES = [(FW, s, "bgp") for s in SPINES]
EDGES += [(s, l, "lldp") for l in LEAVES for s in SPINES]
HOSTS = {l: [f"host-leaf{l[-2:]}-{j:02d}" for j in range(1, 5)] for l in LEAVES}
EDGES += [(h, l, "lldp") for l in LEAVES for h in HOSTS[l]]
assert len(EDGES) == 38

# Theme-neutral palette: transparent background, translucent grey surfaces, and text in
# currentColor so the flow panel (which inlines the SVG) inherits the Grafana theme text colour.
W, H = 1600, 620
NEUTRAL = "#8e8e8e"          # translucent surfaces and outlines, readable on light and dark
UNBOUND = "#8e8e8e"          # no-data colour for tiles, chips and links
CHIP_TXT = "#101114"         # dark digits on coloured host chips
PRI = 'fill="currentColor"'
SEC = 'fill="currentColor" fill-opacity="0.68"'

svg = []
a = svg.append
a(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" '
  f'font-family="Inter, Helvetica, Arial, sans-serif">')

# tier bands
def band(y, h, label):
    a(f'<rect x="4" y="{y}" width="{W-8}" height="{h}" rx="10" fill="{NEUTRAL}" fill-opacity="0.07" '
      f'stroke="{NEUTRAL}" stroke-opacity="0.35" stroke-width="1"/>')
    a(f'<text x="26" y="{y+h/2:.1f}" font-size="13" font-weight="700" letter-spacing="2" {SEC} '
      f'text-anchor="middle" transform="rotate(-90 26 {y+h/2:.1f})">{label}</text>')

band(4, 108, "EDGE")
band(120, 108, "SPINE")
band(236, 116, "LEAF")
band(360, 220, "SERVERS")

pos = {}
pos[FW] = (800, 58, 360, 74)
for i, s in enumerate(SPINES):
    pos[s] = (500 + 600 * i, 174, 280, 74)
slot = (W - 64) / 6
for i, l in enumerate(LEAVES):
    cx = 48 + slot * i + slot / 2
    pos[l] = (cx, 294, 236, 74)
    for j, h in enumerate(HOSTS[l]):
        hx = cx - 87 + j * 58
        pos[h] = (hx, 500, 48, 48)

def bottom(d):
    x, y, w, h = pos[d]
    return x, y + h / 2

def top(d):
    x, y, w, h = pos[d]
    return x, y - h / 2

# links first (under the tiles)
for src, dst, proto in EDGES:
    cid = f"L-{src}-{dst}"
    if src == FW:
        x1, y1 = bottom(src); x2, y2 = top(dst)
        dash = ' stroke-dasharray="10 6"'
        sw = 4
    elif src.startswith("spine"):
        x1, y1 = bottom(src); x2, y2 = top(dst)
        dash = ""
        sw = 3
    else:  # host -> leaf: straight drop from the leaf to each host chip
        x2, y2 = top(src)
        x1, y1 = x2, pos[dst][1] + pos[dst][3] / 2
        dash = ""
        sw = 3
    a(f'<g id="{cid}"><title>{src} - {dst} ({proto})</title>'
      f'<line x1="{x1:.1f}" y1="{y1:.1f}" x2="{x2:.1f}" y2="{y2:.1f}" stroke="{UNBOUND}" '
      f'stroke-width="{sw}" stroke-linecap="round"{dash}/></g>')

# protocol tags on the BGP sessions
for s in SPINES:
    x1, y1 = bottom(FW); x2, y2 = top(s)
    mx, my = (x1 + x2) / 2, (y1 + y2) / 2
    a(f'<rect x="{mx-26:.1f}" y="{my-11:.1f}" width="52" height="22" rx="11" fill="{NEUTRAL}" fill-opacity="0.22" '
      f'stroke="{NEUTRAL}" stroke-opacity="0.6"/>')
    a(f'<text x="{mx:.1f}" y="{my+4:.1f}" font-size="12" font-weight="700" {PRI} text-anchor="middle">BGP</text>')

def device_tile(d, name, sub):
    x, y, w, h = pos[d]
    x0, y0 = x - w / 2, y - h / 2
    # cell group: rect 1 = tile body (stroke driven), rect 2 = status bar (fill driven)
    a(f'<g id="{d}"><title>{d}</title>'
      f'<rect x="{x0:.1f}" y="{y0:.1f}" width="{w}" height="{h}" rx="8" fill="{NEUTRAL}" fill-opacity="0.12" '
      f'stroke="{UNBOUND}" stroke-width="3"/>'
      f'<rect x="{x0+6:.1f}" y="{y0+6:.1f}" width="10" height="{h-12}" rx="4" fill="{UNBOUND}" stroke="none"/>'
      f'</g>')
    a(f'<text x="{x0+26:.1f}" y="{y0+31:.1f}" font-size="22" font-weight="700" {PRI}>{name}</text>')
    a(f'<text x="{x0+26:.1f}" y="{y0+57:.1f}" font-size="14" {SEC}>{sub}</text>')
    # uptime readout cell (label and label colour driven)
    a(f'<g id="up-{d}"><text x="{x0+w-12:.1f}" y="{y0+30:.1f}" font-size="18" font-weight="700" '
      f'fill="currentColor" text-anchor="end">--</text></g>')

device_tile(FW, "edge-fw-01", "Cisco IOS-XE 17.12.1 - edge firewall")
vend = {"arista": "Arista EOS 4.36.0F", "cisco": "Cisco IOS-XE 17.12.1"}
dev_vendor = {"spine-01": "arista", "spine-02": "cisco"}
for i, l in enumerate(LEAVES):
    dev_vendor[l] = "arista" if i % 2 == 0 else "cisco"
for s in SPINES:
    device_tile(s, s, vend[dev_vendor[s]])
for l in LEAVES:
    device_tile(l, l, vend[dev_vendor[l]])

# host chips
for l in LEAVES:
    for j, h in enumerate(HOSTS[l]):
        x, y, w, hh = pos[h]
        a(f'<g id="{h}"><title>{h}</title><rect x="{x-w/2:.1f}" y="{y-hh/2:.1f}" width="{w}" height="{hh}" rx="7" '
          f'fill="{UNBOUND}" stroke="none"/></g>')
        a(f'<text x="{x:.1f}" y="{y+6:.1f}" font-size="17" font-weight="700" fill="{CHIP_TXT}" text-anchor="middle">{j+1:02d}</text>')
    cx = pos[l][0]
    a(f'<text x="{cx:.1f}" y="552" font-size="14" {SEC} text-anchor="middle">host-leaf{l[-2:]}-01..04</text>')

# legend
ly = 590
items = [("#56a64b", "up 24h+"), ("#fade2a", "rebooted &lt; 24h"), ("#ff9830", "rebooted &lt; 1h"), ("#e02f44", "unreachable / link lost"), (UNBOUND, "no data")]
lx = 48
for col, txt in items:
    a(f'<rect x="{lx}" y="{ly}" width="18" height="18" rx="4" fill="{col}"/>')
    a(f'<text x="{lx+26}" y="{ly+14}" font-size="15" {SEC}>{txt}</text>')
    lx += 64 + len(txt.replace("&lt;", "<")) * 8
a(f'<text x="{W-24}" y="{ly+14}" font-size="15" {SEC} text-anchor="end">'
  f'Tiles: device uptime. Lines: discovered adjacency (edge_info). Dashed: BGP session.</text>')
a('</svg>')
SVG = "".join(svg)

# ---- panel config yaml
# -1 = unreachable (device seen in the last hour but its series has gone), see the "or" fallbacks below
UPTIME_TH = [("red", -1), ("orange", 0), ("yellow", 3600), ("green", 86400)]
LINK_TH = [("red", 0), ("green", 1)]

def th(lst, indent):
    sp = " " * indent
    return "".join(f'{sp}- {{color: "{c}", level: {v}}}\n' for c, v in lst)

y = ['---', 'datapoint: "lastNotNull"', 'cellIdPreamble: ""', 'cells:']
for d in [FW] + SPINES + LEAVES:
    y.append(f'  {d}:')
    y.append(f'    dataRef: "{d}"')
    y.append('    strokeColor:')
    y.append('      thresholds:')
    y.append(th(UPTIME_TH, 8).rstrip("\n"))
    y.append('    fillColor:')
    y.append('      thresholds:')
    y.append(th(UPTIME_TH, 8).rstrip("\n"))
    y.append('    fillColorElementFilter:')
    y.append("      - {name: 'rect', position: '2'}")
    y.append(f'  up-{d}:')
    y.append(f'    dataRef: "d-{d}"')
    y.append('    label:')
    y.append('      separator: "replace"')
    y.append('      units: "none"')
    y.append('      unitsPostfix: "d up"')
    y.append('      valueMappings:')
    y.append('        - {valueMax: -0.5, text: "UNREACHABLE"}')
    y.append('      decimalPoints: 1')
    y.append('    labelColor:')
    y.append(f'      dataRef: "{d}"')
    y.append('      thresholds:')
    y.append(th(UPTIME_TH, 8).rstrip("\n"))
for l in LEAVES:
    for h in HOSTS[l]:
        y.append(f'  {h}:')
        y.append(f'    dataRef: "{h}"')
        y.append('    fillColor:')
        y.append('      thresholds:')
        y.append(th(UPTIME_TH, 8).rstrip("\n"))
for src, dst, _ in EDGES:
    cid = f"L-{src}-{dst}"
    y.append(f'  {cid}:')
    y.append(f'    dataRef: "{cid}"')
    y.append('    strokeColor:')
    y.append('      thresholds:')
    y.append(th(LINK_TH, 8).rstrip("\n"))
YAML = "\n".join(y) + "\n"

# ---- panels
def gp(x, y_, w, h):
    return {"x": x, "y": y_, "w": w, "h": h}

def prom(expr, legend="", ref="A", instant=False, fmt="time_series"):
    t = {"datasource": PROM, "refId": ref, "expr": expr, "legendFormat": legend or "__auto",
         "range": not instant, "instant": instant, "format": fmt}
    return t

E = '$exporter'
panels = []
pid = [0]
def nid():
    pid[0] += 1
    return pid[0]

def stat(title, expr, x, w, unit="none", steps=None, mappings=None, desc="", color_mode="background"):
    return {
        "id": nid(), "type": "stat", "title": title, "datasource": PROM,
        "gridPos": gp(x, 0, w, 4),
        "targets": [prom(expr, "", "A", instant=True)],
        "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "colorMode": color_mode, "graphMode": "none", "justifyMode": "center",
                    "textMode": "value", "orientation": "auto", "wideLayout": True},
        "fieldConfig": {"defaults": {"unit": unit, "decimals": 0, "noValue": "0",
                                      "mappings": mappings or [],
                                      "thresholds": {"mode": "absolute", "steps": steps or [{"color": "#1f60c4", "value": None}]},
                                      "color": {"mode": "thresholds"}},
                        "overrides": []},
    }

panels.append(stat("Devices", f'max(network_topology_graph_devices_total{{instance="{E}"}})', 0, 3,
                   desc="Devices in the discovered topology graph (network_topology_graph_devices_total)."))
panels.append(stat("Links", f'max(network_topology_graph_edges_total{{instance="{E}"}})', 3, 3,
                   desc="Adjacencies in the discovered topology graph (network_topology_graph_edges_total)."))
panels.append(stat("Graph", f'max(network_topology_graph_stale{{instance="{E}"}})', 6, 3,
                   steps=[{"color": "green", "value": None}, {"color": "red", "value": 1}],
                   mappings=[{"type": "value", "options": {"0": {"text": "FRESH", "color": "green", "index": 0},
                                                            "1": {"text": "STALE", "color": "red", "index": 1}}}],
                   desc="network_topology_graph_stale: 1 when the last discovery cycle failed and the graph is being served from the previous cycle."))
panels.append(stat("Changes 1h", f'sum(increase(network_topology_change_total{{instance="{E}"}}[1h])) or vector(0)', 9, 3,
                   steps=[{"color": "green", "value": None}, {"color": "orange", "value": 1}, {"color": "red", "value": 10}],
                   desc="Adjacency adds/removes/changes over the last hour: sum(increase(network_topology_change_total[1h]))."))
panels.append(stat("Disc errors",
                   f'(sum(increase(network_topology_snmp_walks_total{{instance="{E}",status!="ok"}}[1h])) or vector(0))'
                   f' + (count(network_topology_module_last_status{{instance="{E}"}} > 0) or vector(0))',
                   12, 3,
                   steps=[{"color": "green", "value": None}, {"color": "orange", "value": 1}, {"color": "red", "value": 5}],
                   desc="Failed SNMP walks in the last hour (network_topology_snmp_walks_total, status != ok) plus discovery modules currently degraded or failed (network_topology_module_last_status > 0)."))

def clock(title, tz, x, w, label):
    return {
        "id": nid(), "type": "grafana-clock-panel", "title": "", "transparent": False,
        "gridPos": gp(x, 0, w, 4),
        "options": {
            "mode": "time", "clockType": "custom", "timezone": tz, "refresh": "sec", "fontMono": True,
            "bgColor": "",
            "style": "text",
            "timeSettings": {"customFormat": "HH:mm", "fontSize": "40px", "fontWeight": "bold"},
            "dateSettings": {"showDate": True, "dateFormat": "ddd DD MMM", "locale": "en-gb",
                             "fontSize": "15px", "fontWeight": "normal"},
            "timezoneSettings": {"showTimezone": False, "zoneFormat": "abbv", "fontSize": "14px", "fontWeight": "normal"},
            "descriptionSettings": {"source": "input", "descriptionText": label, "queryField": "",
                                    "noValueText": "", "fontSize": "18px", "fontWeight": "bold"},
            "countdownSettings": {"source": "input", "endCountdownTime": "", "queryCalculation": "lastNotNull",
                                  "queryField": "", "endText": "", "noValueText": "", "invalidValueText": ""},
            "countupSettings": {"source": "input", "beginCountupTime": "", "queryCalculation": "lastNotNull",
                                "queryField": "", "beginText": "", "noValueText": "", "invalidValueText": ""},
        },
    }

panels.append(clock("UTC", "Etc/UTC", 15, 4, "UTC"))
panels.append(clock("London", "Europe/London", 19, 5, "LONDON"))

# flow panel
flow_targets = [
    prom(f'max by (device_id) (network_topology_device_uptime_seconds{{instance="{DC1}"}})'
         f' or (max by (device_id) (last_over_time(network_topology_device_uptime_seconds{{instance="{DC1}"}}[1h])) * 0 - 1)',
         "{{device_id}}", "A"),
    prom(f'max by (device_id) (network_topology_device_uptime_seconds{{instance="{DC1}",device_id!~"host-.*"}}) / 86400'
         f' or (max by (device_id) (last_over_time(network_topology_device_uptime_seconds{{instance="{DC1}",device_id!~"host-.*"}}[1h])) * 0 - 1)',
         "d-{{device_id}}", "B"),
    prom(f'max by (src_device, dst_device) (network_topology_edge_info{{instance="{DC1}"}})'
         f' or (max by (src_device, dst_device) (last_over_time(network_topology_edge_info{{instance="{DC1}"}}[1h])) * 0)',
         "L-{{src_device}}-{{dst_device}}", "C"),
]
panels.append({
    "id": nid(), "type": "andrewbmchugh-flow-panel",
    "title": "HQ-DC1 fabric - live adjacency and device uptime",
    "description": "Hand-drawn fabric for the HQ-DC1 discovery exporter (netobs-ent-dc1:9100; this diagram is fixed to that site). "
                   "Tiles and host chips are coloured by network_topology_device_uptime_seconds: red UNREACHABLE when a device polled in the last hour stops reporting, orange under 1h (recent reboot), yellow under 24h, green otherwise, grey when never seen. "
                   "Each line is one discovered adjacency from network_topology_edge_info: green while the series is present, red when it was seen in the last hour but has disappeared, grey when never seen.",
    "datasource": PROM,
    "gridPos": gp(0, 4, 15, 15),
    "targets": flow_targets,
    "options": {
        "svg": SVG, "panelConfig": YAML, "siteConfig": "",
        "panZoomEnabled": False, "animationsEnabled": False, "animationControlEnabled": False,
        "highlighterEnabled": False, "highlighterSelection": "", "timeSliderEnabled": False,
        "timeSliderMode": "local", "testDataEnabled": False,
    },
    "fieldConfig": {"defaults": {}, "overrides": []},
})

# polystat honeycomb
SHORT_UPTIME_DAYS = ('(max by (device_id) (network_topology_device_uptime_seconds{instance="$exporter"}) / 86400'
                     ' or (max by (device_id) (last_over_time(network_topology_device_uptime_seconds{instance="$exporter"}[1h])) * 0 - 1))')
for pat, rpl in [("(.*)", "$1"), ("host-leaf(\\\\d+)-(\\\\d+)", "h$1-$2"), ("leaf-(\\\\d+)", "lf$1"),
                 ("spine-(\\\\d+)", "sp$1"), ("edge-fw-(\\\\d+)", "fw$1"), ("wan-core-(\\\\d+)", "wc$1")]:
    SHORT_UPTIME_DAYS = f'label_replace({SHORT_UPTIME_DAYS}, "short", "{rpl}", "device_id", "{pat}")'
panels.append({
    "id": nid(), "type": "grafana-polystat-panel",
    "title": "Device uptime honeycomb",
    "description": "One cell per device reporting network_topology_device_uptime_seconds for the selected exporter, value in days, sorted lowest uptime first so recent reboots sit top-left. Red DOWN: unreachable (polled in the last hour, no longer reporting). Orange: rebooted in the last hour. Yellow: rebooted in the last 24h. Green: up 24h or more. Short names: fwNN edge-fw-NN, spNN spine-NN, lfNN leaf-NN, wcNN wan-core-NN, hNN-MM host-leafNN-MM; hover a cell for the value.",
    "datasource": PROM,
    "gridPos": gp(15, 4, 9, 15),
    "targets": [prom(SHORT_UPTIME_DAYS, "{{short}}", "A")],
    "options": {
        "autoSizeColumns": True, "autoSizeRows": True, "autoSizePolygons": True,
        "layoutNumColumns": 6, "layoutNumRows": 6, "layoutDisplayLimit": 200,
        "globalPolygonSize": 25, "globalPolygonBorderSize": 2,
        "globalAutoScaleFonts": False, "globalLabelFontSize": 13, "globalValueFontSize": 15,
        "globalCompositeValueFontSize": 12,
        "globalTextFontAutoColorEnabled": True, "globalTextFontColor": "#000000",
        "globalTextFontFamily": "Inter",
        "ellipseEnabled": False, "ellipseCharacters": 10,
        "sortByDirection": 3, "sortByField": "value",
        "globalTooltipsEnabled": True, "globalTooltipsShowTimestampEnabled": False,
        "globalTooltipsShowValueEnabled": True, "globalShowTooltipColumnHeadersEnabled": True,
        "tooltipDisplayMode": "all", "tooltipDisplayTextTriggeredEmpty": "OK",
        "tooltipPrimarySortDirection": 2, "tooltipPrimarySortByField": "thresholdLevel",
        "tooltipSecondarySortDirection": 2, "tooltipSecondarySortByField": "value",
        "globalDisplayMode": "all", "globalDisplayTextTriggeredEmpty": "OK",
        "globalShowValueEnabled": True, "globalShowTimestampEnabled": False,
        "globalShape": "hexagon_pointed_top", "globalGradientsEnabled": True,
        "globalFillColor": "rgba(10, 85, 161, 1)", "globalPolygonBorderColor": "rgba(0, 0, 0, 0)",
        "globalUnitFormat": "suffix:d", "globalOperator": "lastNotNull", "globalDecimals": 0,
        "globalThresholdsConfig": [
            {"color": "#E02F44", "state": 2, "value": -1},
            {"color": "#FF9830", "state": 1, "value": 0},
            {"color": "#FADE2A", "state": 1, "value": 0.0417},
            {"color": "#56A64B", "state": 0, "value": 1},
        ],
        "globalClickthrough": "", "globalClickthroughSanitizedEnabled": True,
        "globalClickthroughNewTabEnabled": True, "globalClickthroughCustomTargetEnabled": False,
        "globalClickthroughCustomTarget": "", "globalRegexPattern": "",
        "overrideConfig": {"overrides": []},
        "compositeGlobalAliasingEnabled": False,
        "compositeConfig": {"composites": [], "enabled": True, "animationSpeed": "1500"},
    },
    "fieldConfig": {"defaults": {"unit": "suffix:d", "decimals": 0, "mappings": [{"type": "range", "options": {"from": -2, "to": -0.5, "result": {"text": "DOWN", "color": "#E02F44", "index": 0}}}]}, "overrides": []},
})

# logs
panels.append({
    "id": nid(), "type": "logs", "title": "Topology changes and neighbour conflicts",
    "description": "Live discovery events from {source=\"network-topology-exporter\"}: event=topology_change (adjacency added/removed) and event=topology_conflict (LLDP and CDP disagree about a neighbour).",
    "datasource": LOKI,
    "gridPos": gp(0, 19, 15, 7),
    "targets": [{"datasource": LOKI, "refId": "A", "queryType": "range",
                 "expr": f'{{source="network-topology-exporter", instance="{E}"}} | logfmt '
                         '| line_format "{{.event}}  {{if .change_kind}}{{.change_kind}} {{.proto}}  {{.src_device}} {{.src_port}} -> {{.dst_device}} {{.dst_port}}{{else}}{{.conflict_type}}  {{.src_device}} {{.src_port}}  sources={{.sources}}{{end}}"'}],
    "options": {"showTime": True, "showLabels": False, "showCommonLabels": False, "wrapLogMessage": False,
                "prettifyLogMessage": False, "enableLogDetails": True, "dedupStrategy": "none",
                "sortOrder": "Descending", "enableInfiniteScrolling": False},
})

# discovery module status timeline
panels.append({
    "id": nid(), "type": "state-timeline", "title": "Discovery module health",
    "description": "network_topology_module_last_status per discovery module: 0 OK, 1 degraded, 2 hard-failed.",
    "datasource": PROM,
    "gridPos": gp(15, 19, 9, 7),
    "targets": [prom(f'max by (module) (network_topology_module_last_status{{instance="{E}"}})', "{{module}}", "A")],
    "options": {"showValue": "never", "mergeValues": True, "alignValue": "center", "rowHeight": 0.8,
                "legend": {"showLegend": False, "displayMode": "list", "placement": "bottom"},
                "tooltip": {"mode": "single", "sort": "none"}},
    "fieldConfig": {"defaults": {
        "color": {"mode": "thresholds"},
        "thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": None}, {"color": "orange", "value": 1}, {"color": "red", "value": 2}]},
        "mappings": [{"type": "value", "options": {"0": {"text": "OK", "color": "green", "index": 0},
                                                    "1": {"text": "Degraded", "color": "orange", "index": 1},
                                                    "2": {"text": "Hard-failed", "color": "red", "index": 2}}}],
        "custom": {"fillOpacity": 85, "lineWidth": 0}}, "overrides": []},
})

dash = {
    "apiVersion": "dashboard.grafana.app/v1beta1",
    "kind": "Dashboard",
    "metadata": {"name": "nos-noc-wallboard", "annotations": {"grafana.app/folder": "netobs-showcase"}},
    "spec": {
        "title": "Network Observability - NOC Wallboard",
        "description": "Big-screen NOC view of the global network's HQ-DC1 data centre: live fabric adjacency, device uptime, discovery health and topology events.",
        "uid": "nos-noc-wallboard",
        "tags": ["netobs-showcase", "noc", "wallboard"],
        "editable": True, "graphTooltip": 1, "refresh": "30s", "schemaVersion": 41,
        "style": "dark", "timezone": "utc",
        "time": {"from": "now-1h", "to": "now"},
        "timepicker": {"refresh_intervals": ["30s", "1m", "5m"]},
        "links": [{"type": "dashboards", "tags": ["netobs-showcase"], "asDropdown": True, "title": "Network Operations",
                   "includeVars": False, "keepTime": True, "targetBlank": False, "icon": "external link"}],
        "templating": {"list": [{
            "name": "exporter", "label": "Discovery exporter", "type": "query", "datasource": PROM,
            "query": {"qryType": 1, "query": "label_values(network_topology_graph_devices_total, instance)", "refId": "PrometheusVariableQueryEditor-VariableQuery"},
            "definition": "label_values(network_topology_graph_devices_total, instance)",
            "current": {"text": DC1, "value": DC1}, "refresh": 1, "sort": 1, "multi": False, "includeAll": False,
            "options": [],
        }]},
        "annotations": {"list": []},
        "panels": panels,
    },
}
with open(OUT, "w") as f:
    json.dump(dash, f, indent=2)
print("wrote", OUT, len(SVG), "svg bytes", len(YAML), "yaml bytes")
