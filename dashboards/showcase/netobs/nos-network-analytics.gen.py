import os
import json

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "nos-network-analytics.json")
PROM = {"type": "prometheus", "uid": "grafanacloud-prom"}
LOKI = {"type": "loki", "uid": "grafanacloud-logs"}
EXP = 'instance=~"$exporter"'

HELPERS = r"""
const series = context.panel.data.series || [];
const theme = context.grafana.theme;
const TXT = theme.colors.text.primary;
const MUTED = theme.colors.text.secondary;
const GRID = theme.isDark ? 'rgba(204,204,220,0.12)' : 'rgba(36,41,46,0.12)';
const PANEL_BG = theme.colors.background.primary;
function val(v) { return v; }
function frames(refId) { return series.filter((s) => s.refId === refId); }
function rows(refId) {
  const out = [];
  frames(refId).forEach((f) => {
    if (!f.fields.length) { return; }
    const n = f.fields[0].values.length;
    for (let i = 0; i < n; i++) {
      const r = {};
      f.fields.forEach((fl) => {
        const v = fl.values.get ? fl.values.get(i) : fl.values[i];
        r[fl.name] = v;
        if (fl.type === 'number' && fl.name !== 'Time') { r.__value = v; }
        if (fl.labels) { Object.assign(r, fl.labels); }
      });
      out.push(r);
    }
  });
  return out;
}
function scalar(refId) {
  const r = rows(refId);
  if (!r.length || r[0].__value === undefined || r[0].__value === null || isNaN(r[0].__value)) { return null; }
  return Number(r[0].__value);
}
function byInstance(refId) {
  const m = {};
  rows(refId).forEach((r) => { if (r.instance) { m[r.instance] = Number(r.__value); } });
  return m;
}
const TIER_ORDER = ['ext-peer', 'edge-fw', 'wan-core', 'spine', 'leaf', 'host', 'other'];
const TIER_NAME = { 'ext-peer': 'External peer', 'edge-fw': 'Edge firewall', 'wan-core': 'WAN core',
  spine: 'Spine', leaf: 'Leaf', host: 'Host', other: 'Other' };
const TIER_COLOR = { 'ext-peer': '#F2495C', 'edge-fw': '#FF9830', 'wan-core': '#FADE2A', spine: '#B877D9',
  leaf: '#5794F2', host: '#73BF69', other: '#8E8E8E' };
const VENDOR_COLOR = { arista: '#5794F2', cisco: '#73BF69', juniper: '#FF9830', nokia: '#B877D9' };
const PROTO_COLOR = { lldp: '#5794F2', cdp: '#73BF69', fdb: '#8E8E8E', bgp: '#FF9830', ospf: '#FADE2A',
  isis: '#B877D9', mpls_te: '#F2495C' };
function tierOf(id) {
  const s = String(id || '');
  for (const t of ['ext-peer', 'edge-fw', 'wan-core', 'spine', 'leaf', 'host']) { if (s.startsWith(t)) { return t; } }
  return 'other';
}
function shortExp(i) { return String(i || '').replace(/^netobs-/, '').replace(/:\d+$/, ''); }
const EXP_NAME = { 'ent-dc1': 'Enterprise DC (hq-dc1)', 'glb-hub': 'Global hub', 'spoke-emea': 'EMEA spoke' };
function expName(i) { const s = shortExp(i); return EXP_NAME[s] || s; }
// Degree = distinct neighbours per device, from edge_info rows (instance-scoped).
function degreeMap(edgeRows) {
  const nb = {}; const protos = {};
  edgeRows.forEach((e) => {
    const a = e.instance + '|' + e.src_device; const b = e.instance + '|' + e.dst_device;
    (nb[a] = nb[a] || new Set()).add(e.dst_device);
    (nb[b] = nb[b] || new Set()).add(e.src_device);
    (protos[a] = protos[a] || new Set()).add(e.discovery_proto);
    (protos[b] = protos[b] || new Set()).add(e.discovery_proto);
  });
  return { nb, protos };
}
function noData(msg) {
  return { title: { text: msg, left: 'center', top: 'middle', textStyle: { color: MUTED, fontSize: 14, fontWeight: 'normal' } } };
}
"""


def js(body):
    return HELPERS + body


def prom(ref, expr, instant=True, fmt="table", interval=None, legend=None):
    t = {"datasource": PROM, "refId": ref, "expr": expr, "editorMode": "code",
         "instant": instant, "range": not instant, "format": fmt}
    if interval:
        t["interval"] = interval
    if legend:
        t["legendFormat"] = legend
    return t


def echarts(pid, title, desc, grid, targets, body):
    return {
        "id": pid, "type": "volkovlabs-echarts-panel", "title": title, "description": desc,
        "gridPos": grid, "datasource": {"type": "datasource", "uid": "-- Mixed --"} if any(
            t["datasource"]["type"] == "loki" for t in targets) and any(
            t["datasource"]["type"] == "prometheus" for t in targets) else targets[0]["datasource"],
        "targets": targets,
        "options": {"renderer": "canvas", "map": "none", "themeEditor": {"name": "default", "config": "{}"},
                    "editorMode": "code", "editor": {"format": "auto"}, "getOption": js(body)},
    }


