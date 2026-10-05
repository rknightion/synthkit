#!/usr/bin/env python3
"""Build nos-site-investigation.json: WAN site investigation (one site of the global WAN vs the rest)."""
import os
import json

UID = "nos-site-investigation"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), UID + ".json")
PROM = {"type": "prometheus", "uid": "grafanacloud-prom"}
LOKI = {"type": "loki", "uid": "grafanacloud-logs"}
MIXED = {"type": "datasource", "uid": "-- Mixed --"}
HUB = "netobs-glb-hub:9100"

# ---------------------------------------------------------------- site model
# A site is either a device_info `site` value (discovery inventory) or a hub federation `spoke_id`.
# Where the two describe the same place, they alias each other. The only configured alias today is
# the EMEA edge discovery exporter, which pushes to the hub under spoke identity dc-emea.
SPOKE_TO_DEVSITE = {"dc-emea": "emea-edge1"}
DEVSITE_TO_SPOKE = {v: k for k, v in SPOKE_TO_DEVSITE.items()}
# Spoke-side exporter (the exporter that does the pushing), for spoke-side push counters.
SPOKE_EXPORTER = {"dc-emea": "netobs-spoke-emea:9100", "emea-edge1": "netobs-spoke-emea:9100"}
# Probe region per site. Sites not matched (the global core) are compared against every region.
REGION_RULES = [
    ("EMEA", "emea-.*|dc-emea|dc-edge-.*"),
    ("US", "hq-dc1|amer-.*|dc-amer"),
    ("APAC", "apac-.*|dc-apac"),
]
ALL_REGIONS = "EMEA|US|APAC"
STALE_BUDGET = 300  # seconds; same staleness budget as the WAN and federation maps

INFO = "network_topology_device_info"
EDGE = "network_topology_edge_info"
UP = "network_topology_federation_spoke_up"
PUSH = "network_topology_federation_spoke_last_push_timestamp_seconds"


def R(s, **kw):
    """Tiny templating: replace @KEY@ tokens so PromQL braces stay literal."""
    for k, v in kw.items():
        s = s.replace("@" + k + "@", v)
    return s


DS = '{site="$dsite"}'
SEEN = R('last_over_time(@I@@S@[1h])', I=INFO, S=DS)                       # devices seen in last hour
NOW = INFO + DS
SRCSET = R('max by (instance, src_device) (label_replace(@SEEN@, "src_device", "$1", "device_id", "(.*)"))', SEEN=SEEN)
DSTSET = R('max by (instance, dst_device) (label_replace(@SEEN@, "dst_device", "$1", "device_id", "(.*)"))', SEEN=SEEN)
REPSET = R('max by (instance, reporting_device) (label_replace(@SEEN@, "reporting_device", "$1", "device_id", "(.*)"))', SEEN=SEEN)


def touch(e):
    """Edges with at least one end at the site (deduplicated by `or`)."""
    return R('((@E@) and on (instance, src_device) @SRC@) or ((@E@) and on (instance, dst_device) @DST@)',
             E=e, SRC=SRCSET, DST=DSTSET)


TOUCH_NOW = touch(EDGE)
TOUCH_SEEN = touch("last_over_time(" + EDGE + "[1h])")
UNREACH = R('max by (instance, device_id) (@SEEN@) unless on (instance, device_id) @NOW@', SEEN=SEEN, NOW=NOW)


def tier(e):
    return R('label_replace(@E@, "tier", "$1", "device_id", "(edge-fw|wan-core|spine|leaf|host|ext-peer).*")', E=e)


# ---------------------------------------------------------------- helpers
_id = [0]


def nid():
    _id[0] += 1
    return _id[0]


def pq(expr, ref="A", legend=None, fmt=None, instant=False, interval=None):
    t = {"refId": ref, "datasource": PROM, "expr": expr, "editorMode": "code", "range": not instant, "instant": instant}
    if legend is not None:
        t["legendFormat"] = legend
    if fmt:
        t["format"] = fmt
    if interval:
        t["interval"] = interval
    return t


def lq(expr, ref="A", legend=None, instant=False, interval=None):
    t = {"refId": ref, "datasource": LOKI, "expr": expr, "editorMode": "code", "queryType": "instant" if instant else "range"}
    if legend is not None:
        t["legendFormat"] = legend
    if interval:
        t["interval"] = interval
    return t


def thr(*steps):
    return {"mode": "absolute", "steps": [{"color": c, "value": v} for c, v in steps]}


def panel(typ, title, x, y, w, h, targets, desc, ds=PROM, fc=None, opts=None, tr=None):
    p = {"id": nid(), "type": typ, "title": title, "description": desc, "gridPos": {"x": x, "y": y, "w": w, "h": h},
         "datasource": ds, "targets": targets,
         "fieldConfig": fc or {"defaults": {}, "overrides": []}, "options": opts or {}}
    if tr:
        p["transformations"] = tr
    return p


def row(title, y):
    return {"id": nid(), "type": "row", "title": title, "collapsed": False, "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": []}


panels = []

