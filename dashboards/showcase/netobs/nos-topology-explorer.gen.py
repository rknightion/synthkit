#!/usr/bin/env python3
"""Generate nos-topology-explorer.json (lane topo)."""
import os
import json

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "nos-topology-explorer.json")
PROM = {"type": "prometheus", "uid": "grafanacloud-prom"}
TIER_RE = "^(edge-fw|wan-core|ext-peer|spine|leaf|host).*"

DEV = 'network_topology_device_info{instance="$exporter",vendor=~"$vendor"}'


def relabel(expr, dst, src):
    return f'label_replace({expr}, "{dst}", "$1", "{src}", "(.*)")'


# Edge selector honouring exporter, protocol and vendor (both endpoints) filters.
E = (
    '(network_topology_edge_info{instance="$exporter",discovery_proto=~"$proto"}'
    f' and on(instance, src_device) {relabel(DEV, "src_device", "device_id")}'
    f' and on(instance, dst_device) {relabel(DEV, "dst_device", "device_id")})'
)


def tiers(expr):
    return (f'label_replace(label_replace({expr}, "src_tier", "$1", "src_device", "{TIER_RE}"),'
            f' "dst_tier", "$1", "dst_device", "{TIER_RE}")')


DEV_TIER = f'label_replace({DEV}, "tier", "$1", "device_id", "{TIER_RE}")'


def vend(side):
    base = 'network_topology_device_info{instance="$exporter"}'
    return f'label_replace({relabel(base, side + "_device", "device_id")}, "{side}_vendor", "$1", "vendor", "(.*)")'


VENDOR_PAIR = (
    f'count by (pair) (label_join(({E} * on(instance, src_device) group_left(src_vendor) {vend("src")})'
    f' * on(instance, dst_device) group_left(dst_vendor) {vend("dst")}, "pair", " -> ", "src_vendor", "dst_vendor"))'
)

DEGREE = (
    'count by (instance, device_id) ('
    f'label_replace(label_replace({E}, "device_id", "$1", "src_device", "(.*)"), "end", "src", "", "")'
    f' or label_replace(label_replace({E}, "device_id", "$1", "dst_device", "(.*)"), "end", "dst", "", ""))'
)
INV_LABELS = "instance, device_id, tier, vendor, os_version, site"


def q(expr, ref, fmt="table", instant=True, legend=None):
    t = {"datasource": PROM, "refId": ref, "expr": expr, "format": fmt, "instant": instant,
         "range": not instant, "editorMode": "code"}
    if legend:
        t["legendFormat"] = legend
    return t


panels = []
pid = [0]


def add(p, x, y, w, h):
    pid[0] += 1
    p["id"] = pid[0]
    p["gridPos"] = {"x": x, "y": y, "w": w, "h": h}
    p.setdefault("datasource", PROM)
    panels.append(p)
    return p


# ---------------------------------------------------------------- header
add({
    "type": "text", "title": "",
    "options": {"mode": "markdown", "content": (
        "### Topology Explorer\n"
        "The discovered L2/L3 graph of the selected discovery exporter, as reconciled from LLDP, CDP, "
        "FDB, BGP, OSPF, IS-IS and MPLS-TE walks. Filter by **vendor** (both link ends must match) and "
        "**discovery protocol**, and switch the graph between a **layered** tier view and a "
        "**force** layout. Tier is derived from the device name prefix "
        "(edge-fw, wan-core, spine, leaf, host).\n\n"
        "Source: `network_topology_device_info`, `network_topology_edge_info`, "
        "`network_topology_device_uptime_seconds`.")},
    "transparent": True,
}, 0, 0, 9, 6)


