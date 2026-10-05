#!/usr/bin/env python3
"""Generate nos-device-investigation.json (lane device).

Operator drilldown for one discovered device: identity, reachability, adjacencies, a 2-hop ego
network, change/conflict events and the health of the exporter that polls it.
"""
import os
import json

UID = "nos-device-investigation"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), UID + ".json")
PROM = {"type": "prometheus", "uid": "grafanacloud-prom"}
LOKI = {"type": "loki", "uid": "grafanacloud-logs"}
TIER_RE = "^(edge-fw|wan-core|ext-peer|spine|leaf|host).*"

EX = 'instance="$exporter"'
# "Present now" = a sample inside the last two poll intervals. A bare selector would look back five
# minutes and hide a short outage entirely, because the series carry no staleness markers.
NOW = "2m"
DSEL = f'{EX},device_id="$device"'
INFO = f"network_topology_device_info{{{DSEL}}}"
UP = f"network_topology_device_uptime_seconds{{{DSEL}}}"
EXPORTER_ALIVE = f"count(network_topology_graph_devices_total{{{EX}}})"
SELF_LINK = "/d/" + UID + "/?var-exporter=${exporter}&var-device=${__value.raw}&${__url_time_range}"

# ---------------------------------------------------------------- shared PromQL
# Adjacency selectors normalised to the selected device's point of view.
ADJ_L = "neighbour, local_port, remote_port, discovery_proto, link_kind, direction, side"


def adj(rng=None):
    def sel(extra):
        s = f'network_topology_edge_info{{{EX},{extra}}}'
        return f"last_over_time({s}[{rng}])" if rng else f"max_over_time({s}[{NOW}])"
    out_ = (f'label_replace(label_replace(label_replace(label_replace({sel("src_device=\"$device\"")},'
            f' "neighbour", "$1", "dst_device", "(.*)"), "local_port", "$1", "src_port", "(.*)"),'
            f' "remote_port", "$1", "dst_port", "(.*)"), "side", "outbound", "", "")')
    in_ = (f'label_replace(label_replace(label_replace(label_replace({sel("dst_device=\"$device\"")},'
           f' "neighbour", "$1", "src_device", "(.*)"), "local_port", "$1", "dst_port", "(.*)"),'
           f' "remote_port", "$1", "src_port", "(.*)"), "side", "inbound", "", "")')
    return f"max by ({ADJ_L}) ({out_} or {in_})"


# 1 = adjacency present now, 0 = seen in the last hour but no longer reported.
ADJ_STATE = f"({adj()} or (0 * {adj('1h')}))"

# Device present (1) / seen in the last hour but absent (0) for a whole exporter.
DEV_L = "instance, device_id, vendor, os_version, site"
DEV_STATE_EX = (f"(max by ({DEV_L}) (max_over_time(network_topology_device_info{{{EX}}}[{NOW}]))"
                f" or (0 * max by ({DEV_L}) (last_over_time(network_topology_device_info{{{EX}}}[1h]))))")
EDGE_L = "src_device, src_port, dst_device, dst_port, discovery_proto, link_kind, direction"
EDGE_STATE_EX = (f"(max by ({EDGE_L}) (max_over_time(network_topology_edge_info{{{EX}}}[{NOW}]))"
                 f" or (0 * max by ({EDGE_L}) (last_over_time(network_topology_edge_info{{{EX}}}[1h]))))")

# Card values (instant, table).
CARD_ID = f"max by (device_id, vendor, os_version, site) (last_over_time({INFO}[1h]))"
CARD_STATE = (f"(2 * (count(max_over_time({UP}[{NOW}]) < 3600) > 0)) or count(max_over_time({INFO}[{NOW}])) or (0 * count(last_over_time({INFO}[1h])))")
CARD_UPTIME = f"max(max_over_time({UP}[{NOW}]))"
CARD_BOOT = f"max(last_over_time((timestamp({UP}) - {UP})[1h:1m]))"
CARD_LASTSEEN = f"max(last_over_time(timestamp({INFO})[1h:1m]))"
CARD_ADJ = f"count({adj()}) or vector(0)"
CARD_ADJ_LOST = f"count({adj('1h')} unless {adj()}) or vector(0)"
CARD_STALE = f"max(network_topology_graph_stale{{{EX}}})"
CARD_MODS = f"count(network_topology_module_last_status{{{EX}}} > 0) or vector(0)"


def q(expr, ref, fmt="table", instant=True, legend=None, ds=PROM):
    t = {"datasource": ds, "refId": ref, "expr": expr, "editorMode": "code"}
    if ds is PROM:
        t.update({"format": fmt, "instant": instant, "range": not instant})
    else:
        t["queryType"] = "range"
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


def row(title, y):
    add({"type": "row", "title": title, "collapsed": False, "panels": []}, 0, y, 24, 1)


VENDOR_COLORS = {"arista": "#5794F2", "cisco": "#73BF69", "juniper": "#FF9830", "nokia": "#B877D9"}
VENDOR_MAP = [{"type": "value", "options": {v: {"color": c, "index": i}
                                            for i, (v, c) in enumerate(VENDOR_COLORS.items())}}]
PRESENCE_MAP = [{"type": "value", "options": {
    "1": {"text": "Reachable", "color": "green", "index": 0},
    "0": {"text": "Unreachable", "color": "red", "index": 1}}}]
LINK_MAP = [{"type": "value", "options": {
    "1": {"text": "Up", "color": "green", "index": 0},
    "0": {"text": "Lost", "color": "red", "index": 1}}}]

