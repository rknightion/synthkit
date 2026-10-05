#!/usr/bin/env python3
"""Generate nos-wan-maps.json (Global WAN and Federation Maps)."""
import os
import copy
import json

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "nos-wan-maps.json")
PROM = {"type": "prometheus", "uid": "grafanacloud-prom"}
JOB = 'job="integrations/network-topology-exporter"'
HUB = f'{JOB},instance="netobs-glb-hub:9100"'
SPOKE = f'{JOB},instance="netobs-spoke-emea:9100"'
ENT = f'{JOB},instance="netobs-ent-dc1:9100"'

# Sites: id, label, city, lat, lng
SITES = [
    ("hub", "Global hub", "London", 51.5074, -0.1278),
    ("dc-amer", "dc-amer", "Ashburn", 39.0438, -77.4874),
    ("dc-apac", "dc-apac", "Singapore", 1.3521, 103.8198),
    ("dc-emea", "dc-emea", "Frankfurt", 50.1109, 8.6821),
    ("dc-edge-1", "dc-edge-1", "Dublin", 53.3498, -6.2603),
    ("dc-edge-2", "dc-edge-2", "Amsterdam", 52.3676, 4.9041),
    ("emea-edge1", "emea-edge1", "Paris", 48.8566, 2.3522),
    ("hq-dc1", "hq-dc1", "Chicago", 41.8781, -87.6298),
]
SPOKES = ["dc-amer", "dc-apac", "dc-emea", "dc-edge-1", "dc-edge-2"]
SITE = {s[0]: s for s in SITES}

pid = [0]


def nid():
    pid[0] += 1
    return pid[0]


def tgt(expr, ref, legend="", instant=False, fmt="time_series"):
    t = {"datasource": PROM, "refId": ref, "expr": expr, "editorMode": "code",
         "legendFormat": legend, "format": fmt}
    if instant:
        t.update({"instant": True, "range": False})
    else:
        t.update({"instant": False, "range": True})
    return t


def panel(ptype, title, x, y, w, h, targets, desc="", options=None, fieldConfig=None,
          transformations=None, **kw):
    p = {"id": nid(), "type": ptype, "title": title, "description": desc,
         "gridPos": {"x": x, "y": y, "w": w, "h": h}, "datasource": PROM,
         "targets": targets, "options": options or {},
         "fieldConfig": fieldConfig or {"defaults": {}, "overrides": []}}
    if transformations:
        p["transformations"] = transformations
    p.update(kw)
    return p


def thr(*steps):
    return {"mode": "absolute", "steps": [{"color": c, "value": v} for v, c in steps]}


AGE_THR = thr((None, "green"), (120, "yellow"), (180, "orange"), (300, "red"))

# ---------------------------------------------------------------- networkmap

_AGE = f'(time() - network_topology_federation_spoke_last_push_timestamp_seconds{{{HUB}}})'
_UP = f'network_topology_federation_spoke_up{{{HUB}}}'
GATED_AGE = (f'(({_AGE} and on (spoke_id) ({_UP} == 1))'
             f' or (clamp_min({_AGE}, 300) and on (spoke_id) ({_UP} == 0)))')
RAW_AGE = f'time() - network_topology_federation_spoke_last_push_timestamp_seconds{{{HUB}}}'
SPOKE_AGE = f'time() - network_topology_federation_spoke_push_last_success_unix{{{SPOKE}}}'


def srcdst_spokes(expr):
    return (f'label_replace(label_replace({expr}, "src", "hub", "instance", ".*"),'
            f' "dst", "$1", "spoke_id", "(.+)")')


def srcdst_emea(expr):
    return (f'label_replace(label_replace({expr}, "src", "hub", "instance", ".*"),'
            f' "dst", "emea-edge1", "instance", ".*")')


def nm_targets():
    a = f'sum by (src, dst) ({srcdst_spokes(GATED_AGE)} or {srcdst_emea(SPOKE_AGE)})'
    b = f'sum by (src, dst) ({srcdst_spokes(RAW_AGE)} or {srcdst_emea(SPOKE_AGE)})'
    return [tgt(a, "A", instant=True, fmt="table"), tgt(b, "B", instant=True, fmt="table")]


def nm_node(sid, label_side="right", dy=4, text=None):
    s = SITE[sid]
    color = "#111827"
    name = text if text is not None else f"{s[1]} - {s[2]}"
    if label_side == "none":
        txt = ""
    elif label_side == "right":
        txt = f"<text x='11' y='{dy}' fill='{color}' font-size='12'>{name}</text>"
    else:
        txt = (f"<text x='-11' y='{dy}' fill='{color}' font-size='12' text-anchor='end'>"
               f"{name}</text>")
    r = 9 if sid == "hub" else 7
    return {"name": sid, "coordinate": [s[3], s[4]],
            "meta": {"display_name": f"{s[1]} ({s[2]})",
                     "svg": f"<circle r='{r}'/>{txt}"}}