panels = []

# ---------------------------------------------------------------- header text
panels.append({
    "id": 1, "type": "text", "title": "", "gridPos": {"x": 0, "y": 0, "w": 24, "h": 4},
    "options": {"mode": "markdown", "content":
        "### Network analytics\n"
        "Multi-dimensional views of the discovered estate across the enterprise DC, the global Clos core and the "
        "regional spokes. Fabric KPIs and the exporter health radar come from the SNMP topology discovery exporters "
        "(`network_topology_*`); composition, degree and tier-to-tier link flow are computed from "
        "`network_topology_device_info` and `network_topology_edge_info`; event flow is read from the exporter's "
        "topology change log stream in Loki; the host matrix uses `node_network_*` and `windows_net_*` interface counters. "
        "Use the Exporter selector to scope the device-level charts."}})

# ---------------------------------------------------------------- 2 gauge cluster
gauge_body = r"""
const k = [
  { ref: 'A', name: 'SNMP walk success' },
  { ref: 'B', name: 'Credential success' },
  { ref: 'C', name: 'Session pool hit ratio' },
  { ref: 'D', name: 'Discovery modules healthy' },
  { ref: 'E', name: 'Graph freshness' },
  { ref: 'F', name: 'Federation spokes reporting' },
];
const n = k.length;
const ser = k.map((g, i) => {
  const v = scalar(g.ref);
  const pct = v === null ? 0 : Math.max(0, Math.min(100, v * 100));
  return {
    type: 'gauge', center: [((i + 0.5) / n * 100) + '%', '58%'], radius: '78%',
    startAngle: 220, endAngle: -40, min: 0, max: 100, splitNumber: 4,
    progress: { show: true, width: 12, roundCap: true,
      itemStyle: { color: pct >= 99 ? '#73BF69' : pct >= 90 ? '#FADE2A' : pct >= 75 ? '#FF9830' : '#F2495C' } },
    axisLine: { roundCap: true, lineStyle: { width: 12, color: [[1, GRID]] } },
    axisTick: { show: false }, splitLine: { show: false },
    axisLabel: { show: false },
    pointer: { show: false },
    anchor: { show: false },
    title: { show: true, offsetCenter: [0, '82%'], color: MUTED, fontSize: 13 },
    detail: { valueAnimation: true, offsetCenter: [0, '2%'], fontSize: 26, fontWeight: 600, color: TXT,
      formatter: () => (v === null ? 'n/a' : (pct >= 99.95 || pct === 0 ? pct.toFixed(0) : pct.toFixed(1)) + '%') },
    data: [{ value: pct, name: g.name }],
  };
});
return { backgroundColor: 'transparent', series: ser };
"""
panels.append(echarts(
    2, "Fabric health KPIs",
    "Estate-wide discovery and federation health over the selected range. Walk success = ok share of "
    "network_topology_snmp_walks_total; credential success = ok share of network_topology_credential_trials_total; "
    "pool hit ratio = session_pool_hits / (hits + misses); modules healthy = share of network_topology_module_last_status at 0; "
    "graph freshness = share of the range with network_topology_graph_stale at 0; spokes reporting = time-averaged network_topology_federation_spoke_up across hub spokes.",
    {"x": 0, "y": 4, "w": 24, "h": 7},
    [
        prom("A", 'sum(increase(network_topology_snmp_walks_total{status="ok"}[$__range])) / sum(increase(network_topology_snmp_walks_total[$__range]))'),
        prom("B", 'sum(increase(network_topology_credential_trials_total{status="ok"}[$__range])) / sum(increase(network_topology_credential_trials_total[$__range]))'),
        prom("C", 'sum(increase(network_topology_snmp_session_pool_hits_total[$__range])) / (sum(increase(network_topology_snmp_session_pool_hits_total[$__range])) + sum(increase(network_topology_snmp_session_pool_misses_total[$__range])))'),
        prom("D", 'count(network_topology_module_last_status == 0) / count(network_topology_module_last_status) or vector(0)'),
        prom("E", '1 - avg(avg_over_time(network_topology_graph_stale[$__range]))'),
        prom("F", 'avg(avg_over_time(network_topology_federation_spoke_up[$__range]))'),
    ], gauge_body))