# ---------------------------------------------------------------- identity card (Business Text)
CARD_HELPERS = r"""
// In "All data" mode context.data.data is one array of row objects per returned frame. Frames that
// return nothing are omitted, so look each value up by its "Value #<refId>" column, not by index.
const raw = context.data && Array.isArray(context.data.data) ? context.data.data : context.data;
const frames = Array.isArray(raw) ? raw : [];
const theme = context.grafana.theme;
const rowFor = (ref) => {
  for (const f of frames) {
    const rowsArr = Array.isArray(f) ? f : [f];
    const r = rowsArr.find((x) => x && Object.prototype.hasOwnProperty.call(x, 'Value #' + ref));
    if (r) { return r; }
  }
  return null;
};
const val = (ref) => {
  const r = rowFor(ref);
  if (!r) { return null; }
  const v = Number(r['Value #' + ref]);
  return Number.isNaN(v) ? null : v;
};
const lab = (ref, name) => { const r = rowFor(ref); return r && r[name] !== undefined ? String(r[name]) : ''; };
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const dur = (s) => {
  if (s === null) { return '-'; }
  const d = Math.floor(s / 86400); const h = Math.floor((s % 86400) / 3600); const m = Math.floor((s % 3600) / 60);
  return d > 0 ? d + 'd ' + h + 'h' : (h > 0 ? h + 'h ' + m + 'm' : m + 'm');
};
const ts = (s) => {
  if (s === null) { return '-'; }
  const d = new Date(s * 1000);
  const pad = (n) => String(n).padStart(2, '0');
  const MON = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  return d.getDate() + ' ' + MON[d.getMonth()] + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
};
const ago = (s) => {
  if (s === null) { return ''; }
  // Relative to the end of the dashboard time range, so a past window reads correctly.
  const tr = context.grafana.timeRange;
  const end = tr && tr.to && tr.to.valueOf ? tr.to.valueOf() / 1000 : Date.now() / 1000;
  const secs = Math.max(0, end - s);
  return dur(secs) + ' ago';
};
const TIER = { 'edge-fw': 'Edge firewall', 'wan-core': 'WAN core', 'ext-peer': 'External peer', spine: 'Spine',
  leaf: 'Leaf', host: 'Host' };
const VC = { arista: '#5794F2', cisco: '#73BF69', juniper: '#FF9830', nokia: '#B877D9' };

context.handlebars.registerHelper('dbg', () => JSON.stringify({d: context.data, k: Object.keys(context)}).slice(0, 3000));
context.handlebars.registerHelper('deviceCard', () => {
  const device = context.grafana.replaceVariables('$device');
  const exporter = context.grafana.replaceVariables('$exporter');
  const m = /^(edge-fw|wan-core|ext-peer|spine|leaf|host)/.exec(device);
  const tier = m ? TIER[m[1]] : 'Other';
  const vendor = lab('A', 'vendor');
  const os = lab('A', 'os_version');
  const site = lab('A', 'site');
  const state = val('B');
  const uptime = val('C');
  const boot = val('D');
  const lastSeen = val('E');
  const adjN = val('F');
  const adjLost = val('G');
  const stale = val('H');
  const mods = val('I');
  let st = { t: 'NO DATA', c: theme.colors.text.secondary, s: 'not reported in the last hour' };
  if (state === 1) { st = { t: 'UP', c: '#56A64B', s: 'responding to SNMP polls' }; }
  if (state === 2) { st = { t: 'RECENTLY REBOOTED', c: '#FF9830', s: 'sysUpTime under one hour' }; }
  if (state === 0) { st = { t: 'UNREACHABLE', c: '#E02F44', s: 'last seen ' + ago(lastSeen) }; }
  const poll = stale === 1 || (mods !== null && mods > 0)
    ? { t: stale === 1 ? 'Graph stale' : 'Modules degraded', c: '#FF9830' }
    : { t: 'Polling healthy', c: '#56A64B' };
  const kpi = (l, v, s, color) => '<div class="ni-kpi"><span class="l">' + esc(l) + '</span><span class="v"'
    + (color ? ' style="color:' + color + '"' : '') + '>' + esc(v) + '</span><span class="s">' + esc(s) + '</span></div>';
  const html = '<div class="ni-wrap">'
    + '<div class="ni-id" style="border-left-color:' + st.c + '">'
    + '<span class="ni-tier">' + esc(tier) + '</span>'
    + '<span class="ni-name">' + esc(device) + '</span>'
    + '<span class="ni-sub"><span class="ni-dot" style="background:' + (VC[vendor] || '#9FA7B3') + '"></span>'
    + esc(vendor || 'unknown') + ' ' + esc(os) + ' &middot; site ' + esc(site || '-') + '</span>'
    + '<span class="ni-sub">polled by ' + esc(exporter) + '</span></div>'
    + '<div class="ni-kpi ni-state" style="background:' + st.c + '22;border-color:' + st.c + '"><span class="l">Current state</span>'
    + '<span class="v" style="color:' + st.c + '">' + esc(st.t) + '</span><span class="s">' + esc(st.s) + '</span></div>'
    + kpi('Uptime', state === 0 ? '-' : dur(uptime), state === 0 ? 'no sysUpTime while unreachable' : 'SNMP sysUpTime')
    + kpi('Last reboot', ts(boot), boot === null ? '' : ago(boot))
    + kpi('Adjacencies', adjN === null ? '-' : String(adjN), (adjLost || 0) > 0 ? adjLost + ' lost in the last hour' : 'none lost in the last hour',
      (adjLost || 0) > 0 ? '#E02F44' : null)
    + kpi('Exporter', poll.t, 'discovery health of ' + exporter, poll.c)
    + '</div>';
  return new context.handlebars.SafeString(html);
});
"""

CARD_CSS = """
.ni-wrap{display:flex;gap:10px;align-items:stretch;height:100%;font-family:Inter,sans-serif;}
.ni-id{flex:2;display:flex;flex-direction:column;justify-content:center;padding:4px 16px;border-left:6px solid #56A64B;}
.ni-tier{font-size:12px;text-transform:uppercase;letter-spacing:.08em;opacity:.7;}
.ni-name{font-size:30px;font-weight:600;line-height:1.15;}
.ni-sub{font-size:13px;opacity:.8;line-height:1.5;display:flex;align-items:center;gap:6px;}
.ni-dot{display:inline-block;width:10px;height:10px;border-radius:50%;}
.ni-kpi{flex:1;display:flex;flex-direction:column;justify-content:center;padding:6px 14px;border-radius:6px;background:rgba(128,128,128,.12);border:1px solid transparent;min-width:0;}
.ni-state{flex:1.4;}
.ni-kpi .l{font-size:12px;text-transform:uppercase;letter-spacing:.06em;opacity:.7;}
.ni-kpi .v{font-size:24px;font-weight:600;line-height:1.2;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.ni-kpi .s{font-size:12px;opacity:.7;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
"""