def nm_edge(dst, mid=None):
    a, b = SITE["hub"], SITE[dst]
    coords = [[a[3], a[4]]]
    if mid:
        coords.append(mid)
    coords.append([b[3], b[4]])
    return {"name": f"hub--{dst}", "meta": {"endpoint_identifiers": {"names": ["hub", dst]}},
            "coordinates": coords, "children": []}


def networkmap(title, x, y, w, h, center, zoom, labels, mids, desc):
    nodes = [nm_node(sid, *labels.get(sid, ("right", 4))) for sid, *_ in SITES]
    edges = [nm_edge(d, mids.get(d)) for d in SPOKES + ["emea-edge1"]]
    mapjson = {"name": "Federation push paths", "pathLayout": {"type": "curveNatural"},
               "nodes": nodes, "edges": edges}
    empty = {"color": "#64748B", "edgeWidth": 2, "endpointId": "names", "legend": False,
             "mapjson": "{\"nodes\":[],\"edges\":[]}", "nodeWidth": 5, "pathOffset": 2,
             "visible": False}
    layer = {
        "color": "#8AB8FF", "dashboardEdgeDstVar": "destination",
        "dashboardEdgeSrcVar": "source", "dashboardNodeVar": "node",
        "srcField": "src", "dstField": "dst",
        "inboundValueField": "Value #A", "outboundValueField": "Value #B",
        "edgeWidth": 4, "endpointId": "names", "legend": True,
        "mapjson": json.dumps(mapjson), "name": "Hub to spoke push paths",
        "nodeThresholds": {"mode": "absolute", "steps": [{"color": "#8AB8FF", "value": None}]},
        "nodeWidth": 8, "pathOffset": 3, "visible": True,
    }
    l2, l3 = copy.deepcopy(empty), copy.deepcopy(empty)
    l2["name"], l3["name"] = "Layer 2", "Layer 3"
    options = {
        "background": "#D6D6D6",
        "customEdgeTooltip": (
            "<div><strong>${forward.from} - ${forward.to}</strong><br/>"
            "Hub to spoke half: seconds since last push, held at 300 s or more while the hub marks the spoke down: "
            "${forward.dataPoint}<br/>Spoke to hub half: seconds since last push: "
            "${reverse.dataPoint}</div>"),
        "customNodeTooltip": "<div><strong>${meta.display_name}</strong><br/>Site ID: ${name}</div>",
        "enableCustomEdgeTooltip": True, "enableCustomNodeTooltip": True,
        "enableEdgeAnimation": True, "enableEditing": False, "enableNodeAnimation": True,
        "enableScrolling": False, "initialViewStrategy": "static", "layerLimit": 1,
        "layers": [layer, l2, l3],
        "legendColumnLength": 4, "legendDefaultBehavior": "visible",
        "legendPosition": "bottomleft", "showLegend": True, "showSidebar": False,
        "showViewControls": True,
        "tileset": {"boundaries": None, "geographic": "arcgis", "labels": None},
        "topologySource": "json",
        "viewport": {"center": {"lat": center[0], "lng": center[1]}, "zoom": zoom},
        "latitudeVar": "", "longitudeVar": "",
    }
    fc = {"defaults": {"color": {"mode": "thresholds"}, "decimals": 0, "unit": "s",
                       "thresholds": AGE_THR}, "overrides": []}
    return panel("esnet-networkmap-panel", title, x, y, w, h, nm_targets(), desc, options, fc,
                 transformations=[{"id": "merge", "options": {}}], pluginVersion="3.1.0")


NM_DESC_SITES = ("Site locations: global hub London, dc-amer Ashburn, dc-apac Singapore, dc-emea "
                 "Frankfurt, dc-edge-1 Dublin, dc-edge-2 Amsterdam, EMEA edge spoke exporter "
                 "(emea-edge1) Paris, enterprise DC hq-dc1 Chicago (standalone discovery, not "
                 "federated, so it has no push path). ")
NM_DESC_DATA = (
    "Each path is a federation push path from a spoke to the hub, drawn as a curve between "
    "sites, not a measured route or circuit. Colour is seconds since the hub last accepted a "
    "push (green under 120 s, yellow 120 s, orange 180 s, red 300 s and above). The hub to spoke "
    "half is held at 300 s or more (red) whenever network_topology_federation_spoke_up is 0, so "
    "a spoke the hub marks down always shows red; the spoke to hub half is the raw push age from "
    "network_topology_federation_spoke_last_push_timestamp_seconds. The emea-edge1 path uses "
    "the spoke-side network_topology_federation_spoke_push_last_success_unix for both halves. "
    "No bandwidth or utilisation is shown: the topology exporters do not measure link traffic.")

# ---------------------------------------------------------------- weathermap

ANCH = {"0": 0, "1": 0, "2": 0, "3": 0, "4": 0}