# ---------------------------------------------------------------- 3 sunburst
sun_body = r"""
const dev = rows('A');
if (!dev.length) { return noData('No devices discovered for this scope'); }
const tree = {};
dev.forEach((d) => {
  const site = d.site || 'unknown'; const tier = TIER_NAME[tierOf(d.device_id)];
  const ven = d.vendor || 'unknown'; const os = d.os_version || 'unknown';
  tree[site] = tree[site] || {}; tree[site][tier] = tree[site][tier] || {};
  tree[site][tier][ven] = tree[site][tier][ven] || {};
  tree[site][tier][ven][os] = (tree[site][tier][ven][os] || 0) + 1;
});
const SITE_COLOR = ['#5794F2', '#B877D9', '#FF9830', '#73BF69', '#F2CC0C', '#8AB8FF', '#FF7383'];
const sites = Object.keys(tree).sort();
const tierKey = (name) => Object.keys(TIER_NAME).find((k) => TIER_NAME[k] === name) || 'other';
const data = sites.map((s, si) => ({
  name: s, itemStyle: { color: SITE_COLOR[si % SITE_COLOR.length] },
  children: Object.keys(tree[s]).sort((a, b) => TIER_ORDER.indexOf(tierKey(a)) - TIER_ORDER.indexOf(tierKey(b))).map((t) => ({
    name: t, itemStyle: { color: TIER_COLOR[tierKey(t)] },
    children: Object.keys(tree[s][t]).sort().map((v) => ({
      name: v, itemStyle: { color: VENDOR_COLOR[v] || '#8E8E8E' },
      children: Object.keys(tree[s][t][v]).map((o) => ({ name: o, value: tree[s][t][v][o],
        itemStyle: { color: VENDOR_COLOR[v] || '#8E8E8E', opacity: 0.7 } })),
    })),
  })),
}));
return {
  backgroundColor: 'transparent',
  tooltip: { trigger: 'item', formatter: (p) => p.treePathInfo.slice(1).map((x) => x.name).join(' / ') + '<br/><b>' + p.value + '</b> devices' },
  series: [{
    type: 'sunburst', data, radius: ['12%', '95%'], center: ['50%', '52%'], sort: undefined,
    nodeClick: 'rootToNode', emphasis: { focus: 'ancestor' },
    itemStyle: { borderColor: PANEL_BG, borderWidth: 1.5 },
    label: { color: '#fff', fontSize: 11, textBorderColor: 'rgba(0,0,0,0.45)', textBorderWidth: 2 },
    levels: [
      {},
      { r0: '12%', r: '34%', label: { rotate: 'tangential', fontSize: 12, fontWeight: 600, minAngle: 25 } },
      { r0: '34%', r: '62%', label: { rotate: 'radial', minAngle: 6 } },
      { r0: '62%', r: '82%', label: { rotate: 'radial', minAngle: 6 } },
      { r0: '82%', r: '86%', label: { show: false }, itemStyle: { borderWidth: 0.5 } },
    ],
  }],
};
"""
panels.append(echarts(
    3, "Estate composition - site, tier, vendor, OS",
    "Drill-down of every discovered device (network_topology_device_info): site -> tier (from the device_id prefix) -> "
    "vendor -> OS version (outer ring). Click a segment to zoom in; click the centre to zoom out.",
    {"x": 0, "y": 11, "w": 12, "h": 14},
    [prom("A", f'network_topology_device_info{{{EXP}}}')], sun_body))