def stat(title, expr, color, x, unit="none", decimals=0, desc=""):
    add({
        "type": "stat", "title": title, "description": desc,
        "targets": [q(expr, "A", fmt="time_series")],
        "fieldConfig": {"defaults": {"unit": unit, "decimals": decimals,
                                     "color": {"mode": "fixed", "fixedColor": color}},
                        "overrides": []},
        "options": {"colorMode": "background_solid", "graphMode": "none", "justifyMode": "center",
                    "textMode": "value", "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "orientation": "auto", "wideLayout": True, "showPercentChange": False},
    }, x, 0, 3, 6)


stat("Devices", f"count({DEV})", "#1F60C4", 9, desc="Discovered devices matching the vendor filter.")
stat("Links", f"count({E})", "#8F3BB8", 12, desc="Reconciled edges matching the vendor and protocol filters.")
stat("Links per device", f"2 * count({E}) / count({DEV})", "#37872D", 15, decimals=2,
     desc="Mean degree: each edge counts at both ends.")
stat("Discovery protocols", f"count(count by (discovery_proto) ({E}))", "#C4162A", 18)
stat("Vendors", f"count(count by (vendor) ({DEV}))", "#E0752D", 21)

# ---------------------------------------------------------------- ECharts graph
GRAPH_JS = r"""
const series = context.panel.data.series || [];
const theme = context.grafana.theme;
const textColor = theme.colors.text.primary;
const mutedColor = theme.colors.text.secondary;
const layoutMode = context.grafana.replaceVariables('$layout') === 'force' ? 'force' : 'layered';

function rows(refId) {
  const f = series.find((s) => s.refId === refId);
  if (!f || !f.fields.length) { return []; }
  const n = f.fields[0].values.length;
  const out = [];
  for (let i = 0; i < n; i++) {
    const r = {};
    f.fields.forEach((fl) => { r[fl.name] = fl.values.get ? fl.values.get(i) : fl.values[i]; });
    out.push(r);
  }
  return out;
}

const TIER_ORDER = ['edge-fw', 'wan-core', 'ext-peer', 'spine', 'leaf', 'host', 'other'];
const TIER_NAME = { 'edge-fw': 'Edge firewall', 'wan-core': 'WAN core', 'ext-peer': 'External peer',
  spine: 'Spine', leaf: 'Leaf', host: 'Host', other: 'Other' };
const TIER_SYMBOL = { 'edge-fw': 'triangle', 'wan-core': 'diamond', 'ext-peer': 'pin', spine: 'roundRect',
  leaf: 'rect', host: 'circle', other: 'circle' };
const LAYER = { 'edge-fw': 0, 'wan-core': 0, 'ext-peer': 0, spine: 1, leaf: 2, host: 3, other: 3 };
const VENDOR_COLOR = { arista: '#5794F2', cisco: '#73BF69', juniper: '#FF9830', nokia: '#B877D9' };
const PROTO_STYLE = {
  lldp: { color: '#8AB8FF', type: 'solid', width: 1.2, curveness: 0 },
  cdp: { color: '#96D98D', type: 'solid', width: 1.2, curveness: 0 },
  fdb: { color: '#8E8E8E', type: 'dotted', width: 1, curveness: 0 },
  bgp: { color: '#FF9830', type: 'dashed', width: 2.5, curveness: 0.25 },
  ospf: { color: '#FADE2A', type: 'dashed', width: 2, curveness: 0.15 },
  isis: { color: '#F2CC0C', type: 'dashed', width: 2, curveness: 0.15 },
  mpls_te: { color: '#F2495C', type: 'solid', width: 3.5, curveness: 0.12 },
  configured: { color: '#CCCCDC', type: 'dashed', width: 1.5, curveness: 0 },
};
const tierOf = (id) => { const m = /^(edge-fw|wan-core|ext-peer|spine|leaf|host)/.exec(id); return m ? m[1] : 'other'; };
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const natural = (a, b) => a.localeCompare(b, undefined, { numeric: true });

const nodeMap = new Map();
rows('A').forEach((r) => {
  if (!r.device_id || nodeMap.has(r.device_id)) { return; }
  nodeMap.set(r.device_id, { id: r.device_id, vendor: r.vendor || 'unknown', os: r.os_version || '',
    site: r.site || '', tier: tierOf(r.device_id), degree: 0, nbrs: [] });
});
const links = [];
const protosSeen = new Set();
rows('B').forEach((r) => {
  const s = nodeMap.get(r.src_device);
  const t = nodeMap.get(r.dst_device);
  if (!s || !t) { return; }
  s.degree++; t.degree++; s.nbrs.push(t.id); t.nbrs.push(s.id);
  const st = PROTO_STYLE[r.discovery_proto] || PROTO_STYLE.configured;
  protosSeen.add(r.discovery_proto);
  links.push({ source: s.id, target: t.id, proto: r.discovery_proto, kind: r.link_kind,
    direction: r.direction, srcPort: r.src_port, dstPort: r.dst_port,
    lineStyle: { color: st.color, type: st.type, width: st.width, curveness: st.curveness, opacity: 0.55 } });
});

if (nodeMap.size === 0) {
  return { title: { text: 'No devices match the current filters', left: 'center', top: 'middle',
    textStyle: { color: mutedColor, fontSize: 14 } } };
}

// Layered placement: one horizontal band per tier group; hosts sit under their upstream switch.
const W = 1600; const H = 800;
const nodes = Array.from(nodeMap.values());
const layersPresent = Array.from(new Set(nodes.map((n) => LAYER[n.tier]))).sort();
const yOf = (layer) => {
  const i = layersPresent.indexOf(layer);
  return layersPresent.length === 1 ? H / 2 : (i / (layersPresent.length - 1)) * H;
};
const pos = new Map();
layersPresent.filter((l) => l < 3).forEach((l) => {
  const members = nodes.filter((n) => LAYER[n.tier] === l)
    .sort((a, b) => TIER_ORDER.indexOf(a.tier) - TIER_ORDER.indexOf(b.tier) || natural(a.id, b.id));
  members.forEach((n, i) => pos.set(n.id, { x: ((i + 0.5) / members.length) * W, y: yOf(l) }));
});
const bottom = nodes.filter((n) => LAYER[n.tier] === 3).map((n) => {
  const xs = n.nbrs.map((id) => pos.get(id)).filter(Boolean).map((p) => p.x);
  return { n, x0: xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : W / 2 };
}).sort((a, b) => a.x0 - b.x0 || natural(a.n.id, b.n.id));
bottom.forEach((b, i) => pos.set(b.n.id, { x: ((i + 0.5) / bottom.length) * W, y: yOf(3) }));

const tiersPresent = TIER_ORDER.filter((t) => nodes.some((n) => n.tier === t));
const categories = tiersPresent.map((t) => ({ name: TIER_NAME[t], symbol: TIER_SYMBOL[t],
  itemStyle: { color: '#9FA7B3' } }));
const big = nodes.length > 60;
const data = nodes.map((n) => {
  const p = pos.get(n.id);
  const showLabel = n.tier !== 'host' || !big;
  return {
    id: n.id, name: n.id, x: p.x, y: p.y, value: n.degree,
    category: tiersPresent.indexOf(n.tier),
    symbol: TIER_SYMBOL[n.tier],
    symbolSize: Math.round(7 + Math.sqrt(n.degree) * (n.tier === 'host' ? 2 : 4.2)),
    itemStyle: { color: VENDOR_COLOR[n.vendor] || '#9FA7B3', borderColor: theme.colors.background.primary, borderWidth: 1 },
    label: { show: showLabel, fontSize: n.tier === 'spine' || LAYER[n.tier] === 0 ? 12 : 10,
      fontWeight: n.tier === 'spine' || LAYER[n.tier] === 0 ? 'bold' : 'normal',
      position: LAYER[n.tier] === 2 ? 'bottom' : 'top', color: textColor, distance: 4 },
    meta: n,
  };
});

// Static key for vendor colours and protocol line styles (the legend toggles tiers).
const key = [];
let kx = 0;
Object.keys(VENDOR_COLOR).filter((v) => nodes.some((n) => n.vendor === v)).forEach((v) => {
  key.push({ type: 'circle', shape: { cx: kx + 6, cy: 6, r: 6 }, style: { fill: VENDOR_COLOR[v] } });
  key.push({ type: 'text', x: kx + 16, y: 0, style: { text: v, fill: textColor, fontSize: 12 } });
  kx += 16 + v.length * 7 + 18;
});
kx += 14;
Object.keys(PROTO_STYLE).filter((p) => protosSeen.has(p)).forEach((p) => {
  const st = PROTO_STYLE[p];
  key.push({ type: 'line', shape: { x1: kx, y1: 6, x2: kx + 26, y2: 6 },
    style: { stroke: st.color, lineWidth: Math.max(st.width, 1.5), lineDash: st.type === 'solid' ? null : (st.type === 'dotted' ? [2, 3] : [6, 4]) } });
  key.push({ type: 'text', x: kx + 32, y: 0, style: { text: p, fill: textColor, fontSize: 12 } });
  kx += 32 + p.length * 7 + 18;
});

return {
  backgroundColor: 'transparent',
  tooltip: {
    trigger: 'item', confine: true,
    formatter: (pr) => {
      if (pr.dataType === 'edge') {
        const d = pr.data;
        const port = (x) => (x && x !== 'null' ? ' : ' + esc(x) : '');
        return '<b>' + esc(d.source) + port(d.srcPort) + '</b> &rarr; <b>' + esc(d.target) + port(d.dstPort) + '</b><br/>'
          + 'protocol: ' + esc(d.proto) + '<br/>link kind: ' + esc(d.kind) + '<br/>direction: ' + esc(d.direction);
      }
      const m = pr.data.meta;
      return '<b>' + esc(m.id) + '</b><br/>tier: ' + esc(TIER_NAME[m.tier]) + '<br/>vendor: ' + esc(m.vendor)
        + '<br/>OS: ' + esc(m.os) + '<br/>site: ' + esc(m.site) + '<br/>links: ' + esc(m.degree);
    },
  },
  legend: [{ data: categories.map((c) => c.name), top: 4, left: 8, itemWidth: 14, itemHeight: 14,
    textStyle: { color: textColor, fontSize: 12 } }],
  graphic: [{ type: 'group', right: 12, top: 6, children: key }],
  animation: false,
  series: [{
    type: 'graph',
    layout: layoutMode === 'force' ? 'force' : 'none',
    force: { repulsion: big ? 700 : 420, edgeLength: big ? [40, 120] : [70, 160], gravity: 0.05, friction: 0.2, layoutAnimation: false },
    nodeScaleRatio: 0,
    zoom: layoutMode === 'force' && big ? 0.5 : 1,
    top: 90, bottom: 30, left: 40, right: 40,
    roam: true, draggable: true,
    categories,
    data,
    links,
    edgeSymbol: ['none', 'none'],
    labelLayout: { hideOverlap: true },
    emphasis: { focus: 'adjacency', label: { show: true }, lineStyle: { width: 4, opacity: 1 } },
    blur: { itemStyle: { opacity: 0.15 }, lineStyle: { opacity: 0.05 } },
  }],
};
"""

add({
    "type": "volkovlabs-echarts-panel",
    "title": "Discovered topology - $exporter",
    "description": "All discovered devices and reconciled links. Node colour = vendor, shape = tier, size = link "
                   "count (degree). Line style = discovery protocol. Hover a node to isolate its neighbours; "
                   "scroll to zoom, drag to pan. Driven by network_topology_device_info (nodes) and "
                   "network_topology_edge_info (links).",
    "targets": [
        q(DEV, "A"),
        q(E, "B"),
    ],
    "options": {"renderer": "canvas", "map": "none", "themeEditor": {"name": "default", "config": "{}"},
                "editorMode": "code", "editor": {"format": "auto"}, "getOption": GRAPH_JS.strip()},
}, 0, 6, 24, 22)

# ---------------------------------------------------------------- Sankey + treemap
add({
    "type": "netsage-sankey-panel",
    "title": "Link flow: source tier -> discovery protocol -> destination tier",
    "description": "Link counts from network_topology_edge_info grouped by the tier of each end (from the device "
                   "name prefix) and the protocol that discovered the link. Ribbon width = number of links.",
    "targets": [q(f"count by (src_tier, discovery_proto, dst_tier) ({tiers(E)})", "A")],
    "transformations": [{"id": "organize", "options": {
        "excludeByName": {"Time": True},
        "indexByName": {"src_tier": 0, "discovery_proto": 1, "dst_tier": 2, "Value": 3},
        "renameByName": {"src_tier": "Source tier", "discovery_proto": "Protocol", "dst_tier": "Destination tier",
                         "Value": "Links"}}}],
    "fieldConfig": {"defaults": {"unit": "none", "color": {"mode": "thresholds"}}, "overrides": []},
    "options": {"monochrome": False, "nodeColor": "#9FA7B3", "nodeWidth": 22, "nodePadding": 26,
                "labelSize": 13, "iteration": 15, "valueField": "Links"},
}, 0, 28, 14, 13)

add({
    "type": "marcusolsson-treemap-panel",
    "title": "Estate composition: site / vendor / OS version (all exporters)",
    "description": "Every discovered device across all three exporters, nested by site, then vendor, then OS "
                   "version. Tile area = device count. Driven by count(network_topology_device_info).",
    "targets": [q('count by (path) (label_join(network_topology_device_info{vendor=~"$vendor"}, "path", "/", '
                  '"site", "vendor", "os_version"))', "A")],
    "transformations": [{"id": "organize", "options": {"excludeByName": {"Time": True},
                                                        "renameByName": {"Value": "Devices"}}}],
    "fieldConfig": {"defaults": {"unit": "none", "color": {"mode": "continuous-BlPu"},
                                 "custom": {"separator": "/"}}, "overrides": []},
    "options": {"tiling": "treemapSquarify"},
}, 14, 28, 10, 13)

# ---------------------------------------------------------------- matrices
PROTO_CODES = [("lldp", 1), ("cdp", 2), ("fdb", 3), ("ospf", 4), ("isis", 5), ("bgp", 6), ("mpls_te", 7),
               ("configured", 8)]
PROTO_COLORS = {"lldp": "#8AB8FF", "cdp": "#96D98D", "fdb": "#8E8E8E", "ospf": "#FADE2A", "isis": "#F2CC0C",
                "bgp": "#FF9830", "mpls_te": "#F2495C", "configured": "#CCCCDC"}
fab_sel = E.replace('discovery_proto=~"$proto"}', 'discovery_proto=~"$proto",src_device!~"host-.*"}', 1)
def _proto_sel(p):
    return fab_sel.replace("discovery_proto=~", 'discovery_proto="' + p + '",discovery_proto=~', 1)


code_expr = " or ".join("(" + _proto_sel(p) + " * " + str(c) + ")" for p, c in PROTO_CODES)
FABRIC = f"max by (src_device, dst_device, src_tier, dst_tier) ({tiers('(' + code_expr + ')')})"

add({
    "type": "esnet-matrix-panel",
    "title": "Fabric adjacency matrix - cell colour = discovery protocol (blue lldp, orange bgp, red mpls_te)",
    "description": "Rows = link source device, columns = link destination device, grouped by tier. Cell colour "
                   "= the protocol that discovered the link. Driven by network_topology_edge_info with "
                   "src_device not a host.",
    "targets": [q(FABRIC, "A")],
    "transformations": [{"id": "organize", "options": {"excludeByName": {"Time": True}}}],
    "fieldConfig": {"defaults": {
        "unit": "none", "color": {"mode": "thresholds"},
        "thresholds": {"mode": "absolute", "steps": [{"color": "#9FA7B3", "value": None}]},
        "mappings": [{"type": "value", "options": {
            str(c): {"text": p, "color": PROTO_COLORS[p], "index": i} for i, (p, c) in enumerate(PROTO_CODES)}}],
    }, "overrides": []},
    "options": {"sourceField": "src_device", "targetField": "dst_device", "valueField": "Value",
                "enableRowGrouping": True, "rowCategoryField": "src_tier", "rowCategoryHeaderWidth": 110,
                "rowCategoryGap": 6,
                "enableColGrouping": False, "colCategoryField": "dst_tier", "colCategoryHeaderHeight": 30,
                "colCategoryGap": 6,
                "showLegend": False, "legendType": "categorical", "sortType": "natural-asc",
                "sourceText": "From", "targetText": "To", "valueText": "Protocol",
                "cellSize": 30, "cellPadding": 10, "txtLength": 14, "txtSize": 11,
                "nullColor": "#2A2D35", "defaultColor": "#22252B", "inputList": False, "addUrl": False},
}, 0, 41, 16, 11)

add({
    "type": "esnet-matrix-panel",
    "title": "Tier-to-tier link counts",
    "description": "Number of links from each source tier (rows) to each destination tier (columns). Driven by "
                   "count by (src_tier, dst_tier) over network_topology_edge_info.",
    "targets": [q(f"count by (src_tier, dst_tier) ({tiers(E)})", "A")],
    "transformations": [{"id": "organize", "options": {"excludeByName": {"Time": True}}}],
    "fieldConfig": {"defaults": {"unit": "none", "color": {"mode": "continuous-GrYlRd"}}, "overrides": []},
    "options": {"sourceField": "src_tier", "targetField": "dst_tier", "valueField": "Value",
                "showLegend": True, "legendType": "range", "sortType": "natural-asc",
                "sourceText": "From tier", "targetText": "To tier", "valueText": "Links",
                "cellSize": 50, "cellPadding": 6, "txtLength": 12, "txtSize": 13,
                "nullColor": "#2A2D35", "defaultColor": "#22252B", "inputList": False, "addUrl": False,
                "enableRowGrouping": False, "enableColGrouping": False},
}, 16, 41, 8, 11)

# ---------------------------------------------------------------- pies + bar
PROTO_OVR = [{"matcher": {"id": "byName", "options": p},
              "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": PROTO_COLORS[p]}}]}
             for p, _ in PROTO_CODES]