# ================================================================ 1. summary card + site model text
CARD_JS = r"""
const S = context.panelData.series || [];
const frames = (ref) => S.filter((f) => f.refId === ref);
const rows = (ref) => {
  const out = [];
  frames(ref).forEach((f) => {
    const n = f.length || (f.fields[0] ? f.fields[0].values.length : 0);
    const num = f.fields.find((x) => x.type === 'number');
    for (let i = 0; i < n; i++) {
      const r = Object.assign({}, num && num.labels ? num.labels : {});
      f.fields.forEach((x) => { if (x.type === 'string') r[x.name] = x.values[i]; });
      r.__v = num ? num.values[i] : null;
      out.push(r);
    }
  });
  return out;
};
const one = (ref) => { const r = rows(ref); return r.length && r[0].__v !== null && r[0].__v !== undefined ? Number(r[0].__v) : null; };
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
const rv = (v) => context.grafana.replaceVariables(v);
const site = rv('$site'), dsite = rv('$dsite'), fspoke = rv('$fspoke'), region = rv('$region');
const now = one('A'), seen = one('B'), links = one('C');
const vendors = rows('D').sort((a, b) => b.__v - a.__v);
const oses = rows('E').sort((a, b) => b.__v - a.__v);
const exps = rows('F').map((r) => r.instance);
const up = one('G'), age = one('H'), downMin = one('I'), pushFail = one('J'), stale = one('K');
const rej = one('L'), unreach = rows('M').map((r) => r.device_id).sort();
const modsBad = one('N'), probe = one('O');
const hasDev = seen !== null && seen > 0;
const hasFed = up !== null;
const fmtAge = (s) => s === null ? '-' : (s < 120 ? Math.round(s) + ' s' : (s / 60).toFixed(1) + ' min');
let state = 'HEALTHY', cls = 'ok', why = [];
if (!hasDev && !hasFed) { state = 'NO DATA'; cls = 'idle'; why.push('no discovery inventory or federation series for this site'); }
if (hasDev && now !== null && now < seen) { state = 'DEGRADED'; cls = 'warn'; why.push((seen - now) + ' of ' + seen + ' devices unreachable'); }
if (hasDev && now === null) { state = 'DOWN'; cls = 'bad'; why.push('no device is reporting'); }
if (hasFed && up === 0) { state = 'SPOKE DOWN'; cls = 'bad'; why.push('hub marks spoke ' + fspoke + ' down'); }
if (stale === 1) { why.push('discovery graph stale'); if (cls === 'ok') { state = 'STALE'; cls = 'warn'; } }
if (cls === 'ok') why.push(hasDev ? 'all devices seen in the last hour are reporting' : 'spoke pushing within its liveness window');
const cov = hasDev && hasFed ? 'Discovery inventory + federation spoke ' + esc(fspoke)
  : hasDev ? 'Discovery inventory only (not a federation spoke)'
  : hasFed ? 'Federation signals only - no device inventory reaches this stack' : 'Unknown site';
const kpi = (v, l, c) => '<div class="kpi ' + (c || '') + '"><b>' + v + '</b><i>' + l + '</i></div>';
const chips = (arr, f) => arr.length ? arr.map((r) => '<span class="chip">' + f(r) + '</span>').join('') : '<span class="muted">none</span>';
let h = '<div class="card ' + cls + '">';
h += '<div class="hdr"><div><div class="name">' + esc(site) + '</div><div class="cov">' + cov + '</div></div>';
h += '<div class="pill">' + state + '</div></div>';
h += '<div class="why">' + esc(why.join(' - ')) + '</div>';
h += '<div class="grid">';
h += kpi(hasDev ? (now === null ? 0 : now) + '<small>/' + seen + '</small>' : '-', 'devices reporting / seen 1h', hasDev && now !== seen ? 'warn' : '');
h += kpi(hasDev ? (links === null ? 0 : links) : '-', 'links touching site');
h += kpi(hasDev ? vendors.length : '-', 'vendors');
h += kpi(hasDev ? oses.length : '-', 'OS versions');
h += kpi(hasFed ? (up === 1 ? 'UP' : 'DOWN') : '-', 'spoke liveness', hasFed ? (up === 1 ? 'good' : 'bad') : '');
h += kpi(hasFed ? fmtAge(age) : '-', 'last push age', hasFed && age !== null && age >= __BUDGET__ ? 'bad' : (hasFed && age !== null && age >= 120 ? 'warn' : ''));
h += kpi(hasFed ? Math.round(downMin || 0) + ' min' : '-', 'spoke down in range', hasFed && downMin > 0 ? 'warn' : '');
h += kpi(probe === null ? '-' : probe.toFixed(1) + '%', 'probe success (' + esc(region.split('|').join('/')) + ')', probe !== null && probe < 99 ? 'warn' : '');
h += '</div><div class="lists">';
h += '<div><span class="lbl">Exporters</span>' + (exps.length ? exps.map((e) => '<span class="chip">' + esc(e) + ' (discovery)</span>').join('') : '') +
     (hasFed ? '<span class="chip fed">hub ' + esc('__HUB__') + ' (federation)</span>' : '') + '</div>';
h += '<div><span class="lbl">Vendors</span>' + chips(vendors, (r) => esc(r.vendor) + ' <em>' + r.__v + '</em>') + '</div>';
h += '<div><span class="lbl">OS versions</span>' + chips(oses, (r) => esc(r.vendor) + ' ' + esc(r.os_version) + ' <em>' + r.__v + '</em>') + '</div>';
h += '<div><span class="lbl">Unreachable now</span>' + (unreach.length ? unreach.map((d) => '<span class="chip red">' + esc(d) + '</span>').join('') : '<span class="muted">' + (hasDev ? 'none' : 'n/a') + '</span>') + '</div>';
h += '<div><span class="lbl">Federation</span>' + (hasFed
  ? '<span class="chip">spoke-side push failures in range <em>' + (pushFail === null ? 'n/a (no spoke-side exporter on this stack)' : Math.round(pushFail)) + '</em></span>'
    + '<span class="chip">hub rejected updates in range, all spokes <em>' + Math.round(rej || 0) + '</em></span>'
  : '<span class="muted">not a federation spoke</span>') + '</div>';
h += '<div><span class="lbl">Discovery</span>' + (hasDev
  ? '<span class="chip ' + (stale === 1 ? 'red' : '') + '">graph ' + (stale === 1 ? 'STALE' : 'fresh') + '</span><span class="chip ' + (modsBad > 0 ? 'red' : '') + '">modules not OK <em>' + (modsBad || 0) + '</em></span>'
  : '<span class="muted">n/a</span>') + '</div>';
h += '</div></div>';
context.handlebars.registerHelper('siteCard', () => new context.handlebars.SafeString(h));
""".replace("__BUDGET__", str(STALE_BUDGET)).replace("__HUB__", HUB)

CARD_CSS = """
.card{height:100%;box-sizing:border-box;border-radius:8px;padding:12px 16px;border:1px solid rgba(128,128,128,.35);border-left:8px solid #73BF69;background:rgba(115,191,105,.06)}
.card.warn{border-left-color:#FF9830;background:rgba(255,152,48,.07)}
.card.bad{border-left-color:#F2495C;background:rgba(242,73,92,.08)}
.card.idle{border-left-color:#8e8e8e}
.hdr{display:flex;justify-content:space-between;align-items:flex-start}
.name{font-size:26px;font-weight:700;letter-spacing:.3px;line-height:1.1}
.cov{font-size:13px;opacity:.75;margin-top:2px}
.pill{font-size:14px;font-weight:700;padding:4px 12px;border-radius:14px;background:#73BF69;color:#0b0c0e;white-space:nowrap}
.card.warn .pill{background:#FF9830}.card.bad .pill{background:#F2495C;color:#fff}.card.idle .pill{background:#8e8e8e}
.why{font-size:13px;margin:6px 0 10px 0;opacity:.85}
.grid{display:grid;grid-template-columns:repeat(4,1fr);gap:10px 8px;margin-bottom:10px}
.kpi{display:flex;flex-direction:column;border-radius:6px;padding:6px 10px;background:rgba(128,128,128,.10)}
.kpi b{font-size:24px;line-height:1.15;font-weight:700}.kpi b small{font-size:15px;opacity:.65;font-weight:600}
.kpi i{font-size:11px;font-style:normal;opacity:.72;text-transform:uppercase;letter-spacing:.4px}
.kpi.warn b{color:#FF9830}.kpi.bad b{color:#F2495C}.kpi.good b{color:#56A64B}
.lists>div{margin:3px 0;font-size:13px;line-height:1.9}
.lbl{display:inline-block;width:132px;font-size:11px;text-transform:uppercase;letter-spacing:.4px;opacity:.7}
.chip{display:inline-block;padding:0 8px;margin:0 4px 2px 0;border-radius:10px;border:1px solid rgba(128,128,128,.45);font-size:12px;line-height:20px}
.chip em{font-style:normal;font-weight:700;margin-left:3px}
.chip.red{border-color:#F2495C;color:#F2495C;font-weight:600}.chip.fed{border-style:dashed}
.muted{opacity:.6;font-size:12px}
"""

FS = '{spoke_id="$fspoke"}'
card_t = [
    pq('vector(1)', "SITE", fmt="table", instant=True),  # always non-empty so the card renders for every site
    pq('count(' + NOW + ')', "A", fmt="table", instant=True),
    pq('count(' + SEEN + ')', "B", fmt="table", instant=True),
    pq('count(' + TOUCH_NOW + ')', "C", fmt="table", instant=True),
    pq('count by (vendor) (' + SEEN + ')', "D", fmt="table", instant=True),
    pq('count by (vendor, os_version) (' + SEEN + ')', "E", fmt="table", instant=True),
    pq('count by (instance) (' + SEEN + ')', "F", fmt="table", instant=True),
    pq('max(' + UP + FS + ')', "G", fmt="table", instant=True),
    pq('time() - max(' + PUSH + FS + ')', "H", fmt="table", instant=True),
    pq('sum_over_time((max(' + UP + FS + ') == bool 0)[$__range:1m])', "I", fmt="table", instant=True),
    pq('sum(increase(network_topology_federation_spoke_push_failures_total{instance="$sexp"}[$__range]))', "J", fmt="table", instant=True),
    pq('max(network_topology_graph_stale{instance=~"$dexp"})', "K", fmt="table", instant=True),
    pq('sum(increase(network_topology_graph_updates_rejected_total{instance="' + HUB + '"}[$__range])) and on () max(' + UP + FS + ')',
       "L", fmt="table", instant=True),
    pq(UNREACH, "M", fmt="table", instant=True),
    pq('count(network_topology_module_last_status{instance=~"$dexp"} > 0) or (max(network_topology_graph_stale{instance=~"$dexp"}) * 0)',
       "N", fmt="table", instant=True),
    pq('100 * avg(avg_over_time(probe_success[$__range]) * on (instance, job, probe) group_left (region) '
       'max by (instance, job, probe, region) (sm_check_info{region=~"$region"}))', "O", fmt="table", instant=True),
]
panels.append(panel(
    "marcusolsson-dynamictext-panel", "Site summary - $site", 0, 0, 15, 14, card_t,
    "State of the selected site now. Devices reporting: network_topology_device_info now vs seen in the last hour; "
    "links: network_topology_edge_info adjacencies with at least one end at the site; vendors and OS versions from device_info labels; "
    "exporter: the discovery exporter (instance) reporting the site's devices. Federation (spokes only): network_topology_federation_spoke_up, "
    "push age from network_topology_federation_spoke_last_push_timestamp_seconds, spoke-side network_topology_federation_spoke_push_failures_total "
    "where the pushing exporter reports to this stack, and hub-wide network_topology_graph_updates_rejected_total (that counter has no spoke label). "
    "Probe success: probe_success averaged over the range for the probe region mapped to this site.",
    ds=PROM,
    opts={"renderMode": "data", "content": "{{siteCard}}", "defaultContent": "<p>No discovery or federation data for this site in the selected range.</p>",
          "styles": CARD_CSS, "helpers": CARD_JS, "afterRender": "", "externalStyles": [], "contentPartials": [], "wrap": False,
          "editors": ["default", "styles", "helpers"], "editor": {"language": "html", "format": "auto"}, "status": ""}))