# ---------------------------------------------------------------- 4 radar
radar_body = r"""
const axes = [
  { ref: 'A', name: 'Walk success' },
  { ref: 'B', name: 'Credential success' },
  { ref: 'C', name: 'Pool hit ratio' },
  { ref: 'D', name: 'Modules healthy' },
  { ref: 'E', name: 'Cycle budget adherence' },
  { ref: 'F', name: 'Graph freshness' },
];
const m = axes.map((a) => byInstance(a.ref));
const p95 = byInstance('G');
const inst = Array.from(new Set(m.flatMap((x) => Object.keys(x)))).sort();
if (!inst.length) { return noData('No discovery exporters reporting'); }
const COLORS = ['#5794F2', '#FF9830', '#73BF69', '#B877D9', '#F2495C'];
return {
  backgroundColor: 'transparent',
  color: COLORS,
  legend: { bottom: 0, textStyle: { color: TXT }, data: inst.map(expName) },
  tooltip: { trigger: 'item', formatter: (p) => {
    return '<b>' + p.name + '</b><br/>' + axes.map((a, j) => a.name + ': ' + (p.value[j] === null ? 'n/a' : (p.value[j] * 100).toFixed(1) + '%')).join('<br/>')
      + '<br/>Cycle p95: ' + (p95[p.data.inst] !== undefined ? p95[p.data.inst].toFixed(1) + ' s' : 'n/a');
  } },
  radar: {
    center: ['50%', '50%'], radius: '66%', shape: 'polygon', splitNumber: 4,
    indicator: axes.map((a) => ({ name: a.name, min: 0, max: 1 })),
    axisName: { color: TXT, fontSize: 12 },
    splitLine: { lineStyle: { color: GRID } },
    splitArea: { areaStyle: { color: ['transparent', theme.isDark ? 'rgba(255,255,255,0.025)' : 'rgba(0,0,0,0.025)'] } },
    axisLine: { lineStyle: { color: GRID } },
  },
  series: inst.map((i, k) => ({
    type: 'radar', name: expName(i), symbolSize: 6, itemStyle: { color: COLORS[k % COLORS.length] },
    lineStyle: { width: 2.2, color: COLORS[k % COLORS.length], type: k === 0 ? 'solid' : k === 1 ? 'solid' : 'dashed' },
    areaStyle: { opacity: 0.1 },
    data: [{ name: expName(i), inst: i,
      value: m.map((x) => (x[i] === undefined || isNaN(x[i]) ? null : Math.max(0, Math.min(1, x[i])))) }],
  })),
};
"""
panels.append(echarts(
    4, "Discovery health profile per exporter",
    "Each exporter normalised 0-1 on six axes over the selected range: SNMP walk success, credential success, session pool "
    "hit ratio, share of discovery modules at status 0, cycle budget adherence (1 - cycle_budget_skips / discovery cycles) and "
    "graph freshness (share of the range with graph_stale at 0). A full hexagon is a healthy exporter; a dent shows which subsystem is degrading. "
    "Tooltip adds the p95 discovery cycle time.",
    {"x": 12, "y": 11, "w": 12, "h": 14},
    [
        prom("A", 'sum by (instance)(increase(network_topology_snmp_walks_total{status="ok"}[$__range])) / sum by (instance)(increase(network_topology_snmp_walks_total[$__range]))'),
        prom("B", 'sum by (instance)(increase(network_topology_credential_trials_total{status="ok"}[$__range])) / sum by (instance)(increase(network_topology_credential_trials_total[$__range]))'),
        prom("C", 'sum by (instance)(increase(network_topology_snmp_session_pool_hits_total[$__range])) / (sum by (instance)(increase(network_topology_snmp_session_pool_hits_total[$__range])) + sum by (instance)(increase(network_topology_snmp_session_pool_misses_total[$__range])))'),
        prom("D", '(count by (instance)(network_topology_module_last_status == 0) or (count by (instance)(network_topology_module_last_status) * 0)) / count by (instance)(network_topology_module_last_status)'),
        prom("E", '1 - (sum by (instance)(increase(network_topology_cycle_budget_skips_total[$__range])) / sum by (instance)(increase(network_topology_discovery_cycle_duration_seconds_count[$__range])))'),
        prom("F", '1 - max by (instance)(avg_over_time(network_topology_graph_stale[$__range]))'),
        prom("G", 'histogram_quantile(0.95, sum by (instance, le)(rate(network_topology_discovery_cycle_duration_seconds_bucket[$__range])))'),
    ], radar_body))

# ---------------------------------------------------------------- 5 parallel coordinates
par_body = r"""
const dev = rows('A');
if (!dev.length) { return noData('No devices discovered for this scope'); }
const up = {}; rows('B').forEach((r) => { up[r.instance + '|' + r.device_id] = Number(r.__value); });
const dg = degreeMap(rows('C'));
const exps = Array.from(new Set(dev.map((d) => expName(d.instance)))).sort();
const tiers = TIER_ORDER.filter((t) => dev.some((d) => tierOf(d.device_id) === t)).map((t) => TIER_NAME[t]);
const vendors = Array.from(new Set(dev.map((d) => d.vendor))).sort();
const sites = Array.from(new Set(dev.map((d) => d.site))).sort();
const byTier = {};
dev.forEach((d) => {
  const key = d.instance + '|' + d.device_id; const t = tierOf(d.device_id);
  const deg = dg.nb[key] ? dg.nb[key].size : 0;
  const np = dg.protos[key] ? dg.protos[key].size : 0;
  const u = up[key] !== undefined ? +(up[key] / 86400).toFixed(1) : null;
  (byTier[t] = byTier[t] || []).push({ name: d.device_id, value: [expName(d.instance), d.site, TIER_NAME[t], d.vendor, u, deg, np] });
});
const all = Object.values(byTier).flat();
const maxOf = (j) => Math.max(1, ...all.map((x) => Number(x.value[j]) || 0));
const upMax = Math.ceil(maxOf(4) / 50) * 50; const degMax = maxOf(5); const prMax = maxOf(6);
const catAxis = (dim, name, data) => ({ dim, name, type: 'category', data });
return {
  backgroundColor: 'transparent',
  legend: { top: 0, textStyle: { color: TXT }, data: TIER_ORDER.filter((t) => byTier[t]).map((t) => TIER_NAME[t]) },
  tooltip: { trigger: 'item', formatter: (p) => '<b>' + p.name + '</b><br/>' + p.value[0] + ' / ' + p.value[1] + '<br/>' + p.value[2] + ', ' + p.value[3]
    + '<br/>Uptime ' + p.value[4] + ' d<br/>Degree ' + p.value[5] + ' neighbours, ' + p.value[6] + ' protocols' },
  parallel: { left: 70, right: 90, top: 60, bottom: 30,
    parallelAxisDefault: {
      nameLocation: 'start', nameGap: 18, nameTextStyle: { color: TXT, fontSize: 12, fontWeight: 600 },
      axisLine: { lineStyle: { color: MUTED } }, axisTick: { lineStyle: { color: MUTED } },
      axisLabel: { color: TXT, fontSize: 11, backgroundColor: PANEL_BG, padding: [1, 3], borderRadius: 2 }, splitLine: { show: false },
    } },
  parallelAxis: [
    catAxis(0, 'Exporter', exps), catAxis(1, 'Site', sites), catAxis(2, 'Tier', tiers), catAxis(3, 'Vendor', vendors),
    { dim: 4, name: 'Uptime (days)', type: 'value', min: 0, max: upMax },
    { dim: 5, name: 'Degree', type: 'value', min: 0, max: degMax, minInterval: 1 },
    { dim: 6, name: 'Protocols', type: 'value', min: 0, max: prMax, minInterval: 1 },
  ],
  series: TIER_ORDER.filter((t) => byTier[t]).map((t) => ({
    name: TIER_NAME[t], type: 'parallel', smooth: 0.15, data: byTier[t],
    lineStyle: { color: TIER_COLOR[t], width: t === 'host' ? 0.8 : 1.6, opacity: t === 'host' ? 0.35 : 0.75 },
    emphasis: { lineStyle: { width: 3, opacity: 1 } },
  })),
};
"""
panels.append(echarts(
    5, "Device profile - parallel coordinates",
    "One line per discovered device across exporter, site, tier, vendor, uptime (network_topology_device_uptime_seconds), "
    "degree (distinct neighbours) and number of discovery protocols seeing it (both from network_topology_edge_info). "
    "Drag along any axis to brush a range, for example low-uptime high-degree devices.",
    {"x": 0, "y": 25, "w": 16, "h": 14},
    [prom("A", f'network_topology_device_info{{{EXP}}}'),
     prom("B", f'network_topology_device_uptime_seconds{{{EXP}}}'),
     prom("C", f'network_topology_edge_info{{{EXP}}}')], par_body))