def wm_node(nid_, x, y, label, icon, status=None, tooltip=None):
    n = {"id": nid_, "position": [x, y], "label": label, "showLabel": True,
         "anchors": {k: {"numLinks": 0, "numFilledLinks": 0} for k in ANCH},
         "useConstantSpacing": False, "compactVerticalLinks": False,
         "padding": {"vertical": 7, "horizontal": 12},
         "colors": {"font": "#1F2937", "background": "#FFFFFF", "border": "#5794F2",
                    "statusDown": "#F2495C"},
         "nodeIcon": {"src": f"public/plugins/tamirsuliman-weathermap-panel/icons/{icon}.svg",
                      "name": icon, "size": {"width": 40, "height": 40},
                      "padding": {"vertical": 2, "horizontal": 2}, "drawInside": False},
         "useIconBoundaryForLinks": True, "isConnection": False,
         "nodeStatusColorTarget": "border"}
    if status:
        n["statusQuery"] = status[0]
        n["statusValueMappings"] = [{"value": v, "color": c} for v, c in status[1]]
    if tooltip:
        n["tooltipMetrics"] = tooltip
    return n


UPMAP = [(0, "#F2495C"), (1, "#73BF69")]
STALEMAP = [(0, "#73BF69"), (1, "#F2495C")]


def wm_link(lid, a, z, query, bandwidth, units, port_a, port_z, direction, status=None,
            waypoints=None, tooltip=None):
    l = {"id": lid, "nodes": [a, z],
         "sides": {"A": {"bandwidth": bandwidth, "query": query, "labelOffset": 50, "anchor": 0,
                         "dashboardLink": "", "portLabel": "", "directionLabel": ""},
                   "Z": {"bandwidth": 0, "labelOffset": 50, "anchor": 0, "dashboardLink": "",
                         "portLabel": "", "directionLabel": ""}},
         "units": units, "arrows": {"width": 8, "height": 10, "offset": 2}, "stroke": 6,
         "showThroughputPercentage": False, "statusDownColor": "#F2495C", "statusBlink": True,
         "singleDirection": True, "animation": "inherit"}
    if status:
        l["statusQuery"] = status
    if waypoints:
        l["waypoints"] = [{"x": px, "y": py} for px, py in waypoints]
    if tooltip:
        l["tooltipMetrics"] = tooltip
    return l