MAP_MD = """A site is a discovery site (`site` on `network_topology_device_info`) or a federation spoke (`spoke_id` on the hub's `network_topology_federation_spoke_*`). The picker lists both.

| Site | Devices from | Federation | Probes |
|---|---|---|---|
| hq-dc1 | netobs-ent-dc1 | none | US |
| global | netobs-glb-hub (Clos core) | the hub | all |
| emea-dc1 / amer-dc1 | netobs-glb-hub (WAN core) | none | EMEA / US |
| emea-edge1 = dc-emea | netobs-spoke-emea | pushes as dc-emea | EMEA |
| dc-amer, dc-apac | none | hub view only | US, APAC |
| dc-edge-1, dc-edge-2 | none | hub view only | EMEA |

**dc-emea is the only spoke with devices**: the emea-edge1 exporter pushes under that identity, so both names show the same 9 devices plus dc-emea liveness. **dc-amer, dc-apac, dc-edge-1 and dc-edge-2 are federation-only**: their exporters do not report here, so device, link and change panels stay empty and only hub liveness and push age exist. amer-dc1 is not assumed to be dc-amer.

Probe region follows site location (hq-dc1 Chicago, dc-edge-1 Dublin, dc-edge-2 Amsterdam); the global core is compared against all regions. Push age is judged against a 300 s staleness budget.
"""
panels.append(panel("text", "Site model and mappings", 15, 0, 9, 14, [],
                    "How discovery sites, federation spokes and probe regions are tied together on this dashboard.",
                    ds=None, opts={"mode": "markdown", "content": MAP_MD, "code": {"language": "plaintext", "showLineNumbers": False, "showMiniMap": False}}))
panels[-1].pop("datasource")
panels[-1].pop("targets")