# ---------------------------------------------------------------- 6 polar
polar_body = r"""
const r = rows('A');
if (!r.length) { return noData('No links discovered for this scope'); }
const inst = Array.from(new Set(r.map((x) => x.instance))).sort();
const protos = Object.keys(PROTO_COLOR).filter((p) => r.some((x) => x.discovery_proto === p));
const tot = {}; r.forEach((x) => { tot[x.instance] = (tot[x.instance] || 0) + Number(x.__value); });
return {
  backgroundColor: 'transparent',
  legend: { bottom: 0, textStyle: { color: TXT }, itemWidth: 12, itemHeight: 10 },
  tooltip: { trigger: 'item', formatter: (p) => '<b>' + p.name + '</b><br/>' + p.seriesName.toUpperCase() + ': ' + p.value + ' links' },
  polar: { radius: ['14%', '78%'], center: ['50%', '46%'] },
  angleAxis: { type: 'value', startAngle: 90, max: (v) => v.max * 1.12, axisLine: { lineStyle: { color: GRID } }, splitLine: { lineStyle: { color: GRID } },
    axisTick: { show: false }, axisLabel: { show: false } },
  radiusAxis: { type: 'category', data: inst.map((i) => expName(i) + ' (' + tot[i] + ')'), z: 10,
    axisLabel: { color: TXT, fontSize: 11, fontWeight: 600, interval: 0, align: 'right', margin: 6, backgroundColor: PANEL_BG, padding: [2, 4], borderRadius: 3 }, axisLine: { show: false }, axisTick: { show: false } },
  series: protos.map((p) => ({
    type: 'bar', name: p, coordinateSystem: 'polar', stack: 'links', roundCap: false, barWidth: '62%',
    itemStyle: { color: PROTO_COLOR[p], borderColor: PANEL_BG, borderWidth: 1 },
    data: inst.map((i) => { const x = r.find((y) => y.instance === i && y.discovery_proto === p); return x ? Number(x.__value) : 0; }),
  })),
};
"""
panels.append(echarts(
    6, "Links by discovery protocol",
    "Radial stacked bar of discovered links per exporter, split by discovery protocol: count by (instance, discovery_proto) "
    "of network_topology_edge_info. Ring order is exporter; the arc length is the link count, total in brackets.",
    {"x": 16, "y": 25, "w": 8, "h": 14},
    [prom("A", f'count by (instance, discovery_proto)(network_topology_edge_info{{{EXP}}})')], polar_body))