add({
    "type": "piechart", "title": "Links by discovery protocol",
    "description": "count by (discovery_proto) of network_topology_edge_info.",
    "targets": [q(f"count by (discovery_proto) ({E})", "A", fmt="time_series", legend="{{discovery_proto}}")],
    "fieldConfig": {"defaults": {"unit": "none", "color": {"mode": "palette-classic"}}, "overrides": PROTO_OVR},
    "options": {"pieType": "donut", "displayLabels": ["percent"],
                "legend": {"displayMode": "table", "placement": "right", "showLegend": True, "values": ["value", "percent"]},
                "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                "tooltip": {"mode": "single", "sort": "none"}},
}, 0, 52, 7, 9)

add({
    "type": "piechart", "title": "Links by link kind",
    "description": "count by (link_kind) of network_topology_edge_info.",
    "targets": [q(f"count by (link_kind) ({E})", "A", fmt="time_series", legend="{{link_kind}}")],
    "fieldConfig": {"defaults": {"unit": "none", "color": {"mode": "palette-classic"}}, "overrides": [
        {"matcher": {"id": "byName", "options": "ethernet"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#5794F2"}}]},
        {"matcher": {"id": "byName", "options": "ip"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#FF9830"}}]},
        {"matcher": {"id": "byName", "options": "mpls-te"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#F2495C"}}]},
    ]},
    "options": {"pieType": "pie", "displayLabels": ["name"],
                "legend": {"displayMode": "table", "placement": "right", "showLegend": True, "values": ["value", "percent"]},
                "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                "tooltip": {"mode": "single", "sort": "none"}},
}, 7, 52, 7, 9)

add({
    "type": "barchart", "title": "Links by vendor pair (source -> destination)",
    "description": "network_topology_edge_info joined to network_topology_device_info on each end to get the "
                   "source and destination vendor.",
    "targets": [q(VENDOR_PAIR, "A")],
    "transformations": [
        {"id": "organize", "options": {"excludeByName": {"Time": True}, "renameByName": {"Value": "Links", "pair": "Vendor pair"}}},
        {"id": "sortBy", "options": {"sort": [{"field": "Links", "desc": True}]}},
    ],
    "fieldConfig": {"defaults": {"unit": "none", "color": {"mode": "continuous-BlPu"},
                                 "custom": {"fillOpacity": 85, "lineWidth": 0, "gradientMode": "scheme"}},
                    "overrides": []},
    "options": {"orientation": "horizontal", "xField": "Vendor pair", "showValue": "always", "barWidth": 0.8,
                "groupWidth": 0.7, "legend": {"showLegend": False, "displayMode": "list", "placement": "bottom"},
                "tooltip": {"mode": "single", "sort": "none"}, "xTickLabelRotation": 0, "stacking": "none",
                "colorByField": "Links"},
}, 14, 52, 10, 9)

# ---------------------------------------------------------------- inventory table
UPTIME = (f"max by ({INV_LABELS}) (network_topology_device_uptime_seconds{{instance=\"$exporter\"}}"
          f" * on(instance, device_id) group_left(vendor, os_version, site, tier) {DEV_TIER})")
DEG = f"max by ({INV_LABELS}) ({DEGREE} * on(instance, device_id) group_left(vendor, os_version, site, tier) {DEV_TIER})"
add({
    "type": "table", "title": "Device inventory - $exporter",
    "description": "One row per discovered device: identity from network_topology_device_info, uptime from "
                   "network_topology_device_uptime_seconds (SNMP sysUpTime) and link count from "
                   "network_topology_edge_info.",
    "targets": [q(UPTIME, "A"), q(DEG, "B")],
    "transformations": [
        {"id": "merge", "options": {}},
        {"id": "organize", "options": {
            "excludeByName": {"Time": True, "instance": True},
            "indexByName": {"device_id": 0, "tier": 1, "vendor": 2, "os_version": 3, "site": 4, "Value #B": 5, "Value #A": 6},
            "renameByName": {"device_id": "Device", "tier": "Tier", "vendor": "Vendor", "os_version": "OS version",
                             "site": "Site", "Value #A": "Uptime", "Value #B": "Links"}}},
        {"id": "sortBy", "options": {"sort": [{"field": "Links", "desc": True}]}},
    ],
    "fieldConfig": {"defaults": {"custom": {"align": "auto", "cellOptions": {"type": "auto"}, "filterable": True}},
                    "overrides": [
        {"matcher": {"id": "byName", "options": "Uptime"}, "properties": [
            {"id": "unit", "value": "s"}, {"id": "decimals", "value": 1},
            {"id": "min", "value": 0},
            {"id": "custom.cellOptions", "value": {"type": "gauge", "mode": "basic", "valueDisplayMode": "text"}},
            {"id": "color", "value": {"mode": "thresholds"}},
            {"id": "thresholds", "value": {"mode": "absolute", "steps": [
                {"color": "red", "value": None}, {"color": "orange", "value": 86400},
                {"color": "green", "value": 604800}]}},
            {"id": "custom.width", "value": 520}]},
        {"matcher": {"id": "byName", "options": "Links"}, "properties": [
            {"id": "custom.cellOptions", "value": {"type": "color-text"}},
            {"id": "color", "value": {"mode": "continuous-BlPu"}}, {"id": "custom.width", "value": 90}]},
        {"matcher": {"id": "byName", "options": "Vendor"}, "properties": [
            {"id": "custom.cellOptions", "value": {"type": "color-text"}},
            {"id": "mappings", "value": [{"type": "value", "options": {
                "arista": {"color": "#5794F2", "index": 0}, "cisco": {"color": "#73BF69", "index": 1},
                "juniper": {"color": "#FF9830", "index": 2}, "nokia": {"color": "#B877D9", "index": 3}}}]}]},
    ]},
    "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": True, "reducer": ["count"], "fields": ["Device"]},
                "sortBy": [{"displayName": "Links", "desc": True}]},
}, 0, 61, 24, 14)

# ---------------------------------------------------------------- dashboard
def qvar(name, label, query, multi, include_all, current):
    v = {"type": "query", "name": name, "label": label, "datasource": PROM,
         "query": {"qryType": 1, "query": query, "refId": "PrometheusVariableQueryEditor-VariableQuery"},
         "definition": query, "refresh": 2, "sort": 1, "multi": multi, "includeAll": include_all,
         "current": current, "options": [], "hide": 0}
    if include_all:
        v["allValue"] = ".*"
    return v


dash = {
    "apiVersion": "dashboard.grafana.app/v1beta1",
    "kind": "Dashboard",
    "metadata": {"name": "nos-topology-explorer", "annotations": {"grafana.app/folder": "netobs-showcase"}},
    "spec": {
        "title": "Network Observability - Topology Explorer",
        "description": "Interactive analysis of the discovered network graph: topology, link flow, adjacency, "
                       "estate composition and device inventory.",
        "uid": "nos-topology-explorer",
        "tags": ["netobs-showcase", "topology"],
        "editable": True, "graphTooltip": 1, "schemaVersion": 41, "refresh": "1m",
        "time": {"from": "now-1h", "to": "now"}, "timezone": "browser",
        "links": [{"type": "dashboards", "tags": ["netobs-showcase"], "asDropdown": True, "title": "Network Observability",
                   "includeVars": False, "keepTime": True, "targetBlank": False, "icon": "external link", "tooltip": "", "url": ""}],
        "templating": {"list": [
            qvar("exporter", "Exporter", "label_values(network_topology_device_info, instance)", False, False,
                 {"text": "netobs-glb-hub:9100", "value": "netobs-glb-hub:9100"}),
            qvar("vendor", "Vendor", 'label_values(network_topology_device_info{instance="$exporter"}, vendor)', True, True,
                 {"text": ["All"], "value": ["$__all"]}),
            qvar("proto", "Discovery protocol", 'label_values(network_topology_edge_info{instance="$exporter"}, discovery_proto)',
                 True, True, {"text": ["All"], "value": ["$__all"]}),
            {"type": "custom", "name": "layout", "label": "Graph layout", "query": "layered,force",
             "current": {"text": "layered", "value": "layered"}, "multi": False, "includeAll": False, "hide": 0,
             "options": [{"text": "layered", "value": "layered", "selected": True},
                         {"text": "force", "value": "force", "selected": False}]},
        ]},
        "annotations": {"list": [{"builtIn": 1, "datasource": {"type": "grafana", "uid": "-- Grafana --"},
                                  "enable": True, "hide": True, "iconColor": "rgba(0, 211, 255, 1)",
                                  "name": "Annotations & Alerts", "type": "dashboard"}]},
        "panels": panels,
    },
}

with open(OUT, "w") as f:
    json.dump(dash, f, indent=2)
    f.write("\n")
print("wrote", OUT, len(panels), "panels")