# ================================================================ 2. reachability over time
Y = 14
panels.append(row("Reachability over time", Y))
Y += 1
panels.append(panel(
    "timeseries", "Devices present vs seen in the last hour", 0, Y, 9, 8,
    [pq('count(' + NOW + ')', "A", "Reporting now", interval="1m"),
     pq('count(' + SEEN + ')', "B", "Seen in last hour", interval="1m"),
     pq('count(' + SEEN + ') - (count(' + NOW + ') or count(' + SEEN + ') * 0)', "C", "Unreachable", interval="1m")],
    "Per minute: devices of the site reporting network_topology_device_info, devices seen in the trailing hour (last_over_time 1h), "
    "and the gap between them as unreachable bars. A device that stops answering SNMP drops out of device_info while it stays in the hourly view. "
    "Federation-only spokes have no device inventory and show nothing here.",
    fc={"defaults": {"unit": "short", "decimals": 0, "min": 0, "noValue": "No device inventory for this site",
                     "color": {"mode": "palette-classic"},
                     "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 0, "showPoints": "never", "spanNulls": False,
                                "axisSoftMin": 0}},
        "overrides": [
            {"matcher": {"id": "byName", "options": "Reporting now"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#56A64B"}}, {"id": "custom.fillOpacity", "value": 12}]},
            {"matcher": {"id": "byName", "options": "Seen in last hour"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#8AB8FF"}}, {"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [8, 6]}}]},
            {"matcher": {"id": "byName", "options": "Unreachable"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#F2495C"}}, {"id": "custom.drawStyle", "value": "bars"}, {"id": "custom.fillOpacity", "value": 80}]},
        ]},
    opts={"legend": {"showLegend": True, "displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi", "sort": "none"}}))

panels.append(panel(
    "state-timeline", "Site liveness timeline", 9, Y, 8, 8,
    [pq('(count(' + NOW + ') or count(' + SEEN + ') * 0) / count(' + SEEN + ') == bool 1', "A", "Devices all reporting", interval="1m"),
     pq('max(' + UP + FS + ')', "B", "Spoke $fspoke (hub view)", interval="1m"),
     pq('max(network_topology_graph_stale{instance=~"$dexp"}) == bool 0', "C", "Discovery graph fresh", interval="1m")],
    "Green when healthy, red when not. Devices all reporting: every device seen in the last hour is in network_topology_device_info. "
    "Spoke: network_topology_federation_spoke_up on the hub for the site's spoke identity. Discovery graph fresh: network_topology_graph_stale = 0 "
    "on the exporter covering the site. Rows appear only where the site has that signal.",
    fc={"defaults": {"color": {"mode": "thresholds"}, "thresholds": thr(("#F2495C", None), ("#56A64B", 1)),
                     "mappings": [{"type": "value", "options": {"0": {"text": "DOWN", "index": 0}, "1": {"text": "OK", "index": 1}}}],
                     "custom": {"fillOpacity": 85, "lineWidth": 0}, "noValue": "No liveness signals"}, "overrides": []},
    opts={"showValue": "never", "mergeValues": True, "alignValue": "center", "rowHeight": 0.8,
          "legend": {"showLegend": False, "displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single", "sort": "none"}}))

panels.append(panel(
    "timeseries", "Federation push age vs staleness budget", 17, Y, 7, 8,
    [pq('time() - max(' + PUSH + FS + ')', "A", "Hub: last push from $fspoke", interval="1m"),
     pq('time() - max(network_topology_federation_spoke_push_last_success_unix{instance="$sexp"})', "B", "Spoke side: last successful push", interval="1m")],
    "Seconds since the hub last accepted a push from the site's spoke (network_topology_federation_spoke_last_push_timestamp_seconds) and, "
    "where the pushing exporter reports to this stack, the spoke's own last successful push (network_topology_federation_spoke_push_last_success_unix). "
    "The red line and band mark the 300 s staleness budget. Empty for sites that are not federation spokes.",
    fc={"defaults": {"unit": "s", "min": 0, "decimals": 0, "noValue": "Not a federation spoke",
                     "color": {"mode": "palette-classic"},
                     "thresholds": thr(("transparent", None), ("#F2495C", STALE_BUDGET)),
                     "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 10, "showPoints": "never",
                                "thresholdsStyle": {"mode": "line+area"}, "axisSoftMin": 0, "axisSoftMax": STALE_BUDGET + 60}},
        "overrides": [
            {"matcher": {"id": "byFrameRefID", "options": "A"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#5794F2"}}]},
            {"matcher": {"id": "byFrameRefID", "options": "B"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#B877D9"}}, {"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [6, 4]}}]},
        ]},
    opts={"legend": {"showLegend": True, "displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi", "sort": "none"}}))
Y += 8

# ================================================================ 3. devices + topology
panels.append(row("Devices and topology at the site", Y))
Y += 1
LBLS = "instance, device_id, tier, vendor, os_version"
DEV_INFO = tier('max by (instance, device_id, vendor, os_version) (' + SEEN + ')')
dev_t = [
    pq('max by (' + LBLS + ') (last_over_time(network_topology_device_uptime_seconds[1h]) * on (instance, device_id) group_left (tier, vendor, os_version) ' + DEV_INFO + ')',
       "A", fmt="table", instant=True),
    pq('max by (' + LBLS + ') ((' + DEV_INFO + ' * 0) + on (instance, device_id) group_left () (max by (instance, device_id) (' + NOW + ') or max by (instance, device_id) (' + SEEN + ') * 0))',
       "B", fmt="table", instant=True),
]
DEVICE_LINK = "/d/nos-device-investigation?var-exporter=${__data.fields.instance}&var-device=${__data.fields.device_id}&${__url_time_range}"
panels.append(panel(
    "table", "Devices at the site", 0, Y, 10, 13, dev_t,
    "One row per device seen at the site in the last hour (network_topology_device_info). Tier comes from the device naming convention "
    "(edge-fw, wan-core, spine, leaf, host). Uptime is the last reported network_topology_device_uptime_seconds; state is Reachable while "
    "device_info is present and Unreachable when it was seen in the last hour but has stopped reporting. Click a device to open its investigation view.",
    fc={"defaults": {"noValue": "No device inventory - federation-only site", "custom": {"align": "auto", "cellOptions": {"type": "auto"}, "filterable": True}},
        "overrides": [
            {"matcher": {"id": "byName", "options": "device_id"}, "properties": [
                {"id": "displayName", "value": "Device"}, {"id": "custom.width", "value": 145},
                {"id": "links", "value": [{"title": "Investigate ${__data.fields.device_id}", "url": DEVICE_LINK}]}]},
            {"matcher": {"id": "byName", "options": "instance"}, "properties": [{"id": "displayName", "value": "Exporter"}, {"id": "custom.hidden", "value": True}]},
            {"matcher": {"id": "byName", "options": "tier"}, "properties": [{"id": "displayName", "value": "Tier"}, {"id": "custom.width", "value": 80}]},
            {"matcher": {"id": "byName", "options": "vendor"}, "properties": [{"id": "displayName", "value": "Vendor"}, {"id": "custom.width", "value": 80}]},
            {"matcher": {"id": "byName", "options": "os_version"}, "properties": [{"id": "displayName", "value": "OS"}, {"id": "custom.width", "value": 85}]},
            {"matcher": {"id": "byName", "options": "Uptime"}, "properties": [
                {"id": "unit", "value": "s"}, {"id": "decimals", "value": 1},
                {"id": "custom.cellOptions", "value": {"type": "color-text"}},
                {"id": "color", "value": {"mode": "thresholds"}},
                {"id": "thresholds", "value": thr(("#FF9830", None), ("#FADE2A", 3600), ("text", 86400))}]},
            {"matcher": {"id": "byName", "options": "State"}, "properties": [
                {"id": "custom.width", "value": 112},
                {"id": "mappings", "value": [{"type": "value", "options": {"0": {"text": "Unreachable", "color": "#F2495C", "index": 0},
                                                                         "1": {"text": "Reachable", "color": "#56A64B", "index": 1}}}]},
                {"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "basic"}}]},
        ]},
    opts={"showHeader": True, "cellHeight": "sm", "footer": {"show": False, "reducer": ["sum"], "fields": ""},
          "sortBy": [{"displayName": "State", "desc": False}]},
    tr=[{"id": "merge", "options": {}},
        {"id": "organize", "options": {"excludeByName": {"Time": True},
                                       "indexByName": {"device_id": 0, "tier": 1, "vendor": 2, "os_version": 3, "Value #A": 4, "Value #B": 5, "instance": 6},
                                       "renameByName": {"Value #A": "Uptime", "Value #B": "State"}}}]))

# --- nodeGraph: site devices (1 reachable / 0 unreachable), neighbours at other sites (2),
#     external boundary peers seen by the hub (3), federation spoke and hub (spoke_up / 2).
def nodes_union():
    site_now = R('max by (instance, device_id, vendor, os_version, site) (@N@)', N=NOW)
    site_seen = R('max by (instance, device_id, vendor, os_version, site) (@S@) * 0', S=SEEN)
    info_all = 'max by (instance, device_id, vendor, os_version, site) (last_over_time(' + INFO + '[1h]))'
    nd = R('max by (instance, device_id) (label_replace(max by (instance, dst_device) (@T@) unless on (instance, dst_device) @D@, "device_id", "$1", "dst_device", "(.*)"))',
           T=TOUCH_SEEN, D=DSTSET)
    ns = R('max by (instance, device_id) (label_replace(max by (instance, src_device) (@T@) unless on (instance, src_device) @S@, "device_id", "$1", "src_device", "(.*)"))',
           T=TOUCH_SEEN, S=SRCSET)
    neigh = R('(max by (instance, device_id, vendor, os_version, site) ((@ND@ or @NS@) * on (instance, device_id) group_left (vendor, os_version, site) @IA@) * 0 + 2)',
              ND=nd, NS=ns, IA=info_all)
    peers = R('(max by (instance, device_id) (label_replace(max by (instance, peer_a) (network_topology_boundary_observation_info and on (instance, reporting_device) @REP@), "device_id", "$1", "peer_a", "(.*)")) * 0 + 3)',
              REP=REPSET)
    peers = R('label_replace(label_replace(@P@, "vendor", "external peer", "", ""), "site", "outside the estate", "", "")', P=peers)
    nodev = 'unless on () (count(' + SEEN + ') > 0)'
    spoke = R('label_replace(label_replace(label_replace(max by (instance, spoke_id) (@UP@@FS@), "device_id", "$1", "spoke_id", "(.*)"), "vendor", "federation spoke", "", ""), "site", "$fspoke", "", "")',
              UP=UP, FS=FS)
    hub = R('label_replace(label_replace(label_replace(max by (instance) (@UP@@FS@) * 0 + 2, "device_id", "global-hub", "", ""), "vendor", "federation hub", "", ""), "site", "global", "", "")',
            UP=UP, FS=FS)
    fed = '((' + spoke + ') or (' + hub + ')) ' + nodev
    u = '(' + site_now + ') or (' + site_seen + ') or ' + neigh + ' or (' + peers + ') or (' + fed + ')'
    u = tier(u)
    for dst, src in (("id", "device_id"), ("title", "device_id"), ("subtitle", "vendor"), ("mainstat", "tier"),
                     ("detail__site", "site"), ("detail__os_version", "os_version"), ("detail__exporter", "instance")):
        u = R('label_join(@U@, "@D@", "", "@S@")', U=u, D=dst, S=src)
    return u


def edges_union():
    present = TOUCH_NOW
    lost = R('((@T@) unless on (instance, src_device, src_port, dst_device, discovery_proto) @E@) * 0', T=TOUCH_SEEN, E=EDGE)
    e = '(' + present + ') or (' + lost + ')'
    e = R('label_join(label_join(label_join(label_join(max by (instance, src_device, src_port, dst_device, discovery_proto) (@E@), '
          '"id", "/", "src_device", "src_port", "dst_device", "discovery_proto"), "source", "", "src_device"), "target", "", "dst_device"), "mainstat", "", "discovery_proto")', E=e)
    b = R('label_join(label_join(label_join(label_join(max by (instance, reporting_device, src_port, peer_a, proto) (network_topology_boundary_observation_info and on (instance, reporting_device) @REP@) * 0 + 3, '
          '"id", "/", "reporting_device", "src_port", "peer_a"), "source", "", "reporting_device"), "target", "", "peer_a"), "mainstat", "", "proto")', REP=REPSET)
    nodev = 'unless on () (count(' + SEEN + ') > 0)'
    f = R('label_replace(label_replace(label_replace(label_replace(max by (spoke_id) (@UP@@FS@), "id", "push-$1", "spoke_id", "(.*)"), "source", "$1", "spoke_id", "(.*)"), "target", "global-hub", "", ""), "mainstat", "federation push", "", "")',
          UP=UP, FS=FS)
    return '(' + e + ') or (' + b + ') or ((' + f + ') ' + nodev + ')'


TOPO_JS = r"""
const series = context.panel.data.series || [];
const theme = context.grafana.theme;
const text = theme.colors.text.primary, muted = theme.colors.text.secondary;
const rows = (ref) => {
  const out = [];
  series.filter((f) => f.refId === ref).forEach((f) => {
    const n = f.fields.length ? f.fields[0].values.length : 0;
    for (let i = 0; i < n; i++) {
      const r = {};
      f.fields.forEach((x) => { r[x.name] = x.values[i]; });
      const num = f.fields.find((x) => x.type === 'number');
      r.__v = num ? Number(num.values[i]) : null;
      out.push(r);
    }
  });
  return out;
};
const C = { ok: '#56A64B', down: '#F2495C', nbr: '#5794F2', peer: '#B877D9' };
const CATS = [
  { name: 'Reporting', itemStyle: { color: C.ok } },
  { name: 'Unreachable (seen in last hour)', itemStyle: { color: C.down } },
  { name: 'Neighbour at another site', itemStyle: { color: C.nbr } },
  { name: 'External peer (hub boundary)', itemStyle: { color: C.peer } },
];
const SYM = { 'edge-fw': 'triangle', 'wan-core': 'diamond', spine: 'roundRect', leaf: 'rect', host: 'circle', federation: 'roundRect', hub: 'diamond', 'ext-peer': 'circle' };
const SIZE = { 'edge-fw': 30, 'wan-core': 30, spine: 26, leaf: 20, host: 11, federation: 30, hub: 34, 'ext-peer': 16 };
const nrows = rows('A');
const erows = rows('B');
const nodes = {};
nrows.forEach((r) => {
  const v = r.__v;
  let cat = v >= 3 ? 3 : v >= 2 ? 2 : v >= 1 ? 0 : 1;
  const fed = r.subtitle === 'federation spoke' || r.subtitle === 'federation hub';
  if (fed && r.subtitle === 'federation hub') cat = 2;
  const outside = cat >= 2;
  const tier = r.subtitle === 'federation hub' ? 'hub' : (r.tier || (cat === 3 ? 'ext-peer' : (fed ? 'federation' : 'other')));
  nodes[r.id] = { id: r.id, name: r.id, tier, cat, outside, vendor: r.subtitle || '', os: r.detail__os_version || '', site: r.detail__site || '' };
});
const layerOf = (n) => n.outside ? 0 : ({ 'edge-fw': 1, 'wan-core': 1, federation: 1, spine: 2, leaf: 3, host: 4 }[n.tier] ?? 4);
const layers = {};
Object.values(nodes).sort((a, b) => a.id.localeCompare(b.id, undefined, { numeric: true })).forEach((n) => {
  const l = layerOf(n); (layers[l] = layers[l] || []).push(n);
});
const W = 1000; let y = 0; const placed = [];
const hostCount = (layers[4] || []).length;
Object.keys(layers).map(Number).sort((a, b) => a - b).forEach((l) => {
  const arr = layers[l];
  const per = l === 4 ? Math.max(12, Math.ceil(arr.length / Math.ceil(arr.length / 48))) : 40;
  for (let i = 0; i < arr.length; i += per) {
    const chunk = arr.slice(i, i + per);
    chunk.forEach((n, j) => { n.x = (j + 0.5) * W / chunk.length; n.y = y; n.dense = chunk.length > 12; placed.push(n); });
    y += l === 4 ? 70 : 140;
  }
});
const showHostLabels = hostCount <= 48;
const maxY = Math.max(140, y - (hostCount ? 70 : 140));
const ch = context.panel.chart;
const PW = Math.max(300, ((ch && ch.getWidth) ? ch.getWidth() : 1100) - 120), PH = Math.max(150, ((ch && ch.getHeight) ? ch.getHeight() : 380) - 110);
placed.forEach((n) => { n.x = n.x / W * PW; n.y = n.y / maxY * PH; });
const data = placed.map((n) => ({
  id: n.id, name: n.id, x: n.x, y: n.y, category: n.cat,
  symbol: SYM[n.tier] || (n.cat === 3 ? 'pin' : 'circle'),
  symbolSize: SIZE[n.tier] || (n.cat === 3 ? 22 : 24),
  value: n.vendor,
  label: { show: n.tier !== 'host' || showHostLabels, position: n.tier === 'host' || n.dense ? 'bottom' : 'right', color: n.tier === 'host' ? muted : text,
    fontSize: n.tier === 'host' || n.dense ? 10 : 12,
    formatter: n.tier === 'host' ? n.id.replace(/^host-leaf\d+-/, '') : (n.tier === 'hub' ? 'global hub (federation)' : n.tier === 'federation' ? n.id + ' (spoke)' : n.dense ? n.id.replace(/^(ext-peer|leaf|spine)-/, (m, p) => ({ 'ext-peer': 'peer ', leaf: 'L', spine: 'S' }[p])) : n.id) },
  tooltip: { formatter: '<b>' + n.id + '</b><br/>' + [n.tier, n.vendor, n.os, n.site].filter(Boolean).join(' - ') + '<br/>' + CATS[n.cat].name },
}));
data.push({ id: '__a0', name: '', x: 0, y: 0, symbolSize: 0, label: { show: false }, tooltip: { show: false }, silent: true });
data.push({ id: '__a1', name: '', x: PW, y: PH, symbolSize: 0, label: { show: false }, tooltip: { show: false }, silent: true });
const links = [];
erows.forEach((r) => {
  if (!nodes[r.source] || !nodes[r.target]) return;
  const v = r.__v;
  const fed = r.mainstat === 'federation push';
  const col = fed ? (v >= 1 ? C.ok : C.down) : v >= 3 ? C.peer : v >= 1 ? muted : C.down;
  links.push({ source: r.source, target: r.target,
    lineStyle: { color: col, width: v >= 1 && v < 3 && !fed ? 1.2 : 2.2, opacity: v >= 1 && v < 3 && !fed ? 0.55 : 0.95, type: v >= 3 || fed ? 'dashed' : 'solid', curveness: 0.05 },
    tooltip: { formatter: r.source + ' - ' + r.target + '<br/>' + (r.mainstat || '') + (v === 0 ? ' - LOST in the last hour' : '') } });
});
if (data.length <= 2) {
  return { title: { text: 'No discovery inventory or federation path for this site', left: 'center', top: 'middle', textStyle: { color: muted, fontSize: 15, fontWeight: 'normal' } } };
}
return {
  backgroundColor: 'transparent',
  tooltip: { trigger: 'item', confine: true },
  legend: [{ data: CATS.map((c) => c.name), top: 0, left: 'center', textStyle: { color: text }, icon: 'circle' }],
  animation: false,
  series: [{
    type: 'graph', layout: 'none', roam: true, nodeScaleRatio: 0, top: 70, bottom: 40, left: 40, right: 80,
    categories: CATS, data, links, edgeSymbol: ['none', 'none'],
    emphasis: { focus: 'adjacency', lineStyle: { width: 3 } },
    lineStyle: { opacity: 0.6 },
  }],
};
"""
panels.append({
    "id": nid(), "type": "volkovlabs-echarts-panel", "title": "Site topology and links leaving the site",
    "description": "Devices at the site laid out by tier (edge and WAN core, spine, leaf, host) with every discovered adjacency touching them "
                   "(network_topology_edge_info). The top row holds what is outside the site: neighbours at another site (blue, links leaving the site) "
                   "and external peers the hub sees at the domain boundary (purple, network_topology_boundary_observation_info). Green: reporting; red: seen "
                   "in the last hour but no longer reporting. Grey links are present, red links were lost within the last hour, dashed purple links are "
                   "boundary observations. For a federation-only spoke the diagram shows the spoke's push path to the hub, coloured by "
                   "network_topology_federation_spoke_up. Scroll to zoom, drag to pan, hover for details.",
    "gridPos": {"x": 10, "y": Y, "w": 14, "h": 13},
    "datasource": PROM,
    "targets": [pq(nodes_union(), "A", fmt="table", instant=True), pq(edges_union(), "B", fmt="table", instant=True)],
    "fieldConfig": {"defaults": {}, "overrides": []},
    "options": {"renderer": "canvas", "map": "none", "themeEditor": {"name": "default", "config": "{}"},
                "editorMode": "code", "editor": {"format": "auto"}, "getOption": TOPO_JS.strip()},
})
Y += 13

# ================================================================ 4. changes
panels.append(row("Changes, conflicts and rejected updates", Y))
Y += 1
LSEL = '{source="network-topology-exporter", instance=~"$dexp"} | src_device=~"$devs" or dst_device=~"$devs"'
panels.append(panel(
    "timeseries", "Topology changes and conflicts touching the site", 0, Y, 9, 8,
    [lq('sum by (change_kind) (count_over_time(' + LSEL.replace("{source", '{change_kind=~".+", source') + ' [$__interval]))', "A", "{{change_kind}}", interval="2m"),
     lq('sum by (conflict_type) (count_over_time(' + LSEL.replace("{source", '{conflict_type=~".+", source') + ' [$__interval]))', "B", "conflict: {{conflict_type}}", interval="2m")],
    "Topology change log lines (event=topology_change, by change_kind) and neighbour conflict lines (event=topology_conflict) from the discovery exporter "
    "covering the site, filtered to lines where src_device or dst_device is one of the site's devices. Counts per 2 minutes.",
    ds=LOKI,
    fc={"defaults": {"unit": "short", "decimals": 0, "min": 0, "noValue": "No change events for this site", "color": {"mode": "palette-classic"},
                     "custom": {"drawStyle": "bars", "fillOpacity": 70, "lineWidth": 1, "stacking": {"mode": "normal", "group": "A"}, "showPoints": "never"}},
        "overrides": [
            {"matcher": {"id": "byName", "options": "added"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#56A64B"}}]},
            {"matcher": {"id": "byName", "options": "removed"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#F2495C"}}]},
            {"matcher": {"id": "byName", "options": "changed"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#FADE2A"}}]},
            {"matcher": {"id": "byRegexp", "options": "conflict.*"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#FF9830"}}]},
        ]},
    opts={"legend": {"showLegend": True, "displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi", "sort": "desc"}}))

panels.append(panel(
    "timeseries", "Exporter change counters and hub rejections", 9, Y, 7, 8,
    [pq('sum by (change_kind) (increase(network_topology_change_total{instance=~"$dexp"}[2m]))', "A", "exporter {{change_kind}}", interval="2m"),
     pq('sum by (reason) (increase(network_topology_graph_updates_rejected_total{instance="' + HUB + '"}[2m]) > 0) and on () max(' + UP + FS + ')',
        "B", "hub rejected: {{reason}}", interval="2m")],
    "network_topology_change_total on the discovery exporter covering the site (exporter-wide: the counter has no device label, so on the hub it also "
    "covers the rest of the global core). For federation spokes, network_topology_graph_updates_rejected_total on the hub by reason; that counter is "
    "hub-wide with no spoke label, so it shows rejections from all spokes. Increase per 2 minutes.",
    fc={"defaults": {"unit": "short", "decimals": 0, "min": 0, "noValue": "No counters for this site", "color": {"mode": "palette-classic"},
                     "custom": {"axisSoftMax": 4, "drawStyle": "line", "lineInterpolation": "stepAfter", "fillOpacity": 18, "lineWidth": 2, "showPoints": "never"}},
        "overrides": [{"matcher": {"id": "byFrameRefID", "options": "B"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#F2495C"}}, {"id": "custom.drawStyle", "value": "bars"}, {"id": "custom.fillOpacity", "value": 80}]}]},
    opts={"legend": {"showLegend": True, "displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi", "sort": "desc"}}))

panels.append(panel(
    "logs", "Change and conflict log for the site", 16, Y, 8, 8,
    [lq(LSEL + ' | logfmt | line_format "{{.event}}  {{if .change_kind}}{{.change_kind}} {{.proto}}  {{.src_device}} {{.src_port}} -> {{.dst_device}} {{.dst_port}}{{else}}{{.conflict_type}}  {{.src_device}} {{.src_port}}  sources={{.sources}}{{end}}"', "A")],
    "Raw discovery events from {source=\"network-topology-exporter\"} on the site's exporter where src_device or dst_device is a site device.",
    ds=LOKI,
    opts={"showTime": True, "showLabels": False, "showCommonLabels": False, "wrapLogMessage": False, "prettifyLogMessage": False,
          "enableLogDetails": True, "dedupStrategy": "none", "sortOrder": "Descending", "enableInfiniteScrolling": False}))
Y += 8

# ================================================================ 5. external experience
panels.append(row("External experience - probes in the region serving $site", Y))
Y += 1
SMJ = 'on (instance, job, probe) group_left (region) max by (instance, job, probe, region) (sm_check_info{region=~"$region"})'


def probe_lbl(e):
    return 'label_replace(' + e + ', "probe", "$1", "probe", "synthkit-(.*)")'


panels.append(panel(
    "stat", "Probe success in range", 0, Y, 6, 8,
    [pq(probe_lbl('100 * avg by (job, probe, region) (avg_over_time(probe_success[$__range]) * ' + SMJ + ')'), "A", "{{job}} ({{region}})", instant=True)],
    "Share of successful runs per check over the selected range: probe_success averaged over time, joined to sm_check_info for the probe region "
    "mapped to the site.",
    fc={"defaults": {"unit": "percent", "decimals": 1, "min": 0, "max": 100, "noValue": "No probe in this region",
                     "color": {"mode": "thresholds"}, "thresholds": thr(("#F2495C", None), ("#FF9830", 95), ("#56A64B", 99))}, "overrides": []},
    opts={"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}, "colorMode": "background", "graphMode": "none",
          "textMode": "value_and_name", "justifyMode": "center", "orientation": "horizontal", "wideLayout": True}))
panels.append(panel(
    "timeseries", "Probe latency by check", 6, Y, 11, 8,
    [pq('max by (job, region) (probe_duration_seconds * ' + SMJ + ')', "A", "{{job}} ({{region}} probe)", interval="1m"),
     pq(probe_lbl('histogram_quantile(0.95, sum by (le, region) (rate(probe_all_duration_seconds_bucket[5m]) * ' + SMJ + '))'), "B", "p95 all checks ({{region}})", interval="1m")],
    "probe_duration_seconds per check run from the region's probe, plus the p95 across those checks from the probe_all_duration_seconds histogram (5m rate).",
    fc={"defaults": {"unit": "s", "min": 0, "noValue": "No probe in this region", "color": {"mode": "palette-classic"},
                     "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 6, "showPoints": "never"}},
        "overrides": [{"matcher": {"id": "byFrameRefID", "options": "B"}, "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#F2495C"}}, {"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [8, 5]}}, {"id": "custom.fillOpacity", "value": 0}]}]},
    opts={"legend": {"showLegend": True, "displayMode": "table", "placement": "right", "calcs": ["mean", "max"]}, "tooltip": {"mode": "multi", "sort": "desc"}}))
panels.append(panel(
    "status-history", "Probe result per check", 17, Y, 7, 8,
    [pq(probe_lbl('min by (job) (probe_success * ' + SMJ + ')'), "A", "{{job}}", interval="2m")],
    "probe_success per check run from the region's probe, worst result in each 2 minute bucket: green pass, red fail.",
    fc={"defaults": {"color": {"mode": "thresholds"}, "thresholds": thr(("#F2495C", None), ("#56A64B", 1)),
                     "mappings": [{"type": "value", "options": {"0": {"text": "FAIL", "index": 0}, "1": {"text": "OK", "index": 1}}}],
                     "custom": {"fillOpacity": 85, "lineWidth": 1}, "noValue": "No probe in this region"}, "overrides": []},
    opts={"showValue": "never", "rowHeight": 0.85, "colWidth": 0.9, "legend": {"showLegend": False, "displayMode": "list", "placement": "bottom"},
          "tooltip": {"mode": "single", "sort": "none"}}))
Y += 8

# ================================================================ 6. comparison
panels.append(row("All sites compared", Y))
Y += 1
CMP_JS = r"""
const S = context.panelData.series || [];
const rows = (ref) => {
  const out = [];
  S.filter((f) => f.refId === ref).forEach((f) => {
    const num = f.fields.find((x) => x.type === 'number');
    const n = num ? num.values.length : 0;
    for (let i = 0; i < n; i++) {
      const r = Object.assign({}, num.labels || {});
      f.fields.forEach((x) => { if (x.type === 'string') r[x.name] = x.values[i]; });
      r.__v = Number(num.values[i]);
      out.push(r);
    }
  });
  return out;
};
const byKey = (ref, key) => { const m = {}; rows(ref).forEach((r) => { m[r[key]] = r.__v; }); return m; };
const SPOKE_TO_DEV = __S2D__, DEV_TO_SPOKE = __D2S__, RULES = __RULES__;
const regionOf = (s) => { for (const [r, p] of RULES) { if (new RegExp('^(' + p + ')$').test(s)) return r; } return 'ALL'; };
const devSeen = byKey('A', 'site'), devNow = byKey('B', 'site'), links = byKey('C', 'site'), avail = byKey('D', 'site');
const unr = byKey('E', 'site'), spLive = byKey('F', 'spoke_id'), spDown = byKey('G', 'spoke_id'), spUp = byKey('J', 'spoke_id');
const probeR = byKey('I', 'region');
const devSite = {}; rows('H').forEach((r) => { devSite[r.instance + '|' + r.device_id] = r.site; });
const chg = {};
['K', 'L'].forEach((ref) => rows(ref).forEach((r) => {
  const a = devSite[r.instance + '|' + r.src_device], b = devSite[r.instance + '|' + r.dst_device];
  const k = ref === 'K' ? 'c' : 'x';
  [a, b].filter((s, i, arr) => s && arr.indexOf(s) === i).forEach((s) => { chg[s] = chg[s] || { c: 0, x: 0 }; chg[s][k] += r.__v; });
}));
const sites = Array.from(new Set(Object.keys(devSeen).concat(Object.keys(spLive)))).sort();
const cur = context.grafana.replaceVariables('$site');
const tr = context.grafana.replaceVariables('${__url_time_range}');
const fmt = (v, d, suf) => (v === undefined || v === null || isNaN(v)) ? '<span class="na">-</span>' : v.toFixed(d) + (suf || '');
const cls = (v, warn, bad, low) => (v === undefined || isNaN(v)) ? '' : (low ? (v < bad ? 'bad' : v < warn ? 'warn' : 'good') : (v >= bad ? 'bad' : v >= warn ? 'warn' : ''));
let h = '<table class="cmp"><thead><tr><th>Site</th><th>Coverage</th><th>State now</th><th>Devices<br><small>now / seen 1h</small></th><th>Links</th>'
  + '<th>Device availability</th><th>Unreachable<br><small>device-site minutes</small></th><th>Spoke liveness</th><th>Spoke down<br><small>minutes</small></th>'
  + '<th>Changes</th><th>Conflicts</th><th>Probe region</th><th>Probe success</th></tr></thead><tbody>';
sites.forEach((s) => {
  const ds = SPOKE_TO_DEV[s] || s, sp = DEV_TO_SPOKE[s] || s;
  const seen = devSeen[ds], now = devNow[ds] || 0, hasDev = seen !== undefined, hasFed = spLive[sp] !== undefined;
  const reg = regionOf(s);
  const pr = reg === 'ALL' ? (Object.values(probeR).length ? Object.values(probeR).reduce((a, b) => a + b, 0) / Object.values(probeR).length : undefined) : probeR[reg];
  let st = 'OK', sc = 'good';
  if (hasDev && now < seen) { st = (seen - now) + ' unreachable'; sc = 'warn'; }
  if (hasDev && now === 0) { st = 'DOWN'; sc = 'bad'; }
  if (hasFed && spUp[sp] === 0) { st = 'SPOKE DOWN'; sc = 'bad'; }
  const cov = hasDev && hasFed ? 'discovery + spoke ' + sp : hasDev ? 'discovery' : 'federation only';
  const c = chg[ds] || {};
  h += '<tr class="' + (s === cur ? 'sel' : '') + '"><td><a href="/d/__UID__?var-site=' + encodeURIComponent(s) + '&' + tr + '">' + s + '</a></td>'
    + '<td class="cov ' + (hasDev ? '' : 'fo') + '">' + cov + '</td>'
    + '<td><span class="pill ' + sc + '">' + st + '</span></td>'
    + '<td>' + (hasDev ? now + ' / ' + seen : '<span class="na">-</span>') + '</td>'
    + '<td>' + (hasDev ? fmt(links[ds] || 0, 0) : '<span class="na">-</span>') + '</td>'
    + '<td class="' + cls(avail[ds], 99.5, 98, true) + '">' + fmt(avail[ds], 2, '%') + '</td>'
    + '<td class="' + cls(unr[ds], 1, 10) + '">' + (hasDev ? fmt(unr[ds] || 0, 0) : '<span class="na">-</span>') + '</td>'
    + '<td class="' + cls(spLive[sp], 99, 95, true) + '">' + fmt(spLive[sp], 1, '%') + '</td>'
    + '<td class="' + cls(spDown[sp], 1, 10) + '">' + (hasFed ? fmt(spDown[sp] || 0, 0) : '<span class="na">-</span>') + '</td>'
    + '<td>' + (hasDev ? fmt(c.c || 0, 0) : '<span class="na">-</span>') + '</td>'
    + '<td class="' + cls(c.x, 1, 20) + '">' + (hasDev ? fmt(c.x || 0, 0) : '<span class="na">-</span>') + '</td>'
    + '<td>' + (reg === 'ALL' ? 'all' : reg) + '</td>'
    + '<td class="' + cls(pr, 99, 95, true) + '">' + fmt(pr, 2, '%') + '</td></tr>';
});
h += '</tbody></table>';
context.handlebars.registerHelper('cmpTable', () => new context.handlebars.SafeString(h));
"""
CMP_JS = (CMP_JS.replace("__S2D__", json.dumps(SPOKE_TO_DEVSITE)).replace("__D2S__", json.dumps(DEVSITE_TO_SPOKE))
          .replace("__RULES__", json.dumps([[r, p] for r, p in REGION_RULES])).replace("__UID__", UID))
CMP_CSS = """
.cmp{width:100%;border-collapse:collapse;font-size:13px}
.cmp th{font-size:11px;text-transform:uppercase;letter-spacing:.4px;font-weight:600;opacity:.8;text-align:left;padding:6px 8px;border-bottom:2px solid rgba(128,128,128,.45);vertical-align:bottom}
.cmp th small{text-transform:none;font-weight:400;opacity:.8}
.cmp td{padding:5px 8px;border-bottom:1px solid rgba(128,128,128,.2);font-variant-numeric:tabular-nums}
.cmp tr.sel td{background:rgba(87,148,242,.14)}
.cmp tr.sel td:first-child{box-shadow:inset 4px 0 0 #5794F2}
.cmp a{font-weight:700;color:#5794F2;text-decoration:none}
.cmp a:hover{text-decoration:underline}
.cmp .cov{opacity:.85}.cmp .cov.fo{font-style:italic;opacity:.7}
.cmp .na{opacity:.45}
.cmp td.good{color:#56A64B;font-weight:600}.cmp td.warn{color:#FF9830;font-weight:600}.cmp td.bad{color:#F2495C;font-weight:700}
.pill{display:inline-block;font-size:11px;font-weight:700;padding:1px 9px;border-radius:10px;white-space:nowrap}
.pill.good{background:rgba(86,166,75,.2);color:#56A64B}.pill.warn{background:rgba(255,152,48,.2);color:#FF9830}.pill.bad{background:#F2495C;color:#fff}
"""
SITE_SRC = 'max by (instance, src_device, site) (label_replace(last_over_time(' + INFO + '[1h]), "src_device", "$1", "device_id", "(.*)"))'
SITE_DST = 'max by (instance, dst_device, site) (label_replace(last_over_time(' + INFO + '[1h]), "dst_device", "$1", "device_id", "(.*)"))'
EK = "instance, src_device, src_port, dst_device, discovery_proto"
CHG_SEL = '{source="network-topology-exporter", change_kind=~".+"}'
CON_SEL = '{source="network-topology-exporter", conflict_type=~".+"}'
cmp_t = [
    pq('count by (site) (last_over_time(' + INFO + '[1h]))', "A", fmt="table", instant=True),
    pq('count by (site) (' + INFO + ')', "B", fmt="table", instant=True),
    pq('count by (site) (max by (site, ' + EK + ') ((' + EDGE + ' * on (instance, src_device) group_left (site) ' + SITE_SRC + ') or (' + EDGE + ' * on (instance, dst_device) group_left (site) ' + SITE_DST + ')))',
       "C", fmt="table", instant=True),
    pq('100 * avg_over_time(((count by (site) (' + INFO + ') or count by (site) (last_over_time(' + INFO + '[1h])) * 0) / count by (site) (last_over_time(' + INFO + '[1h])))[$__range:1m])',
       "D", fmt="table", instant=True),
    pq('sum_over_time(((count by (site) (last_over_time(' + INFO + '[1h])) - (count by (site) (' + INFO + ') or count by (site) (last_over_time(' + INFO + '[1h])) * 0)) > bool 0)[$__range:1m])',
       "E", fmt="table", instant=True),
    pq('100 * max by (spoke_id) (avg_over_time(' + UP + '[$__range]))', "F", fmt="table", instant=True),
    pq('sum_over_time((max by (spoke_id) (' + UP + ') == bool 0)[$__range:1m])', "G", fmt="table", instant=True),
    pq('max by (instance, device_id, site) (last_over_time(' + INFO + '[$__range]))', "H", fmt="table", instant=True),
    pq('100 * avg by (region) (avg_over_time(probe_success[$__range]) * on (instance, job, probe) group_left (region) max by (instance, job, probe, region) (sm_check_info))',
       "I", fmt="table", instant=True),
    pq('max by (spoke_id) (' + UP + ')', "J", fmt="table", instant=True),
    lq('sum by (instance, src_device, dst_device) (count_over_time(' + CHG_SEL + ' [$__range]))', "K", instant=True),
    lq('sum by (instance, src_device, dst_device) (count_over_time(' + CON_SEL + ' [$__range]))', "L", instant=True),
]
panels.append(panel(
    "marcusolsson-dynamictext-panel", "Site comparison - every site of the global network", 0, Y, 24, 12, cmp_t,
    "One row per site (discovery sites and federation spokes). Devices: network_topology_device_info now / seen in the last hour. Links: edge_info "
    "adjacencies with an end at the site. Device availability: per-minute share of the site's devices reporting, averaged over the range; unreachable "
    "minutes: minutes in range with at least one device missing. Spoke liveness and down minutes: network_topology_federation_spoke_up on the hub. "
    "Changes and conflicts: Loki topology_change / topology_conflict lines in range, attributed to the site of src_device and dst_device. Probe success: "
    "probe_success for the site's probe region (global core: mean of all regions). Click a site name to investigate it; the selected site is highlighted.",
    ds=MIXED,
    opts={"renderMode": "data", "content": "{{cmpTable}}", "defaultContent": "<p>No site data in the selected range.</p>",
          "styles": CMP_CSS, "helpers": CMP_JS, "afterRender": "", "externalStyles": [], "contentPartials": [], "wrap": False,
          "editors": ["default", "styles", "helpers"], "editor": {"language": "html", "format": "auto"}, "status": ""}))
Y += 10

# ================================================================ variables
SITE_Q = ('query_result(max by (site) (last_over_time(' + INFO + '[$__range])) or max by (site) '
          '(label_replace(last_over_time(' + UP + '[$__range]), "site", "$1", "spoke_id", "(.*)")))')


def chain(start_label, start_val, rules):
    e = 'label_replace(label_replace(vector(1), "s", "$site", "", ""), "' + start_label + '", "' + start_val + '", "", "")'
    for val, pat in rules:
        e = 'label_replace(' + e + ', "' + start_label + '", "' + val + '", "s", "' + pat + '")'
    return 'query_result(' + e + ')'


def hidden_q(name, query, regex):
    return {"name": name, "type": "query", "hide": 2, "datasource": PROM,
            "query": {"qryType": 3, "query": query, "refId": "PrometheusVariableQueryEditor-VariableQuery"},
            "definition": query, "regex": regex, "refresh": 2, "sort": 0, "multi": False, "includeAll": False,
            "current": {}, "options": []}


variables = [
    {"name": "site", "label": "Site", "type": "query", "datasource": PROM,
     "query": {"qryType": 3, "query": SITE_Q, "refId": "PrometheusVariableQueryEditor-VariableQuery"},
     "definition": SITE_Q, "regex": '/site="([^"]+)"/', "refresh": 2, "sort": 1, "multi": False, "includeAll": False,
     "current": {"text": "hq-dc1", "value": "hq-dc1"}, "options": [],
     "description": "Discovery site (device_info site) or federation spoke (spoke_id)."},
    hidden_q("dsite", chain("v", "$site", [(v, k) for k, v in SPOKE_TO_DEVSITE.items()]), '/v="([^"]*)"/'),
    hidden_q("fspoke", chain("v", "$site", [(v, k) for k, v in DEVSITE_TO_SPOKE.items()]), '/v="([^"]*)"/'),
    hidden_q("sexp", chain("v", "none", [(v, k) for k, v in SPOKE_EXPORTER.items()]), '/v="([^"]*)"/'),
    hidden_q("region", chain("r", ALL_REGIONS, REGION_RULES), '/r="([^"]*)"/'),
]
for name, lab in (("dexp", "instance"), ("devs", "device_id")):
    q = 'label_values(' + INFO + '{site="$dsite"}, ' + lab + ')'
    variables.append({"name": name, "type": "query", "hide": 2, "datasource": PROM,
                      "query": {"qryType": 1, "query": q, "refId": "PrometheusVariableQueryEditor-VariableQuery"},
                      "definition": q, "refresh": 2, "sort": 1, "multi": True, "includeAll": True, "allValue": None,
                      "current": {"text": ["All"], "value": ["$__all"]}, "options": []})

dash = {
    "apiVersion": "dashboard.grafana.app/v1beta1",
    "kind": "Dashboard",
    "metadata": {"name": UID, "annotations": {"grafana.app/folder": "netobs-showcase"}},
    "spec": {
        "title": "Network Observability - WAN Site Investigation",
        "description": "Investigate one site of the global WAN - discovery inventory, federation liveness, local topology, changes and probe experience - and compare it with every other site.",
        "uid": UID,
        "tags": ["netobs-showcase", "wan", "site", "investigation"],
        "editable": True, "graphTooltip": 1, "refresh": "1m", "schemaVersion": 41, "timezone": "utc",
        "time": {"from": "now-3h", "to": "now"},
        "timepicker": {"refresh_intervals": ["30s", "1m", "5m", "15m"]},
        "links": [
            {"type": "dashboards", "tags": ["netobs-showcase"], "asDropdown": True, "title": "Network Operations",
             "includeVars": False, "keepTime": True, "targetBlank": False, "icon": "external link"},
        ],
        "templating": {"list": variables},
        "annotations": {"list": [
            {"name": "Spoke down", "enable": True, "iconColor": "#F2495C", "datasource": PROM,
             "expr": 'max(' + UP + FS + ') == 0', "step": "1m", "titleFormat": "Spoke $fspoke down", "textFormat": "Hub marks the spoke down (network_topology_federation_spoke_up = 0)",
             "useValueForTime": False},
        ]},
        "panels": panels,
    },
}
with open(OUT, "w") as f:
    json.dump(dash, f, indent=2)
print("wrote", OUT, len(panels), "panels")