# ---------------------------------------------------------------- 7 boxplot degree per tier
box_body = r"""
const dev = rows('A');
if (!dev.length) { return noData('No devices discovered for this scope'); }
const dg = degreeMap(rows('B'));
const groups = {};
dev.forEach((d) => {
  const key = d.instance + '|' + d.device_id; const t = tierOf(d.device_id);
  (groups[t] = groups[t] || []).push({ v: dg.nb[key] ? dg.nb[key].size : 0, id: d.device_id, inst: d.instance });
});
const tiers = TIER_ORDER.filter((t) => groups[t]);
function q(a, p) { const i = (a.length - 1) * p; const lo = Math.floor(i); const hi = Math.ceil(i); return a[lo] + (a[hi] - a[lo]) * (i - lo); }
const box = []; const outl = []; const counts = [];
tiers.forEach((t, ti) => {
  const a = groups[t].map((x) => x.v).sort((x, y) => x - y);
  const q1 = q(a, 0.25); const q2 = q(a, 0.5); const q3 = q(a, 0.75); const iqr = q3 - q1;
  const lw = Math.max(a[0], q1 - 1.5 * iqr); const hw = Math.min(a[a.length - 1], q3 + 1.5 * iqr);
  box.push({ value: [lw, q1, q2, q3, hw], itemStyle: { color: TIER_COLOR[t] + '55', borderColor: TIER_COLOR[t] } });
  counts.push(a.length);
  groups[t].forEach((x) => { if (x.v < lw || x.v > hw) { outl.push({ value: [ti, x.v], name: x.id + ' (' + shortExp(x.inst) + ')' }); } });
});
return {
  backgroundColor: 'transparent',
  grid: { left: 50, right: 20, top: 30, bottom: 50 },
  tooltip: { trigger: 'item', formatter: (p) => p.seriesType === 'boxplot'
    ? '<b>' + p.name + '</b> (' + counts[p.dataIndex] + ' devices)<br/>max ' + p.value[5] + '<br/>Q3 ' + p.value[4] + '<br/>median ' + p.value[3] + '<br/>Q1 ' + p.value[2] + '<br/>min ' + p.value[1]
    : '<b>' + p.name + '</b><br/>degree ' + p.value[1] },
  xAxis: { type: 'category', data: tiers.map((t, i) => TIER_NAME[t] + '\n' + counts[i] + ' devices'),
    axisLabel: { color: TXT, fontSize: 11, interval: 0 }, axisLine: { lineStyle: { color: MUTED } } },
  yAxis: { type: 'value', name: 'Neighbours', nameTextStyle: { color: MUTED }, minInterval: 1,
    axisLabel: { color: MUTED }, splitLine: { lineStyle: { color: GRID } } },
  series: [
    { type: 'boxplot', name: 'Degree', data: box, boxWidth: [16, 46] },
    { type: 'scatter', name: 'Outlier', data: outl, symbolSize: 8, itemStyle: { color: '#F2495C' } },
  ],
};
"""
panels.append(echarts(
    7, "Device degree distribution by tier",
    "Box plot of distinct neighbours per device, grouped by tier (device_id prefix), computed from network_topology_edge_info "
    "joined to network_topology_device_info. Whiskers at 1.5 IQR; red points are outliers such as an under-cabled leaf "
    "or a host with an extra adjacency.",
    {"x": 0, "y": 39, "w": 9, "h": 13},
    [prom("A", f'network_topology_device_info{{{EXP}}}'),
     prom("B", f'network_topology_edge_info{{{EXP}}}')], box_body))

# ---------------------------------------------------------------- 8 sankey tier to tier
sankey_body = r"""
const e = rows('A');
if (!e.length) { return noData('No links discovered for this scope'); }
// One undirected link per device pair, oriented from the upper tier to the lower tier so the flow is acyclic.
const seen = {}; const flows = {}; const protoMix = {}; let intra = 0;
e.forEach((x) => {
  const a = x.src_device; const b = x.dst_device;
  const k = x.instance + '|' + (a < b ? a + '|' + b : b + '|' + a) + '|' + x.discovery_proto;
  if (seen[k]) { return; } seen[k] = 1;
  let ta = tierOf(a); let tb = tierOf(b);
  if (ta === tb) { intra++; return; }
  if (TIER_ORDER.indexOf(ta) > TIER_ORDER.indexOf(tb)) { const t = ta; ta = tb; tb = t; }
  const fk = ta + '>' + tb; flows[fk] = (flows[fk] || 0) + 1;
  protoMix[fk] = protoMix[fk] || {}; protoMix[fk][x.discovery_proto] = (protoMix[fk][x.discovery_proto] || 0) + 1;
});
const used = new Set(); Object.keys(flows).forEach((k) => k.split('>').forEach((t) => used.add(t)));
const nodes = TIER_ORDER.filter((t) => used.has(t)).map((t) => ({ name: TIER_NAME[t], itemStyle: { color: TIER_COLOR[t], borderColor: TIER_COLOR[t] } }));
const links = Object.keys(flows).map((k) => { const [s, d] = k.split('>'); return { source: TIER_NAME[s], target: TIER_NAME[d], value: flows[k], key: k }; });
return {
  backgroundColor: 'transparent',
  title: { text: intra ? intra + ' intra-tier adjacencies (for example spine-spine, WAN core mesh) not drawn' : '', right: 10, bottom: 4,
    textStyle: { color: MUTED, fontSize: 11, fontWeight: 'normal' } },
  tooltip: { trigger: 'item', formatter: (p) => {
    if (p.dataType === 'edge') {
      const mix = protoMix[p.data.key] || {};
      return '<b>' + p.data.source + ' -> ' + p.data.target + '</b>: ' + p.data.value + ' links<br/>'
        + Object.keys(mix).map((m) => m.toUpperCase() + ' ' + mix[m]).join(', ');
    }
    return '<b>' + p.name + '</b>: ' + p.value + ' link ends';
  } },
  series: [{
    type: 'sankey', left: 20, right: 110, top: 16, bottom: 28, nodeWidth: 18, nodeGap: 14, layoutIterations: 64,
    data: nodes, links, emphasis: { focus: 'adjacency' },
    label: { color: TXT, fontSize: 12, fontWeight: 600, formatter: (p) => p.name + '  ' + p.value },
    lineStyle: { color: 'gradient', curveness: 0.5, opacity: 0.4 },
  }],
};
"""
panels.append(echarts(
    8, "Tier-to-tier link flow",
    "Sankey of discovered adjacencies between tiers, one link per device pair and protocol from network_topology_edge_info, "
    "oriented from the upper tier (external peer, edge, WAN core) down to hosts. Hover a band for its protocol mix.",
    {"x": 9, "y": 39, "w": 15, "h": 13},
    [prom("A", f'network_topology_edge_info{{{EXP}}}')], sankey_body))