def weathermap(x, y, w, h):
    W, H = 1100, 640
    hub = wm_node("hub", 550, 320, "Global hub - netobs-glb-hub (London)", "networking/router",
                  status=("graph stale hub", STALEMAP),
                  tooltip=[{"label": "Rejected updates (15m)", "query": "rejected all 15m",
                            "units": "short"},
                           {"label": "Spokes up", "query": "spokes up total", "units": "short"}])
    pos = {"dc-amer": (230, 320), "dc-apac": (880, 320), "dc-emea": (550, 70),
           "dc-edge-1": (230, 110), "dc-edge-2": (870, 110)}
    city = {s[0]: s[2] for s in SITES}
    nodes = [hub]
    links = []
    for sp in SPOKES:
        px, py = pos[sp]
        nodes.append(wm_node(sp, px, py, f"{sp} ({city[sp]})", "networking/building",
                             status=(f"up {sp}", UPMAP)))
        links.append(wm_link(f"push-{sp}", sp, "hub", f"push age {sp}", 300, "s", sp, "hub",
                             "push age", status=f"up {sp}"))
    nodes.append(wm_node("emea-edge1", 550, 575, "emea-edge1 spoke exporter (Paris)",
                         "networking/building",
                         tooltip=[{"label": "Push queue depth", "query": "queue emea-edge1",
                                   "units": "short"},
                                  {"label": "Push failures (15m)", "query": "failures emea-edge1",
                                   "units": "short"}]))
    links.append(wm_link("push-emea-edge1", "emea-edge1", "hub", "push age emea-edge1", 300, "s",
                         "emea-edge1", "hub", "push age"))
    nodes.append(wm_node("validator", 880, 560, "Hub graph-update validator", "networking/firewall",
                         tooltip=[{"label": "stale_generation (15m)",
                                   "query": "rejected stale_generation 15m", "units": "short"}]))
    links.append(wm_link("hub-validator", "hub", "validator", "rejected all 15m", 20, "short",
                         "hub", "validator", "rejected 15m",
                         waypoints=[(700, 470), (880, 470)]))
    nodes.append(wm_node("hq-dc1", 230, 560, "Enterprise DC hq-dc1 (Chicago) - standalone",
                         "networking/server", status=("graph stale hq-dc1", STALEMAP),
                         tooltip=[{"label": "Devices in graph", "query": "devices hq-dc1",
                                   "units": "short"}]))
    # anchor counts
    for l in links:
        for n in nodes:
            if n["id"] in l["nodes"]:
                n["anchors"]["0"]["numLinks"] += 1
    # links carry full node objects
    byid = {n["id"]: n for n in nodes}
    for l in links:
        l["nodes"] = [copy.deepcopy(byid[l["nodes"][0]]), copy.deepcopy(byid[l["nodes"][1]])]
    wm = {
        "version": 14, "id": "nos-wan-weathermap", "nodes": nodes, "links": links,
        "scale": [{"percent": 0, "color": "#73BF69"}, {"percent": 40, "color": "#FADE2A"},
                  {"percent": 60, "color": "#FF9830"}, {"percent": 100, "color": "#F2495C"}],
        "settings": {
            "link": {"spacing": {"horizontal": 12, "vertical": 8},
                     "stroke": {"color": "rgba(36, 41, 46, 0.25)"},
                     "label": {"background": "#FFFFFF", "border": "rgba(36, 41, 46, 0.35)",
                               "font": "#1F2937"},
                     "showAllWithPercentage": False, "defaultUnits": "s", "linkDecimals": 0,
                     "valueMappingMode": "last", "timeline": {"enabled": False},
                     "dynamicStroke": {"enabled": True, "minWidth": 4, "maxWidth": 12},
                     "flowAnimation": {"enabled": True, "speed": 1}, "gradientColor": False,
                     "hoverHighlight": True, "labelCollision": True, "labelHideZoom": 3},
            "animation": {"enabled": True, "respectReducedMotion": True, "maxAnimatedLinks": 100,
                          "pauseInEditMode": True, "showLegend": False},
            "fontSizing": {"node": 12, "link": 11},
            "colorScaleMode": "percent",
            "panel": {"backgroundColor": "#EEF1F5", "showTimestamp": True,
                      "panelSize": {"width": W, "height": H}, "zoomScale": 0,
                      "offset": {"x": 0, "y": 0}, "viewZoomPan": True,
                      "grid": {"enabled": False, "size": 20, "guidesEnabled": False}},
            "tooltip": {"fontSize": 11, "textColor": "#1F2937", "backgroundColor": "#FFFFFF",
                        "inboundColor": "#73BF69", "outboundColor": "#5794F2",
                        "scaleToBandwidth": False},
            "scale": {"position": {"x": 1, "y": 22}, "size": {"width": 60, "height": 200},
                      "title": "% of budget", "fontSizing": {"title": 12, "threshold": 10},
                      "scaleUnit": "%", "fontColor": "#1F2937",
                      "backgroundColor": "rgba(255, 255, 255, 0.9)"},
            "statusLegend": {"enabled": True, "position": {"x": 82, "y": 4},
                             "items": [{"id": "status-up", "color": "#73BF69",
                                        "label": "spoke up / graph fresh"},
                                       {"id": "status-down", "color": "#F2495C",
                                        "label": "spoke down / graph stale"}]},
        },
    }
    targets = [
        tgt(f'time() - network_topology_federation_spoke_last_push_timestamp_seconds{{{HUB}}}',
            "A", "push age {{spoke_id}}"),
        tgt(f'network_topology_federation_spoke_up{{{HUB}}}', "B", "up {{spoke_id}}"),
        tgt(SPOKE_AGE, "C", "push age emea-edge1"),
        tgt(f'sum(increase(network_topology_graph_updates_rejected_total{{{HUB}}}[15m]))', "D",
            "rejected all 15m"),
        tgt(f'sum(increase(network_topology_graph_updates_rejected_total{{{HUB},reason="stale_generation"}}[15m]))',
            "E", "rejected stale_generation 15m"),
        tgt(f'network_topology_federation_spoke_push_queue_depth{{{SPOKE}}}', "F",
            "queue emea-edge1"),
        tgt(f'sum(increase(network_topology_federation_spoke_push_failures_total{{{SPOKE}}}[15m]))',
            "G", "failures emea-edge1"),
        tgt(f'network_topology_graph_stale{{{HUB}}}', "H", "graph stale hub"),
        tgt(f'network_topology_graph_stale{{{ENT}}}', "I", "graph stale hq-dc1"),
        tgt(f'network_topology_graph_devices_total{{{ENT}}}', "J", "devices hq-dc1"),
        tgt(f'sum(network_topology_federation_spoke_up{{{HUB}}})', "K", "spokes up total"),
    ]
    desc = ("Logical hub-and-spoke view of the federated topology estate. Spoke links (A side, "
            "spoke to hub) show seconds since the hub last accepted that spoke's push "
            "(network_topology_federation_spoke_last_push_timestamp_seconds); colour and width "
            "scale against a 300 s staleness budget set on this map (40% yellow, 60% orange, "
            "100% red). A spoke link blinks red when network_topology_federation_spoke_up is 0. "
            "The emea-edge1 link uses the spoke-side push_last_success_unix. The validator link "
            "shows graph updates rejected by the hub in the last 15 minutes "
            "(network_topology_graph_updates_rejected_total, all reasons) against a budget of 20. "
            "Node borders: spoke liveness, and network_topology_graph_stale for the hub and the "
            "standalone enterprise DC. No bandwidth is shown; the exporters do not measure "
            "link traffic.")
    return panel("tamirsuliman-weathermap-panel", "Federation weathermap - push age, liveness and "
                 "rejected updates", x, y, w, h, targets, desc, {"weathermap": wm},
                 pluginVersion="1.6.12")