add({
    "type": "marcusolsson-dynamictext-panel", "title": "",
    "description": "Identity from network_topology_device_info (last hour, so an unreachable device still shows), "
                   "state from presence of the device in the current poll and network_topology_device_uptime_seconds "
                   "(under one hour = recently rebooted), last reboot = sample time minus sysUpTime, adjacencies from "
                   "network_topology_edge_info, exporter health from network_topology_graph_stale and "
                   "network_topology_module_last_status.",
    "targets": [q(CARD_ID, "A"), q(CARD_STATE, "B"), q(CARD_UPTIME, "C"), q(CARD_BOOT, "D"),
                q(CARD_LASTSEEN, "E"), q(CARD_ADJ, "F"), q(CARD_ADJ_LOST, "G"), q(CARD_STALE, "H"),
                q(CARD_MODS, "I")],
    "options": {"renderMode": "data", "content": os.environ.get("CARD_DEBUG") and "<pre style='font-size:10px'>{{dbg}}</pre>" or "{{deviceCard}}",
                "defaultContent": "<p>The selected device has not been reported by $exporter in the last hour.</p>",
                "helpers": CARD_HELPERS.strip(), "styles": CARD_CSS.strip(), "wrap": True, "status": "",
                "editors": ["default", "styles", "helpers"], "externalStyles": [], "afterRender": "",
                "contentPartials": [], "editor": {"language": "html", "format": "auto"}},
    "transparent": True,
}, 0, 0, 24, 4)

# ---------------------------------------------------------------- reachability and uptime
row("Reachability and uptime", 4)
add({
    "type": "status-history", "title": "Reachability - $device",
    "description": "Reachable when the device appears in the exporter's current poll (network_topology_device_info); "
                   "Unreachable when the exporter is reporting but the device is missing. Gaps mean the exporter "
                   "itself was not reporting. The second row shows whether the exporter's discovery graph was fresh "
                   "(network_topology_graph_stale = 0) at the same time.",
    "targets": [
        q(f"(count(max_over_time({INFO}[{NOW}])) or (0 * {EXPORTER_ALIVE}))", "A", fmt="time_series", instant=False, legend="Device"),
        q(f"1 - max(network_topology_graph_stale{{{EX}}})", "B", fmt="time_series", instant=False,
          legend="Discovery graph"),
    ],
    "fieldConfig": {"defaults": {"mappings": PRESENCE_MAP, "color": {"mode": "thresholds"},
                                 "thresholds": {"mode": "absolute", "steps": [{"color": "red", "value": None},
                                                                              {"color": "green", "value": 1}]},
                                 "custom": {"fillOpacity": 85, "lineWidth": 0}, "min": 0, "max": 1},
                    "overrides": [{"matcher": {"id": "byName", "options": "Discovery graph"}, "properties": [
                        {"id": "mappings", "value": [{"type": "value", "options": {
                            "1": {"text": "Fresh", "color": "green", "index": 0},
                            "0": {"text": "Stale", "color": "#FF9830", "index": 1}}}]}]}]},
    "options": {"showValue": "never", "rowHeight": 0.85, "colWidth": 0.95,
                "legend": {"showLegend": False}, "tooltip": {"mode": "single", "sort": "none"}},
    "interval": "1m",
}, 0, 5, 12, 7)

add({
    "type": "timeseries", "title": "Uptime (SNMP sysUpTime)",
    "description": "network_topology_device_uptime_seconds for the device. A drop back towards zero is a reboot or "
                   "an SNMP agent restart; a gap is a period when the device did not answer polls.",
    "targets": [q(f"max({UP}) / 86400", "A", fmt="time_series", instant=False, legend="sysUpTime")],
    "fieldConfig": {"defaults": {"unit": "suffix: days", "decimals": 2,
                                 "color": {"mode": "fixed", "fixedColor": "#5794F2"},
                                 "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 12,
                                            "gradientMode": "opacity", "showPoints": "never",
                                            "spanNulls": False, "lineInterpolation": "linear"}},
                    "overrides": []},
    "options": {"legend": {"showLegend": False}, "tooltip": {"mode": "single", "sort": "none"}},
}, 12, 5, 12, 7)

# ---------------------------------------------------------------- neighbours and ego network
row("Neighbours and ego network", 13)