# ---------------------------------------------------------------- 9 themeRiver (Loki)
river_body = r"""
const fr = frames('A');
const pts = {}; const names = new Set(); const times = new Set();
fr.forEach((f) => {
  const tf = f.fields.find((x) => x.type === 'time'); const vf = f.fields.find((x) => x.type === 'number');
  if (!tf || !vf) { return; }
  const l = vf.labels || {};
  const kind = l.change_kind ? 'change ' + l.change_kind : (l.conflict_type ? 'conflict' : 'other');
  const name = (EXP_NAME[shortExp(l.instance)] ? { 'ent-dc1': 'DC1', 'glb-hub': 'Hub', 'spoke-emea': 'EMEA' }[shortExp(l.instance)] : shortExp(l.instance)) + ' ' + kind;
  names.add(name);
  const n = tf.values.length;
  for (let i = 0; i < n; i++) {
    const t = tf.values.get ? tf.values.get(i) : tf.values[i]; const v = vf.values.get ? vf.values.get(i) : vf.values[i];
    times.add(t); pts[name + '@' + t] = (pts[name + '@' + t] || 0) + (Number(v) || 0);
  }
});
if (!names.size) { return noData('No topology events in range'); }
const ts = Array.from(times).sort((a, b) => a - b);
const nm = Array.from(names).sort();
const data = [];
nm.forEach((n) => ts.forEach((t) => data.push([t, pts[n + '@' + t] || 0, n])));
const PAL = ['#5794F2', '#8AB8FF', '#73BF69', '#96D98D', '#FF9830', '#FFB357', '#B877D9', '#CA95E5', '#F2495C', '#FADE2A'];
return {
  backgroundColor: 'transparent', color: PAL,
  legend: { top: 0, left: 10, right: 10, textStyle: { color: TXT, fontSize: 11 }, itemWidth: 12, itemHeight: 9, itemGap: 8, data: nm },
  tooltip: { trigger: 'axis', axisPointer: { type: 'line', lineStyle: { color: MUTED } } },
  singleAxis: { type: 'time', top: 70, bottom: 34, left: 30, right: 30,
    axisLabel: { color: MUTED, formatter: { hour: '{HH}:{mm}', minute: '{HH}:{mm}' } },
    axisLine: { lineStyle: { color: MUTED } }, splitLine: { show: true, lineStyle: { color: GRID } } },
  series: [{ type: 'themeRiver', data, label: { show: false }, emphasis: { focus: 'self' } }],
};
"""
river_target = {"datasource": LOKI, "refId": "A", "editorMode": "code", "queryType": "range",
                "expr": 'sum by (instance, change_kind, conflict_type)(count_over_time({source="network-topology-exporter", ' + EXP + '}[$__auto]))',
                "step": "2m", "legendFormat": ""}
panels.append(echarts(
    9, "Topology event flow",
    "ThemeRiver of topology change and conflict log events per exporter, from the exporter's Loki stream "
    "{source=\"network-topology-exporter\"} counted by change_kind / conflict_type. Band width is events per 2 minute step.",
    {"x": 0, "y": 52, "w": 12, "h": 12},
    [river_target], river_body))