# ---------------------------------------------------------------- build

def stat(title, x, y, w, h, expr, desc, unit="short", thresholds=None, mappings=None,
         color_mode="background", text=None):
    fc = {"defaults": {"unit": unit, "color": {"mode": "thresholds"},
                       "thresholds": thresholds or thr((None, "blue")),
                       "mappings": mappings or []}, "overrides": []}
    opts = {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
            "colorMode": color_mode, "graphMode": "area", "justifyMode": "center",
            "textMode": "value_and_name" if text else "value", "orientation": "auto",
            "wideLayout": True, "showPercentChange": False}
    return panel("stat", title, x, y, w, h, [tgt(expr, "A", text or "")], desc, opts, fc)


def build():
    panels = []
    y = 0
    panels.append(stat("Spokes up", 0, y, 4, 4,
                       f'sum(network_topology_federation_spoke_up{{{HUB}}})',
                       "Spokes the hub currently marks live (network_topology_federation_spoke_up) out of 5.",
                       thresholds=thr((None, "red"), (4, "orange"), (5, "green"))))
    panels.append(stat("Oldest spoke push", 4, y, 4, 4,
                       f'max(time() - network_topology_federation_spoke_last_push_timestamp_seconds{{{HUB}}})',
                       "Largest seconds-since-last-push across all spokes, seen from the hub.",
                       unit="s", thresholds=AGE_THR))
    panels.append(stat("EMEA edge spoke push age", 8, y, 4, 4, SPOKE_AGE,
                       "Seconds since the emea-edge1 spoke exporter last pushed successfully "
                       "(spoke-side network_topology_federation_spoke_push_last_success_unix).",
                       unit="s", thresholds=AGE_THR))
    panels.append(stat("Rejected updates (1h)", 12, y, 4, 4,
                       f'sum(increase(network_topology_graph_updates_rejected_total{{{HUB}}}[1h]))',
                       "Graph updates the hub rejected in the last hour, all reasons.",
                       thresholds=thr((None, "green"), (5, "yellow"), (20, "red"))))
    panels.append(stat("Boundary observations", 16, y, 4, 4,
                       f'count(network_topology_boundary_observation_info{{{HUB}}})',
                       "Inter-domain boundary adjacencies visible to the hub.",
                       thresholds=thr((None, "purple"))))
    panels.append(stat("Hub graph devices", 20, y, 4, 4,
                       f'network_topology_graph_devices_total{{{HUB}}}',
                       "Devices in the federated hub graph (network_topology_graph_devices_total).",
                       thresholds=thr((None, "blue"))))
    y += 4
    panels.append(networkmap("Global WAN - hub to spoke push paths", 0, y, 15, 17, (30, 8), 2.5,
                             labels={"dc-edge-1": ("none", 0), "emea-edge1": ("none", 0),
                                     "dc-edge-2": ("none", 0), "dc-emea": ("none", 0),
                                     "hub": ("left", 24, "London hub + 4 EMEA sites"),
                                     "hq-dc1": ("left", -8), "dc-amer": ("left", 14)},
                             mids={"dc-amer": [50, -40], "dc-apac": [38, 60],
                                   "dc-emea": [52, 4]},
                             desc=NM_DESC_SITES + NM_DESC_DATA))
    panels.append(networkmap("Europe detail - hub, EMEA and edge spokes", 15, y, 9, 17, (51.3, 1.5),
                             5.0,
                             labels={"dc-edge-1": ("left", 4), "hub": ("right", 16),
                                     "emea-edge1": ("left", 14), "dc-edge-2": ("right", -6),
                                     "dc-emea": ("right", 4)},
                             mids={},
                             desc="Zoomed view of the European sites. " + NM_DESC_SITES + NM_DESC_DATA))
    y += 17
    panels.append(weathermap(0, y, 15, 19))
    # push age bar gauge
    bg_fc = {"defaults": {"unit": "s", "min": 0, "max": 300, "color": {"mode": "thresholds"},
                          "thresholds": AGE_THR, "decimals": 0}, "overrides": []}
    bg = panel("bargauge", "Push age per spoke", 15, y, 9, 10,
               [tgt(f'time() - network_topology_federation_spoke_last_push_timestamp_seconds{{{HUB}}}',
                    "A", "{{spoke_id}}", instant=True),
                tgt(SPOKE_AGE, "B", "emea-edge1 (spoke side)", instant=True)],
               "Seconds since each spoke last pushed to the hub "
               "(network_topology_federation_spoke_last_push_timestamp_seconds), plus the "
               "emea-edge1 exporter's own last-success age. Scale 0 to 300 s.",
               {"orientation": "horizontal", "displayMode": "lcd", "showUnfilled": True,
                "valueMode": "color", "namePlacement": "left", "sizing": "auto",
                "minVizHeight": 16, "maxVizHeight": 40,
                "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}},
               bg_fc)
    panels.append(bg)
    rej_fc = {"defaults": {"unit": "short", "color": {"mode": "palette-classic"},
                           "custom": {"drawStyle": "bars", "fillOpacity": 70, "lineWidth": 1,
                                      "stacking": {"mode": "normal", "group": "A"},
                                      "barAlignment": 0, "showPoints": "never"}},
              "overrides": []}
    panels.append(panel("timeseries", "Rejected graph updates by reason", 15, y + 10, 9, 9,
                        [tgt(f'sum by (reason) (increase(network_topology_graph_updates_rejected_total{{{HUB}}}[$__rate_interval]))',
                             "A", "{{reason}}")],
                        "Graph updates the hub rejected, by reason "
                        "(network_topology_graph_updates_rejected_total, increase per interval). "
                        "stale_generation means a spoke pushed a graph older than the one the hub "
                        "already holds.",
                        {"legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
                         "tooltip": {"mode": "multi", "sort": "desc"}}, rej_fc, interval="2m"))
    y += 19
    st_fc = {"defaults": {"color": {"mode": "thresholds"}, "thresholds": thr((None, "red"), (1, "green")),
                          "mappings": [{"type": "value", "options": {
                              "0": {"text": "DOWN", "color": "red", "index": 0},
                              "1": {"text": "UP", "color": "green", "index": 1}}}],
                          "custom": {"fillOpacity": 80, "lineWidth": 0}}, "overrides": []}
    panels.append(panel("state-timeline", "Spoke liveness", 0, y, 12, 8,
                        [tgt(f'network_topology_federation_spoke_up{{{HUB}}}', "A", "{{spoke_id}}")],
                        "Liveness of each spoke as tracked by the hub (network_topology_federation_spoke_up: 1 up, 0 down).",
                        {"showValue": "never", "rowHeight": 0.8, "mergeValues": True,
                         "alignValue": "center",
                         "legend": {"displayMode": "list", "placement": "bottom", "showLegend": False},
                         "tooltip": {"mode": "single"}}, st_fc))
    ts_fc = {"defaults": {"unit": "s", "color": {"mode": "palette-classic"},
                          "thresholds": AGE_THR,
                          "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 8,
                                     "showPoints": "never", "gradientMode": "opacity",
                                     "thresholdsStyle": {"mode": "dashed"}}}, "overrides": []}
    panels.append(panel("timeseries", "Push age over time", 12, y, 12, 8,
                        [tgt(f'time() - network_topology_federation_spoke_last_push_timestamp_seconds{{{HUB}}}',
                             "A", "{{spoke_id}}"),
                         tgt(SPOKE_AGE, "B", "emea-edge1 (spoke side)")],
                        "Seconds since last push per spoke; dashed lines mark the 120 s, 180 s and 300 s bands used on the maps.",
                        {"legend": {"displayMode": "table", "placement": "right", "showLegend": True,
                                    "calcs": ["lastNotNull", "max"]},
                         "tooltip": {"mode": "multi", "sort": "desc"}}, ts_fc))
    y += 8
    tbl_fc = {"defaults": {"custom": {"align": "auto", "cellOptions": {"type": "auto"},
                                      "filterable": True}}, "overrides": [
        {"matcher": {"id": "byName", "options": "proto"},
         "properties": [{"id": "custom.cellOptions", "value": {"type": "color-text"}},
                        {"id": "color", "value": {"mode": "fixed", "fixedColor": "purple"}}]}]}
    panels.append(panel("table", "Boundary observations at the hub", 0, y, 14, 14,
                        [tgt(f'network_topology_boundary_observation_info{{{HUB}}}', "A",
                             instant=True, fmt="table")],
                        "One row per inter-domain boundary adjacency the hub can see "
                        "(network_topology_boundary_observation_info): the two peers, the device "
                        "that reported it, its port and the discovery protocol.",
                        {"showHeader": True, "cellHeight": "sm",
                         "sortBy": [{"displayName": "Reporting device", "desc": False}],
                         "footer": {"show": False}},
                        tbl_fc,
                        transformations=[{"id": "organize", "options": {
                            "excludeByName": {"Time": True, "Value": True, "__name__": True,
                                              "job": True, "instance": True},
                            "indexByName": {"peer_a": 0, "peer_b": 1, "reporting_device": 2,
                                            "src_port": 3, "proto": 4},
                            "renameByName": {"peer_a": "External peer", "peer_b": "Internal peer",
                                             "reporting_device": "Reporting device",
                                             "src_port": "Port", "proto": "Protocol"}}}]))
    pod_fc = {"defaults": {"unit": "short", "min": 0, "decimals": 0,
                           "color": {"mode": "continuous-BlPu"}}, "overrides": []}
    panels.append(panel("bargauge", "Boundary observations by leaf", 14, y, 4, 14,
                        [tgt('count by (leaf) (label_replace(network_topology_boundary_observation_info{'
                             + HUB + '}, "leaf", "leaf-$1", "reporting_device", "host-leaf([0-9]+)-.*"))',
                             "A", "{{leaf}}", instant=True)],
                        "Boundary adjacencies grouped by the leaf the reporting host hangs off "
                        "(leaf parsed from reporting_device, for example host-leaf01-02 is on leaf-01).",
                        {"orientation": "horizontal", "displayMode": "gradient", "showUnfilled": True,
                         "valueMode": "color", "namePlacement": "left", "sizing": "manual",
                         "minVizHeight": 22, "maxVizHeight": 40,
                         "text": {"titleSize": 14, "valueSize": 16},
                         "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}},
                        pod_fc))
    md = (
        "### Reading this board\n\n"
        "**Global hub** `netobs-glb-hub` (London) federates five regional spokes: "
        "dc-amer (Ashburn), dc-apac (Singapore), dc-emea (Frankfurt), dc-edge-1 (Dublin) and "
        "dc-edge-2 (Amsterdam). The **emea-edge1** spoke exporter (Paris) reports its own push "
        "health. The **enterprise DC** hq-dc1 (Chicago) runs standalone discovery and is not "
        "federated.\n\n"
        "- Map paths are logical push paths drawn between sites, not circuits.\n"
        "- Link colour = seconds since last push; red means stale (300 s or more) or marked down by the hub.\n"
        "- Rejected updates are hub-wide; the exporter does not attribute them to a spoke.\n"
        "- No link bandwidth is shown anywhere on this board: topology discovery does not "
        "measure traffic.")
    panels.append(panel("text", "About these maps", 18, y, 6, 14, [], "",
                        {"mode": "markdown", "content": md}, datasource=None))
    y += 14
    # probe row (expanded)
    panels.append({"id": nid(), "type": "row", "title": "External probe locations",
                   "collapsed": False, "gridPos": {"x": 0, "y": y, "w": 24, "h": 1},
                   "panels": []})
    y += 1
    INFO = 'max by (instance, job, probe, region, geohash) (sm_check_info)'

    def short(expr):
        # present the probe agent name without its deployment prefix
        return f'label_replace({expr}, "probe", "$1", "probe", "[^-]+-(private.*)")'

    succ_fc = {"defaults": {"unit": "percentunit", "min": 0, "max": 1, "decimals": 1,
                            "color": {"mode": "thresholds"},
                            "thresholds": thr((None, "red"), (0.9, "orange"), (0.99, "green"))},
               "overrides": [
                   {"matcher": {"id": "byName", "options": "Mean latency"},
                    "properties": [{"id": "unit", "value": "s"}, {"id": "decimals", "value": 3},
                                   {"id": "min"}, {"id": "max"},
                                   {"id": "thresholds", "value": thr((None, "green"), (0.5, "yellow"), (1, "red"))}]},
                   {"matcher": {"id": "byName", "options": "Checks"},
                    "properties": [{"id": "unit", "value": "short"}, {"id": "decimals", "value": 0},
                                   {"id": "min"}, {"id": "max"},
                                   {"id": "color", "value": {"mode": "fixed", "fixedColor": "blue"}}]}]}
    geo_targets = [
        tgt(f'avg by (geohash) (probe_success * on (instance, job, probe) group_left (region, geohash) {INFO})',
            "A", instant=True, fmt="table"),
        tgt(f'avg by (geohash) (probe_duration_seconds * on (instance, job, probe) group_left (region, geohash) {INFO})',
            "B", instant=True, fmt="table"),
        tgt(f'count by (geohash) ({INFO})', "C", instant=True, fmt="table"),
    ]
    geo = panel("geomap", "Probe agent locations - success and latency", 0, y, 10, 13, geo_targets,
                "One marker per probe location, placed from the sm_check_info geohash label. Colour is "
                "mean probe_success across the checks run from that location (green 99% and above, "
                "orange 90%, red below), size scales with the number of checks, and the tooltip adds "
                "mean probe_duration_seconds. All probe agents currently report the same location "
                "geohash, so they share one marker; the table alongside breaks them out per agent.",
                {"view": {"id": "coords", "lat": 30, "lon": 10, "zoom": 1.6, "allLayers": True},
                 "controls": {"showZoom": True, "mouseWheelZoom": False, "showAttribution": True},
                 "basemap": {"type": "default", "name": "Basemap", "config": {}},
                 "tooltip": {"mode": "details"},
                 "layers": [{"type": "markers", "name": "Probe locations",
                             "location": {"mode": "geohash", "geohash": "geohash"},
                             "config": {"showLegend": True,
                                        "style": {"size": {"field": "Checks", "fixed": 12, "min": 12, "max": 26},
                                                  "color": {"field": "Success"},
                                                  "opacity": 0.85,
                                                  "symbol": {"mode": "fixed", "fixed": "img/icons/marker/circle.svg"},
                                                  "text": {"field": "Success", "mode": "field"},
                                                  "textConfig": {"fontSize": 13, "offsetX": 0,
                                                                 "offsetY": -24,
                                                                 "textAlign": "center",
                                                                 "textBaseline": "middle"}}},
                             "tooltip": True}]},
                succ_fc,
                transformations=[
                    {"id": "joinByField", "options": {"byField": "geohash", "mode": "outer"}},
                    {"id": "organize", "options": {
                        "excludeByName": {"Time": True, "Time 1": True, "Time 2": True, "Time 3": True},
                        "renameByName": {"Value #A": "Success", "Value #B": "Mean latency",
                                         "Value #C": "Checks"}}}])
    panels.append(geo)
    tbl2_fc = copy.deepcopy(succ_fc)
    tbl2_fc["defaults"]["custom"] = {"align": "auto", "cellOptions": {"type": "auto"}}
    tbl2_fc["overrides"].append({"matcher": {"id": "byName", "options": "Success"},
                                 "properties": [{"id": "custom.cellOptions",
                                                 "value": {"type": "color-background", "mode": "gradient"}}]})
    tbl2_fc["overrides"].append({"matcher": {"id": "byName", "options": "Mean latency"},
                                 "properties": [{"id": "custom.cellOptions",
                                                 "value": {"type": "gauge", "mode": "lcd", "valueDisplayMode": "text"}},
                                                {"id": "max", "value": 0.5}]})
    panels.append(panel("table", "Probe agents - success and latency", 10, y, 14, 6, [
        tgt(short(f'avg by (probe, region) (probe_success * on (instance, job, probe) group_left (region) {INFO})'),
            "A", instant=True, fmt="table"),
        tgt(short(f'avg by (probe, region) (probe_duration_seconds * on (instance, job, probe) group_left (region) {INFO})'),
            "B", instant=True, fmt="table"),
        tgt(short(f'count by (probe, region) ({INFO})'), "C", instant=True, fmt="table")],
        "Per probe agent: region, number of checks (sm_check_info), mean probe_success and mean "
        "probe_duration_seconds across its checks.",
        {"showHeader": True, "cellHeight": "sm", "footer": {"show": False},
         "sortBy": [{"displayName": "Region", "desc": False}]},
        tbl2_fc,
        transformations=[
            {"id": "joinByField", "options": {"byField": "probe", "mode": "outer"}},
            {"id": "organize", "options": {
                "excludeByName": {"Time": True, "Time 1": True, "Time 2": True, "Time 3": True,
                                  "region 2": True, "region 3": True},
                "indexByName": {"probe": 0, "region 1": 1, "Value #C": 2, "Value #A": 3, "Value #B": 4},
                "renameByName": {"probe": "Probe agent", "region 1": "Region", "Value #A": "Success",
                                 "Value #B": "Mean latency", "Value #C": "Checks"}}}]))
    lat_fc = {"defaults": {"unit": "s", "min": 0, "decimals": 3, "color": {"mode": "thresholds"},
                           "thresholds": thr((None, "green"), (0.5, "yellow"), (1, "red"))},
              "overrides": []}
    panels.append(panel("timeseries", "Probe latency by check", 10, y + 6, 14, 7, [
        tgt(short('avg by (job, probe) (probe_duration_seconds)'), "A", "{{job}} from {{probe}}")],
        "probe_duration_seconds per check (job) and probe agent.",
        {"legend": {"displayMode": "table", "placement": "right", "showLegend": True,
                    "calcs": ["lastNotNull", "mean"]},
         "tooltip": {"mode": "multi", "sort": "desc"}},
        {"defaults": {"unit": "s", "decimals": 1, "color": {"mode": "palette-classic"},
                      "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 6,
                                 "showPoints": "never", "gradientMode": "opacity"}},
         "overrides": []}))
    y += 13

    dash = {
        "apiVersion": "dashboard.grafana.app/v1beta1", "kind": "Dashboard",
        "metadata": {"name": "nos-wan-maps",
                     "annotations": {"grafana.app/folder": "netobs-showcase"}},
        "spec": {
            "uid": "nos-wan-maps",
            "title": "Network Observability - Global WAN and Federation Maps",
            "description": "Geographic and logical maps of the global network's topology "
                           "federation: hub, regional spokes and the enterprise DC, coloured by "
                           "spoke liveness, push age and rejected graph updates.",
            "tags": ["netobs-showcase"], "timezone": "browser", "schemaVersion": 41,
            "editable": True, "graphTooltip": 1, "refresh": "1m",
            "time": {"from": "now-1h", "to": "now"},
            "templating": {"list": []}, "annotations": {"list": []},
            "links": [{"type": "dashboards", "title": "Network Observability",
                       "tags": ["netobs-showcase"], "asDropdown": True, "includeVars": False,
                       "keepTime": True, "icon": "external link", "targetBlank": False}],
            "panels": panels,
        },
    }
    with open(OUT, "w") as f:
        json.dump(dash, f, indent=2)
        f.write("\n")


build()