EGO_JS = r"""
const series = context.panel.data.series || [];
const theme = context.grafana.theme;
const textColor = theme.colors.text.primary;
const mutedColor = theme.colors.text.secondary;
const centre = context.grafana.replaceVariables('$device');

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
const valOf = (r) => { const k = Object.keys(r).find((x) => x.startsWith('Value')); return k ? Number(r[k]) : 1; };
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const natural = (a, b) => a.localeCompare(b, undefined, { numeric: true });
const tierOf = (id) => { const m = /^(edge-fw|wan-core|ext-peer|spine|leaf|host)/.exec(id); return m ? m[1] : 'other'; };
const TIER_NAME = { 'edge-fw': 'Edge firewall', 'wan-core': 'WAN core', 'ext-peer': 'External peer',
  spine: 'Spine', leaf: 'Leaf', host: 'Host', other: 'Other' };
const TIER_SYMBOL = { 'edge-fw': 'triangle', 'wan-core': 'diamond', 'ext-peer': 'pin', spine: 'roundRect',
  leaf: 'rect', host: 'circle', other: 'circle' };
const VENDOR_COLOR = { arista: '#5794F2', cisco: '#73BF69', juniper: '#FF9830', nokia: '#B877D9' };
const DOWN = '#E02F44';

const dev = new Map();
rows('A').forEach((r) => { if (r.device_id) { dev.set(r.device_id, { vendor: r.vendor || 'unknown', os: r.os_version || '',
  site: r.site || '', up: valOf(r) >= 1 }); } });
const adjMap = new Map();
const edges = [];
rows('B').forEach((r) => {
  if (!r.src_device || !r.dst_device) { return; }
  const e = { s: r.src_device, t: r.dst_device, sp: r.src_port, dp: r.dst_port, proto: r.discovery_proto,
    kind: r.link_kind, dir: r.direction, up: valOf(r) >= 1 };
  edges.push(e);
  [[e.s, e.t], [e.t, e.s]].forEach(([a, b]) => { if (!adjMap.has(a)) { adjMap.set(a, new Set()); } adjMap.get(a).add(b); });
});

if (!adjMap.has(centre) && !dev.has(centre)) {
  return { title: { text: 'No adjacencies reported for ' + centre + ' in the last hour', left: 'center', top: 'middle',
    textStyle: { color: mutedColor, fontSize: 14 } } };
}

// Breadth-first search to two hops. Each hop-2 node is drawn behind one hop-1 parent: nodes with a
// single possible parent are placed first, the rest go to the least loaded parent so groups stay even.
const hop = new Map([[centre, 0]]);
const hop1 = Array.from(adjMap.get(centre) || []).sort(natural);
hop1.forEach((n) => { hop.set(n, 1); });
const cands = new Map();
hop1.forEach((n) => {
  Array.from(adjMap.get(n) || []).forEach((m) => {
    if (hop.has(m) && hop.get(m) < 2) { return; }
    hop.set(m, 2);
    if (!cands.has(m)) { cands.set(m, []); }
    cands.get(m).push(n);
  });
});
const load = new Map(hop1.map((n) => [n, 0]));
const parent = new Map();
Array.from(cands.keys()).sort((a, b) => cands.get(a).length - cands.get(b).length || natural(a, b)).forEach((m) => {
  const best = cands.get(m).slice().sort((a, b) => load.get(a) - load.get(b) || natural(a, b))[0];
  parent.set(m, best); load.set(best, load.get(best) + 1);
});

// Concentric layout: hop 1 on an inner ring, hop 2 fanned out behind its parent.
const R1 = 230; const R2 = 430;
const pos = new Map([[centre, { x: 0, y: 0 }]]);
const angle = new Map();
const tierRank = { 'edge-fw': 0, 'wan-core': 1, 'ext-peer': 2, spine: 3, leaf: 4, host: 5, other: 6 };
const ring1 = hop1.slice().sort((a, b) => tierRank[tierOf(a)] - tierRank[tierOf(b)] || natural(a, b));
const totalKids = Math.max(1, Array.from(load.values()).reduce((x, y) => x + y, 0));
// Each hop-1 node gets an angular share proportional to (1 + its children).
const weight = (n) => 1 + load.get(n) * Math.max(1, ring1.length / totalKids);
const wsum = ring1.reduce((acc, n) => acc + weight(n), 0) || 1;
let cursor = -Math.PI / 2;
const sector = new Map();
ring1.forEach((n) => {
  const span = (2 * Math.PI * weight(n)) / wsum;
  const a = ring1.length === 1 ? -Math.PI / 2 : cursor + span / 2;
  angle.set(n, a); sector.set(n, Math.min(span, (200 * Math.PI) / 180));
  pos.set(n, { x: R1 * Math.cos(a), y: R1 * Math.sin(a) });
  cursor += span;
});
const kids = new Map(ring1.map((n) => [n, []]));
Array.from(parent.keys()).sort((a, b) => tierRank[tierOf(a)] - tierRank[tierOf(b)] || natural(a, b))
  .forEach((m) => kids.get(parent.get(m)).push(m));
ring1.forEach((n) => {
  const k = kids.get(n); const sp = sector.get(n) * 0.9;
  k.forEach((m, j) => {
    const a = angle.get(n) + (k.length === 1 ? 0 : (j / (k.length - 1) - 0.5) * sp);
    pos.set(m, { x: R2 * Math.cos(a), y: R2 * Math.sin(a) });
  });
});
const ring2 = Array.from(parent.keys());

const crowded2 = ring2.length > 36;
const crowded1 = ring1.length > 30;
const nodes = Array.from(hop.keys()).map((id) => {
  const meta = dev.get(id) || { vendor: 'unknown', os: '', site: '', up: false, unknown: true };
  const h = hop.get(id);
  const p = pos.get(id);
  const t = tierOf(id);
  const down = !meta.unknown && !meta.up;
  const size = h === 0 ? 46 : (h === 1 ? 20 : 11);
  const a = Math.atan2(p.y, p.x);
  return {
    id, name: id, x: p.x, y: p.y, symbol: TIER_SYMBOL[t], symbolSize: size,
    itemStyle: { color: meta.unknown ? '#9FA7B3' : (VENDOR_COLOR[meta.vendor] || '#9FA7B3'),
      borderColor: down ? DOWN : (h === 0 ? textColor : theme.colors.background.primary),
      borderWidth: down ? 4 : (h === 0 ? 3 : 1), opacity: down ? 0.55 : 1 },
    label: { show: h === 0 || (h === 1 && !crowded1) || (h === 2 && !crowded2), color: down ? DOWN : textColor,
      fontSize: h === 0 ? 15 : (h === 1 ? 11 : 10), fontWeight: h === 0 ? 'bold' : 'normal',
      position: h === 0 ? 'bottom' : (Math.cos(a) >= 0 ? 'right' : 'left'), distance: 4 },
    meta: { id, hop: h, tier: t, down, unknown: !!meta.unknown, ...meta },
  };
});
// Invisible corner anchors keep the selected device in the middle of the panel whatever the shape.
[[-1, -1], [1, -1], [-1, 1], [1, 1]].forEach(([sx, sy], i) => {
  nodes.push({ id: '__anchor' + i, name: '', x: sx * (R2 + 20), y: sy * (R2 + 20), symbolSize: 0,
    itemStyle: { opacity: 0 }, label: { show: false }, tooltip: { show: false }, emphasis: { disabled: true }, meta: null });
});
const links0 = edges.filter((e) => hop.has(e.s) && hop.has(e.t)).length;
const links = edges.filter((e) => hop.has(e.s) && hop.has(e.t) && !(hop.get(e.s) === 2 && hop.get(e.t) === 2))
  .map((e) => {
    const first = e.s === centre || e.t === centre;
    // Tree edges (to the hop-1 node a hop-2 node is drawn behind) stay visible; mesh cross-links fade.
    const tree = first || parent.get(e.s) === e.t || parent.get(e.t) === e.s;
    return { source: e.s, target: e.t, e,
      lineStyle: { color: e.up ? (first ? theme.colors.primary.main : mutedColor) : DOWN,
        width: first ? 2.4 : (tree ? 1 : 0.6), type: e.up ? (e.proto === 'bgp' || e.proto === 'mpls_te' ? 'dashed' : 'solid') : 'dotted',
        opacity: first ? 0.9 : (tree ? 0.5 : (links0 > 60 ? 0.07 : 0.25)), curveness: 0 } };
  });

// Key: vendor fill, unreachable outline, lost link.
const key = [];
let kx = 0;
Object.keys(VENDOR_COLOR).filter((v) => nodes.some((n) => n.meta && n.meta.vendor === v)).forEach((v) => {
  key.push({ type: 'circle', shape: { cx: kx + 6, cy: 6, r: 6 }, style: { fill: VENDOR_COLOR[v] } });
  key.push({ type: 'text', x: kx + 16, y: 0, style: { text: v, fill: textColor, fontSize: 12 } });
  kx += 16 + v.length * 7 + 16;
});
key.push({ type: 'circle', shape: { cx: kx + 6, cy: 6, r: 5 }, style: { fill: 'transparent', stroke: DOWN, lineWidth: 3 } });
key.push({ type: 'text', x: kx + 16, y: 0, style: { text: 'unreachable', fill: textColor, fontSize: 12 } });
kx += 16 + 11 * 7 + 16;
key.push({ type: 'line', shape: { x1: kx, y1: 6, x2: kx + 24, y2: 6 }, style: { stroke: DOWN, lineWidth: 2, lineDash: [2, 3] } });
key.push({ type: 'text', x: kx + 30, y: 0, style: { text: 'link lost', fill: textColor, fontSize: 12 } });

const nDown = nodes.filter((n) => n.meta && n.meta.down).length;
const summary = ring1.length + ' direct neighbours, ' + ring2.length + ' at two hops'
  + (nDown ? ', ' + nDown + ' unreachable' : '') + '  -  click a node to investigate it';

context.panel.chart.off('click');
context.panel.chart.on('click', (pr) => {
  if (pr.dataType === 'node' && pr.data && pr.data.id && pr.data.id !== centre && pr.data.meta && !pr.data.meta.unknown) {
    context.grafana.locationService.partial({ 'var-device': pr.data.id }, false);
  }
});

return {
  backgroundColor: 'transparent',
  title: { text: summary, left: 8, top: 4, textStyle: { color: mutedColor, fontSize: 12, fontWeight: 'normal' } },
  graphic: [{ type: 'group', right: 12, top: 6, children: key }],
  tooltip: {
    trigger: 'item', confine: true,
    formatter: (pr) => {
      if (pr.dataType === 'edge') {
        const e = pr.data.e;
        const port = (x) => (x && x !== 'null' ? ' : ' + esc(x) : '');
        return '<b>' + esc(e.s) + port(e.sp) + '</b> &rarr; <b>' + esc(e.t) + port(e.dp) + '</b><br/>protocol: '
          + esc(e.proto) + '<br/>link kind: ' + esc(e.kind) + '<br/>direction: ' + esc(e.dir)
          + '<br/>state: ' + (e.up ? 'up' : '<span style="color:' + DOWN + '">lost</span>');
      }
      const m = pr.data.meta;
      if (!m) { return ''; }
      return '<b>' + esc(m.id) + '</b> (' + (m.hop === 0 ? 'selected' : m.hop + ' hop' + (m.hop > 1 ? 's' : '')) + ')<br/>tier: '
        + esc(TIER_NAME[m.tier]) + (m.unknown ? '<br/>not in this exporter\'s inventory' : '<br/>vendor: ' + esc(m.vendor)
        + '<br/>OS: ' + esc(m.os) + '<br/>site: ' + esc(m.site)) + '<br/>state: '
        + (m.unknown ? 'unknown' : (m.down ? '<span style="color:' + DOWN + '">unreachable</span>' : 'reachable'));
    },
  },
  animation: false,
  series: [{
    type: 'graph', layout: 'none', roam: true, draggable: false, nodeScaleRatio: 0,
    top: 40, bottom: 20, left: 90, right: 90,
    data: nodes, links, edgeSymbol: ['none', 'none'],
    labelLayout: { hideOverlap: true },
    emphasis: { focus: 'adjacency', label: { show: true }, lineStyle: { width: 3, opacity: 1 } },
    blur: { itemStyle: { opacity: 0.15 }, lineStyle: { opacity: 0.05 } },
  }],
};
"""