# ---------------------------------------------------------------- 10 matrix heatmap
heat_body = r"""
const fr = frames('A');
const hosts = []; const times = new Set(); const cell = {};
fr.forEach((f) => {
  const tf = f.fields.find((x) => x.type === 'time'); const vf = f.fields.find((x) => x.type === 'number');
  if (!tf || !vf) { return; }
  const h = (vf.labels && vf.labels.instance) || vf.name;
  if (hosts.indexOf(h) < 0) { hosts.push(h); }
  for (let i = 0; i < tf.values.length; i++) {
    const t = tf.values.get ? tf.values.get(i) : tf.values[i]; const v = vf.values.get ? vf.values.get(i) : vf.values[i];
    if (v === null || v === undefined) { continue; }
    times.add(t); cell[h + '@' + t] = Number(v) / 1e6;
  }
});
if (!hosts.length) { return noData('No interface counters for the server fleet'); }
hosts.sort();
const ts = Array.from(times).sort((a, b) => a - b);
const pad = (n) => (n < 10 ? '0' : '') + n;
const lab = ts.map((t) => { const d = new Date(t); return pad(d.getHours()) + ':' + pad(d.getMinutes()); });
const data = []; let mn = Infinity; let mx = -Infinity;
hosts.forEach((h, y) => ts.forEach((t, x) => { const v = cell[h + '@' + t]; if (v !== undefined) { data.push([x, y, +v.toFixed(2)]); mn = Math.min(mn, v); mx = Math.max(mx, v); } }));
return {
  backgroundColor: 'transparent',
  tooltip: { position: 'top', formatter: (p) => '<b>' + hosts[p.value[1]] + '</b> at ' + lab[p.value[0]] + '<br/>' + p.value[2] + ' Mbit/s (rx + tx)' },
  grid: { left: 110, right: 70, top: 10, bottom: 40 },
  xAxis: { type: 'category', data: lab, splitArea: { show: false }, axisLabel: { color: MUTED, fontSize: 10 }, axisLine: { lineStyle: { color: MUTED } } },
  yAxis: { type: 'category', data: hosts, axisLabel: { color: TXT, fontSize: 11 }, axisLine: { show: false }, axisTick: { show: false } },
  visualMap: { min: Math.floor(mn), max: Math.ceil(mx), calculable: true, orient: 'vertical', right: 0, top: 'middle', itemHeight: 140,
    text: ['Mbit/s', ''], textStyle: { color: MUTED },
    inRange: { color: ['#0b2a4a', '#1f60c4', '#3d9be9', '#73BF69', '#FADE2A', '#FF9830', '#F2495C'] } },
  series: [{ type: 'heatmap', data, itemStyle: { borderColor: PANEL_BG, borderWidth: 1.5, borderRadius: 2 },
    emphasis: { itemStyle: { borderColor: TXT, borderWidth: 1 } } }],
};
"""
heat_expr = ('(sum by (instance)(rate(node_network_receive_bytes_total{device!="lo"}[$__rate_interval]) + rate(node_network_transmit_bytes_total{device!="lo"}[$__rate_interval])) * 8)'
             ' or (sum by (instance)(rate(windows_net_bytes_received_total[$__rate_interval]) + rate(windows_net_bytes_sent_total[$__rate_interval])) * 8)')
panels.append(echarts(
    10, "Server fleet interface throughput matrix",
    "Host x time matrix of total interface throughput (receive + transmit, bits per second) from node_network_receive/transmit_bytes_total "
    "(Linux and macOS, loopback excluded) and windows_net_bytes_received/sent_total (Windows), sampled every 2 minutes.",
    {"x": 12, "y": 52, "w": 12, "h": 12},
    [prom("A", heat_expr, instant=False, fmt="time_series", interval="2m", legend="{{instance}}")], heat_body))

dash = {
    "apiVersion": "dashboard.grafana.app/v1beta1",
    "kind": "Dashboard",
    "metadata": {"name": "nos-network-analytics", "annotations": {"grafana.app/folder": "netobs-showcase"}},
    "spec": {
        "title": "Network Observability - Network Analytics",
        "description": "Multi-dimensional analytics of the discovered global network: fabric health KPIs, exporter health radar, estate composition, device profiles, link flow, degree distribution, topology event flow and server fleet throughput.",
        "uid": "nos-network-analytics",
        "tags": ["netobs-showcase", "analytics"],
        "editable": True, "graphTooltip": 1, "schemaVersion": 41, "refresh": "1m",
        "time": {"from": "now-1h", "to": "now"}, "timezone": "browser",
        "links": [{"type": "dashboards", "tags": ["netobs-showcase"], "asDropdown": True, "title": "Network Observability",
                   "includeVars": False, "keepTime": True, "targetBlank": False, "icon": "external link", "tooltip": "", "url": ""}],
        "templating": {"list": [{
            "type": "query", "name": "exporter", "label": "Exporter", "datasource": PROM,
            "query": {"qryType": 1, "query": "label_values(network_topology_device_info, instance)", "refId": "PrometheusVariableQueryEditor-VariableQuery"},
            "definition": "label_values(network_topology_device_info, instance)", "refresh": 2, "sort": 1,
            "multi": True, "includeAll": True, "allValue": ".*",
            "current": {"text": ["All"], "value": ["$__all"]}, "options": [], "hide": 0}]},
        "annotations": {"list": [{"builtIn": 1, "datasource": {"type": "grafana", "uid": "-- Grafana --"}, "enable": True, "hide": True,
                                  "iconColor": "rgba(0, 211, 255, 1)", "name": "Annotations & Alerts", "type": "dashboard"}]},
        "panels": panels,
    },
}
json.dump(dash, open(OUT, "w"), indent=2)
print("wrote", OUT, len(panels), "panels")