add({
    "type": "volkovlabs-echarts-panel",
    "title": "Ego network - $device and two hops out",
    "description": "The selected device at the centre, its direct neighbours on the inner ring and their "
                   "neighbours on the outer ring, from network_topology_edge_info (links reported in the last hour) "
                   "and network_topology_device_info. Fill = vendor, shape = tier, red outline = device seen in the "
                   "last hour but missing from the current poll, red dotted line = link no longer reported. "
                   "Click any node to re-centre this dashboard on it.",
    "targets": [q(DEV_STATE_EX, "A"), q(EDGE_STATE_EX, "B")],
    "options": {"renderer": "canvas", "map": "none", "themeEditor": {"name": "default", "config": "{}"},
                "editorMode": "code", "editor": {"format": "auto"}, "getOption": EGO_JS.strip()},
}, 0, 14, 13, 21)

add({
    "type": "table", "title": "Adjacencies",
    "description": "Every link in network_topology_edge_info where the device is the source (outbound) or the "
                   "destination (inbound) during the last hour, seen from the device's side. Lost = reported earlier "
                   "in the hour but missing from the current poll. Click a neighbour to investigate it.",
    "targets": [q(ADJ_STATE, "A")],
    "transformations": [
        {"id": "organize", "options": {
            "excludeByName": {"Time": True},
            "indexByName": {"Value": 0, "local_port": 1, "neighbour": 2, "remote_port": 3, "discovery_proto": 4,
                            "link_kind": 5, "direction": 6, "side": 7},
            "renameByName": {"Value": "State", "local_port": "Local port", "neighbour": "Neighbour",
                             "remote_port": "Remote port", "discovery_proto": "Protocol", "link_kind": "Link kind",
                             "direction": "Direction", "side": "Seen as"}}},
        {"id": "sortBy", "options": {"sort": [{"field": "Neighbour", "desc": False}]}},
    ],
    "fieldConfig": {"defaults": {"custom": {"align": "auto", "cellOptions": {"type": "auto"}, "filterable": False},
                                 "noValue": "-"},
                    "overrides": [
        {"matcher": {"id": "byName", "options": "State"}, "properties": [
            {"id": "mappings", "value": LINK_MAP}, {"id": "custom.width", "value": 70},
            {"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "basic"}}]},
        {"matcher": {"id": "byName", "options": "Neighbour"}, "properties": [
            {"id": "links", "value": [{"title": "Investigate ${__value.raw}", "url": SELF_LINK}]}]},
        {"matcher": {"id": "byName", "options": "Protocol"}, "properties": [{"id": "custom.width", "value": 80}]},
        {"matcher": {"id": "byName", "options": "Link kind"}, "properties": [{"id": "custom.width", "value": 80}]},
        {"matcher": {"id": "byName", "options": "Seen as"}, "properties": [{"id": "custom.width", "value": 80}]},
    ]},
    "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
}, 13, 14, 11, 11)

add({
    "type": "status-history", "title": "Link presence per adjacency",
    "description": "One row per adjacency (local port -> neighbour : remote port, protocol). Up while "
                   "network_topology_edge_info reports the link, Lost when the exporter is polling but the link is "
                   "missing (tracked for an hour after it was last seen). Gaps mean the exporter was not reporting.",
    "targets": [q(f"{ADJ_STATE} and on() ({EXPORTER_ALIVE} > 0)", "A", fmt="time_series", instant=False,
                  legend="{{neighbour}} - {{local_port}} ({{discovery_proto}})")],
    "fieldConfig": {"defaults": {"mappings": LINK_MAP, "color": {"mode": "thresholds"},
                                 "thresholds": {"mode": "absolute", "steps": [{"color": "red", "value": None},
                                                                              {"color": "green", "value": 1}]},
                                 "custom": {"fillOpacity": 80, "lineWidth": 0}, "min": 0, "max": 1},
                    "overrides": []},
    "options": {"showValue": "never", "rowHeight": 0.8, "colWidth": 0.95,
                "legend": {"showLegend": False}, "tooltip": {"mode": "single", "sort": "none"}},
    "interval": "1m",
}, 13, 25, 11, 10)

# ---------------------------------------------------------------- change and conflict events
row("Change and conflict events involving $device", 35)
LSEL = '{source="network-topology-exporter", instance="$exporter"} | src_device="$device" or dst_device="$device"'
add({
    "type": "timeseries", "title": "Events over time",
    "description": "Topology change (added / removed / changed) and conflict events from the exporter log stream "
                   "where the device is src_device or dst_device (structured metadata), counted per interval.",
    "datasource": LOKI,
    "targets": [q(f'sum by (change_kind, conflict_type) (count_over_time({LSEL} [$__auto]))', "A", ds=LOKI,
                  legend="{{change_kind}}{{conflict_type}}")],
    "interval": "2m",
    "fieldConfig": {"defaults": {"unit": "none", "decimals": 0, "color": {"mode": "palette-classic"},
                                 "noValue": "No change or conflict events involve this device in the selected range",
                                 "min": 0,
                                 "custom": {"drawStyle": "bars", "fillOpacity": 80, "lineWidth": 1,
                                            "stacking": {"mode": "normal", "group": "A"}, "showPoints": "never"}},
                    "overrides": [
        {"matcher": {"id": "byName", "options": "added"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#73BF69"}}]},
        {"matcher": {"id": "byName", "options": "removed"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#F2495C"}}]},
        {"matcher": {"id": "byName", "options": "changed"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#5794F2"}}]},
        {"matcher": {"id": "byName", "options": "neighbour_disagreement"}, "properties": [
            {"id": "color", "value": {"mode": "fixed", "fixedColor": "#FF9830"}},
            {"id": "displayName", "value": "conflict: neighbour disagreement"}]},
    ]},
    "options": {"legend": {"showLegend": True, "displayMode": "list", "placement": "bottom", "calcs": ["sum"]},
                "tooltip": {"mode": "multi", "sort": "desc"}},
}, 0, 36, 9, 10)

add({
    "type": "logs", "title": "Change and conflict log - $device",
    "description": "Raw topology_change and topology_conflict lines from {source=\"network-topology-exporter\"} for "
                   "the selected exporter, filtered on the src_device / dst_device structured metadata.",
    "datasource": LOKI,
    "targets": [q(LSEL, "A", ds=LOKI)],
    "options": {"showTime": True, "showLabels": False, "showCommonLabels": False, "wrapLogMessage": True,
                "prettifyLogMessage": False, "enableLogDetails": True, "dedupStrategy": "none",
                "sortOrder": "Descending", "enableInfiniteScrolling": False},
}, 9, 36, 15, 10)

# ---------------------------------------------------------------- exporter context
row("Polling context - is it the device or the poller? ($exporter)", 46)
STATUS_MAP = [{"type": "value", "options": {
    "0": {"text": "OK", "color": "green", "index": 0},
    "1": {"text": "Degraded", "color": "#FF9830", "index": 1},
    "2": {"text": "Hard-failed", "color": "red", "index": 2}}}]
add({
    "type": "state-timeline", "title": "Discovery module status",
    "description": "network_topology_module_last_status per discovery module of the exporter that polls this "
                   "device: 0 OK, 1 Degraded, 2 Hard-failed. A failing module explains missing links better than "
                   "a device fault does.",
    "targets": [q(f"max by (module) (network_topology_module_last_status{{{EX}}})", "A", fmt="time_series",
                  instant=False, legend="{{module}}")],
    "fieldConfig": {"defaults": {"mappings": STATUS_MAP, "color": {"mode": "thresholds"},
                                 "thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                                              {"color": "#FF9830", "value": 1},
                                                                              {"color": "red", "value": 2}]},
                                 "custom": {"fillOpacity": 80, "lineWidth": 0}},
                    "overrides": []},
    "options": {"showValue": "never", "rowHeight": 0.8, "mergeValues": True, "alignValue": "left",
                "legend": {"showLegend": False}, "tooltip": {"mode": "single", "sort": "none"}},
    "interval": "1m",
}, 0, 47, 8, 9)

add({
    "type": "timeseries", "title": "Discovery cycle duration p95",
    "description": "95th percentile of network_topology_discovery_cycle_duration_seconds (whole cycle) and the "
                   "slowest module from network_topology_discovery_module_duration_seconds, over 10 minute windows.",
    "targets": [
        q(f"histogram_quantile(0.95, sum by (le) (rate(network_topology_discovery_cycle_duration_seconds_bucket{{{EX}}}[10m])))",
          "A", fmt="time_series", instant=False, legend="Full cycle p95"),
        q(f"max(histogram_quantile(0.95, sum by (le, module) (rate(network_topology_discovery_module_duration_seconds_bucket{{{EX}}}[10m]))))",
          "B", fmt="time_series", instant=False, legend="Slowest module p95"),
    ],
    "fieldConfig": {"defaults": {"unit": "s", "decimals": 1, "color": {"mode": "palette-classic"},
                                 "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 10,
                                            "gradientMode": "opacity", "showPoints": "never", "spanNulls": False}},
                    "overrides": [
        {"matcher": {"id": "byName", "options": "Full cycle p95"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#5794F2"}}]},
        {"matcher": {"id": "byName", "options": "Slowest module p95"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#B877D9"}}]},
    ]},
    "options": {"legend": {"showLegend": True, "displayMode": "list", "placement": "bottom"},
                "tooltip": {"mode": "multi", "sort": "desc"}},
}, 8, 47, 8, 9)

add({
    "type": "timeseries", "title": "SNMP walk and credential outcomes",
    "description": "Per-minute rate of network_topology_snmp_walks_total by status and "
                   "network_topology_credential_trials_total by status for the exporter. Rising timeouts or failed "
                   "credential trials point at the poller or the access path, not the device graph.",
    "targets": [
        q(f"sum by (status) (rate(network_topology_snmp_walks_total{{{EX}}}[$__rate_interval])) * 60", "A",
          fmt="time_series", instant=False, legend="walk {{status}}"),
        q(f"sum by (status) (rate(network_topology_credential_trials_total{{{EX}}}[$__rate_interval])) * 60", "B",
          fmt="time_series", instant=False, legend="credential {{status}}"),
    ],
    "fieldConfig": {"defaults": {"unit": "none", "decimals": 1, "color": {"mode": "palette-classic"},
                                 "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 0,
                                            "showPoints": "never", "scaleDistribution": {"type": "log", "log": 10},
                                            "axisLabel": "per minute"}},
                    "overrides": [
        {"matcher": {"id": "byName", "options": "walk ok"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#73BF69"}}]},
        {"matcher": {"id": "byName", "options": "walk timeout"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#FF9830"}}]},
        {"matcher": {"id": "byName", "options": "credential ok"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#5794F2"}}]},
        {"matcher": {"id": "byName", "options": "credential failed"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#F2495C"}}]},
    ]},
    "options": {"legend": {"showLegend": True, "displayMode": "list", "placement": "bottom"},
                "tooltip": {"mode": "multi", "sort": "desc"}},
}, 16, 47, 8, 9)

BOUND = (f'max by (reporting_device, src_port, peer_a, peer_b, proto) ('
         f'network_topology_boundary_observation_info{{{EX},reporting_device="$device"}}'
         f' or network_topology_boundary_observation_info{{{EX},peer_a="$device"}}'
         f' or network_topology_boundary_observation_info{{{EX},peer_b="$device"}})')
add({
    "type": "table", "title": "Federation boundary observations involving $device",
    "description": "network_topology_boundary_observation_info rows on the federation hub where the device is the "
                   "reporting_device or one of the peers: links that cross out of this exporter's discovery scope. "
                   "Only the hub reports these.",
    "targets": [q(BOUND, "A")],
    "transformations": [{"id": "organize", "options": {
        "excludeByName": {"Time": True, "Value": True},
        "indexByName": {"reporting_device": 0, "src_port": 1, "peer_a": 2, "peer_b": 3, "proto": 4},
        "renameByName": {"reporting_device": "Reporting device", "src_port": "Port", "peer_a": "Peer A",
                         "peer_b": "Peer B", "proto": "Protocol"}}}],
    "fieldConfig": {"defaults": {"custom": {"align": "auto", "cellOptions": {"type": "auto"}},
                                 "noValue": "No boundary observations involve this device"}, "overrides": []},
    "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
}, 0, 56, 10, 6)

add({
    "type": "stat", "title": "Exporter at a glance",
    "description": "For the exporter polling this device: devices and links in its current graph "
                   "(network_topology_graph_devices_total, network_topology_graph_edges_total), devices seen in the "
                   "last hour but missing now, and discovery modules not OK (network_topology_module_last_status > 0).",
    "targets": [
        q(f"max(network_topology_graph_devices_total{{{EX}}})", "A", fmt="time_series", instant=False, legend="Devices"),
        q(f"max(network_topology_graph_edges_total{{{EX}}})", "B", fmt="time_series", instant=False, legend="Links"),
        q(f"count(max by (device_id) (last_over_time(network_topology_device_info{{{EX}}}[1h])) unless on (device_id) "
          f"max_over_time(network_topology_device_info{{{EX}}}[{NOW}])) or vector(0)", "C", fmt="time_series", instant=False,
          legend="Unreachable now"),
        q(f"count(network_topology_module_last_status{{{EX}}} > 0) or vector(0)", "D", fmt="time_series",
          instant=False, legend="Modules not OK"),
    ],
    "fieldConfig": {"defaults": {"unit": "none", "decimals": 0, "color": {"mode": "fixed", "fixedColor": "#5794F2"}},
                    "overrides": [
        {"matcher": {"id": "byName", "options": "Links"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#8F3BB8"}}]},
        {"matcher": {"id": "byRegexp", "options": "Unreachable now|Modules not OK"}, "properties": [
            {"id": "color", "value": {"mode": "thresholds"}},
            {"id": "thresholds", "value": {"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                                         {"color": "red", "value": 1}]}}]},
    ]},
    "options": {"colorMode": "background", "graphMode": "area", "justifyMode": "center", "textMode": "value_and_name",
                "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                "orientation": "horizontal", "wideLayout": True, "showPercentChange": False},
}, 10, 56, 14, 6)

# ---------------------------------------------------------------- find a device
row("Find a device (all exporters)", 62)
FIND_L = "instance, device_id, vendor, os_version, site"
FIND = (f"label_replace((max by ({FIND_L}) (max_over_time(network_topology_device_info[{NOW}]))"
        f" or (0 * max by ({FIND_L}) (last_over_time(network_topology_device_info[1h])))),"
        f' "tier", "$1", "device_id", "{TIER_RE}")')
add({
    "type": "table", "title": "Device finder",
    "description": "Every device reported by any discovery exporter in the last hour, with its current state "
                   "(Reachable = in the current poll, Unreachable = seen in the last hour but missing now). Filter a "
                   "column header to search; click a device to investigate it.",
    "targets": [q(FIND, "A")],
    "transformations": [
        {"id": "organize", "options": {
            "excludeByName": {"Time": True},
            "indexByName": {"device_id": 0, "Value": 1, "tier": 2, "vendor": 3, "os_version": 4, "site": 5, "instance": 6},
            "renameByName": {"device_id": "Device", "Value": "State", "tier": "Tier", "vendor": "Vendor",
                             "os_version": "OS version", "site": "Site", "instance": "Exporter"}}},
        {"id": "sortBy", "options": {"sort": [{"field": "State", "desc": False}]}},
    ],
    "fieldConfig": {"defaults": {"custom": {"align": "auto", "cellOptions": {"type": "auto"}, "filterable": True}},
                    "overrides": [
        {"matcher": {"id": "byName", "options": "Device"}, "properties": [
            {"id": "links", "value": [{"title": "Investigate ${__value.raw}",
                                       "url": "/d/" + UID + "/?var-exporter=${__data.fields.Exporter}"
                                              "&var-device=${__value.raw}&${__url_time_range}"}]}]},
        {"matcher": {"id": "byName", "options": "State"}, "properties": [
            {"id": "mappings", "value": PRESENCE_MAP}, {"id": "custom.width", "value": 120},
            {"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "basic"}}]},
        {"matcher": {"id": "byName", "options": "Vendor"}, "properties": [
            {"id": "custom.cellOptions", "value": {"type": "color-text"}}, {"id": "mappings", "value": VENDOR_MAP}]},
    ]},
    "options": {"showHeader": True, "cellHeight": "sm", "footer": {"show": True, "reducer": ["count"], "fields": ["Device"]}},
}, 0, 63, 24, 12)


# ---------------------------------------------------------------- dashboard
dash = {
    "apiVersion": "dashboard.grafana.app/v1beta1",
    "kind": "Dashboard",
    "metadata": {"name": UID, "annotations": {"grafana.app/folder": "netobs-showcase"}},
    "spec": {
        "title": "Network Observability - Device Investigation",
        "description": "Troubleshoot one device anywhere on the global network: identity and state, reachability "
                       "history, adjacencies and ego network, change and conflict events, and the health of the "
                       "discovery exporter that polls it.",
        "uid": UID,
        "tags": ["netobs-showcase", "device", "troubleshooting"],
        "editable": True, "graphTooltip": 1, "schemaVersion": 41, "refresh": "1m",
        "time": {"from": "now-1h", "to": "now"}, "timezone": "browser",
        "links": [
            {"type": "dashboards", "tags": ["netobs-showcase"], "asDropdown": True, "title": "Network Observability",
             "includeVars": False, "keepTime": True, "targetBlank": False, "icon": "external link", "tooltip": "", "url": ""},
            {"type": "link", "title": "Topology Explorer for this exporter", "url": "/d/nos-topology-explorer/",
             "includeVars": False, "keepTime": True, "targetBlank": False, "icon": "dashboard", "tooltip": "",
             "tags": [], "asDropdown": False},
            {"type": "link", "title": "Change and discovery health", "url": "/d/nos-change-and-discovery/",
             "includeVars": False, "keepTime": True, "targetBlank": False, "icon": "dashboard", "tooltip": "",
             "tags": [], "asDropdown": False},
        ],
        "templating": {"list": [
            {"type": "query", "name": "exporter", "label": "Exporter", "datasource": PROM,
             "query": {"qryType": 1, "query": "label_values(network_topology_graph_devices_total, instance)",
                       "refId": "PrometheusVariableQueryEditor-VariableQuery"},
             "definition": "label_values(network_topology_graph_devices_total, instance)",
             "refresh": 2, "sort": 1, "multi": False, "includeAll": False, "hide": 0, "options": [],
             "current": {"text": "netobs-glb-hub:9100", "value": "netobs-glb-hub:9100"}},
            {"type": "query", "name": "device", "label": "Device", "datasource": PROM,
             "description": "Devices in the current poll plus any seen in the last hour, so an unreachable device "
                            "can still be selected.",
             "query": {"qryType": 3,
                       "query": 'query_result(max by (device_id) (last_over_time(network_topology_device_info{instance="$exporter"}[1h])))',
                       "refId": "PrometheusVariableQueryEditor-VariableQuery"},
             "definition": 'query_result(max by (device_id) (last_over_time(network_topology_device_info{instance="$exporter"}[1h])))',
             "regex": '/device_id="([^"]+)"/',
             "refresh": 2, "sort": 7, "multi": False, "includeAll": False, "hide": 0, "options": [],
             "current": {"text": "spine-01", "value": "spine-01"}},
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
