import os
import json
UID="nos-change-and-discovery"
PROM={"type":"prometheus","uid":"grafanacloud-prom"}
LOKI={"type":"loki","uid":"grafanacloud-logs"}
EX='instance=~"$exporter"'
LSEL='{source="network-topology-exporter", instance=~"$exporter"}'
JOB=''

def lr(e, by=(), agg="sum"):
    cols=", ".join(("exporter",)+tuple(by))
    return f'{agg} by ({cols})(label_replace({e}, "exporter", "$1", "instance", "netobs-(.*):9100"))'

_id=[0]
def nid():
    _id[0]+=1; return _id[0]

def pq(expr, ref="A", legend=None, fmt=None, instant=False):
    t={"refId":ref,"datasource":PROM,"expr":expr,"editorMode":"code","range":not instant,"instant":instant}
    if legend is not None: t["legendFormat"]=legend
    if fmt: t["format"]=fmt
    return t
def lq(expr, ref="A", legend=None, instant=False):
    t={"refId":ref,"datasource":LOKI,"expr":expr,"editorMode":"code","queryType":"instant" if instant else "range"}
    if legend is not None: t["legendFormat"]=legend
    return t

def thr(*steps):
    return {"mode":"absolute","steps":[{"color":c,"value":v} for c,v in steps]}

def panel(typ, title, x,y,w,h, targets, desc=None, ds=PROM, fc=None, opts=None, tr=None, mixed=False):
    p={"id":nid(),"type":typ,"title":title,"gridPos":{"x":x,"y":y,"w":w,"h":h},
       "datasource":ds,"targets":targets,
       "fieldConfig":fc or {"defaults":{},"overrides":[]},"options":opts or {}}
    if desc: p["description"]=desc
    if tr: p["transformations"]=tr
    return p
def row(title,y):
    return {"id":nid(),"type":"row","title":title,"collapsed":False,"gridPos":{"x":0,"y":y,"w":24,"h":1},"panels":[]}

GREEN="green"; AMBER="#FF9830"; RED="red"
panels=[]
# ---------- Row 1
panels.append(row("Estate at a glance",0))
cards_html='''<div class="cards">
{{#each data}}
<div class="card {{#if stale}}bad{{else}}ok{{/if}}">
  <div class="hdr"><span class="name">{{exporter}}</span><span class="pill">{{#if stale}}GRAPH STALE{{else}}GRAPH FRESH{{/if}}</span></div>
  <div class="grid">
    <div class="kpi"><b>{{devices}}</b><i>devices</i></div>
    <div class="kpi"><b>{{links}}</b><i>links</i></div>
    <div class="kpi"><b>{{vendors}}</b><i>vendors</i></div>
    <div class="kpi {{#if faults}}warn{{/if}}"><b>{{faults}}</b><i>modules not OK</i></div>
    <div class="kpi"><b>{{chg_range}}</b><i>changes in range</i></div>
    <div class="kpi"><b>{{chg_total}}</b><i>changes since start</i></div>
    <div class="kpi"><b>{{cycle_p95}}s</b><i>cycle p95</i></div>
  </div>
</div>
{{/each}}
</div>'''
cards_css='''
.cards{display:flex;gap:14px;flex-wrap:wrap;height:100%;align-items:stretch}
.card{flex:1 1 300px;border-radius:8px;padding:12px 16px;border:1px solid rgba(128,128,128,.35);border-left:6px solid #73BF69;background:rgba(115,191,105,.07);box-sizing:border-box}
.card.bad{border-left-color:#F2495C;background:rgba(242,73,92,.10)}
.hdr{display:flex;justify-content:space-between;align-items:center;margin-bottom:10px}
.name{font-size:18px;font-weight:600;letter-spacing:.3px}
.pill{font-size:11px;font-weight:600;padding:2px 8px;border-radius:10px;background:rgba(115,191,105,.25);color:#73BF69}
.card.bad .pill{background:rgba(242,73,92,.25);color:#F2495C}
.grid{display:grid;grid-template-columns:repeat(4,1fr);gap:8px 6px}
.kpi{display:flex;flex-direction:column}
.kpi b{font-size:22px;line-height:1.1;font-weight:600}
.kpi i{font-size:11px;font-style:normal;opacity:.7;text-transform:uppercase;letter-spacing:.4px}
.kpi.warn b{color:#FF9830}
'''
card_targets=[
 pq(lr(f'sum by (instance)(network_topology_graph_devices_total{{{EX}}})'),"A",fmt="table",instant=True),
 pq(lr(f'sum by (instance)(network_topology_graph_edges_total{{{EX}}})'),"B",fmt="table",instant=True),
 pq(lr(f'max by (instance)(network_topology_graph_stale{{{EX}}})',agg="max"),"C",fmt="table",instant=True),
 pq(lr(f'histogram_quantile(0.95, sum by (le, instance)(rate(network_topology_discovery_cycle_duration_seconds_bucket{{{EX}}}[10m])))',agg="max").join(["round(",", 0.1)"]),"D",fmt="table",instant=True),
 pq(lr(f'sum by (instance)(increase(network_topology_change_total{{{EX}}}[$__range]))'),"E",fmt="table",instant=True),
 pq(lr(f'sum by (instance)(network_topology_change_total{{{EX}}})'),"F",fmt="table",instant=True),
 pq(lr(f'count by (instance)(count by (instance, vendor)(network_topology_device_info{{{EX}}}))'),"G",fmt="table",instant=True),
 pq(lr(f'sum by (instance)(network_topology_module_last_status{{{EX}}} > bool 0)'),"H",fmt="table",instant=True),
]
rename={"Value #A":"devices","Value #B":"links","Value #C":"stale","Value #D":"cycle_p95","Value #E":"chg_range","Value #F":"chg_total","Value #G":"vendors","Value #H":"faults"}
card_tr=[{"id":"merge","options":{}},
 {"id":"organize","options":{"excludeByName":{"Time":True},"renameByName":rename}},
 {"id":"convertFieldType","options":{"conversions":[{"targetField":"chg_range","destinationType":"number","fields":{}}]}},
 {"id":"sortBy","options":{"fields":{},"sort":[{"field":"exporter"}]}}]
card_tr=[card_tr[0],card_tr[1],card_tr[3]]
cards=panel("marcusolsson-dynamictext-panel","Exporter summary",0,1,24,7,card_targets,
  desc="One card per selected discovery exporter. Devices and links come from the live topology graph, changes from network_topology_change_total, cycle p95 from the discovery cycle duration histogram, and modules not OK from network_topology_module_last_status.",
  tr=card_tr,
  opts={"renderMode":"allRows","content":cards_html,"defaultContent":"<p>No exporter data in the selected range.</p>","styles":cards_css,
        "helpers":"","afterRender":"","externalStyles":[],"contentPartials":[],"wrap":True,"editors":["default","styles"],"editor":{"language":"html","format":"auto"}})
cards["fieldConfig"]={"defaults":{},"overrides":[]}
panels.append(cards)

# ---------- Row 2 topology changes
panels.append(row("Topology change intelligence",8))
def lstat(title,x,expr,color,desc):
    th={"green":thr(("blue",None)),"red":thr(("green",None),("red",1)),"orange":thr(("green",None),("orange",1))}[color]
    return panel("stat",title,x,9,4,4,[lq(f'sum(count_over_time({LSEL} | logfmt {expr} [$__range])) or vector(0)',"A",instant=True)],ds=LOKI,desc=desc,
      fc={"defaults":{"color":{"mode":"thresholds"},"noValue":"0","unit":"short","decimals":0,"thresholds":th},"overrides":[]},
      opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"background","graphMode":"none","textMode":"value","justifyMode":"center"})
panels.append(lstat("Links discovered",0,'| event="topology_change" | change_kind="added"',"green","Topology change log lines with change_kind=added over the selected range (Loki, network-topology-exporter stream)."))
panels.append(lstat("Links lost",4,'| event="topology_change" | change_kind="removed"',"red","Topology change log lines with change_kind=removed over the selected range. Removed edges are logged at warn level."))
panels.append(lstat("Neighbour conflicts",8,'| event="topology_conflict"',"orange","Conflict log lines (conflict_type=neighbour_disagreement) where two discovery protocols disagree about the same adjacency."))
panels.append(panel("stat","Spokes reporting",12,9,4,4,[pq('sum(network_topology_federation_spoke_up) or vector(0)',"A",instant=True),pq('count(network_topology_federation_spoke_up) or vector(0)',"B",instant=True)],
  desc="Federation spokes currently pushing to the global hub (network_topology_federation_spoke_up).",
  fc={"defaults":{"unit":"short","thresholds":thr(("red",None),("green",1)),"color":{"mode":"thresholds"},"noValue":"0"},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"background","graphMode":"none","textMode":"value","justifyMode":"center"},
  tr=[{"id":"calculateField","options":{"mode":"binary","binary":{"left":"Value #A","operator":"/","right":"Value #B"},"alias":"ratio","reduce":{"reducer":"sum"},"replaceFields":False}}]))
# replace spokes stat with simpler single query
panels[-1]=panel("stat","Spokes up",12,9,4,4,[pq('sum(network_topology_federation_spoke_up) or vector(0)',"A",instant=True)],
  desc="Federation spokes currently pushing to the global hub (network_topology_federation_spoke_up == 1).",
  fc={"defaults":{"unit":"short","thresholds":thr(("red",None),("green",1)),"color":{"mode":"thresholds"},"noValue":"0"},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"background","graphMode":"none","textMode":"value","justifyMode":"center"})
panels.append(panel("stat","Spokes down",16,9,4,4,[pq('count(network_topology_federation_spoke_up == 0) or vector(0)',"A",instant=True)],
  desc="Federation spokes that have missed their liveness window.",
  fc={"defaults":{"unit":"short","thresholds":thr(("green",None),("red",1)),"color":{"mode":"thresholds"},"noValue":"0"},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"background","graphMode":"none","textMode":"value","justifyMode":"center"}))
panels.append(panel("stat","Hub updates rejected",20,9,4,4,[pq(f'sum(increase(network_topology_graph_updates_rejected_total{{{EX}}}[$__range])) or vector(0)',"A",instant=True)],
  desc="Spoke graph pushes refused by the hub (size budget, invalid labels, structural or stale generation) over the selected range.",
  fc={"defaults":{"unit":"short","thresholds":thr(("green",None),("orange",1)),"color":{"mode":"thresholds"},"noValue":"0","decimals":0},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"background","graphMode":"none","textMode":"value","justifyMode":"center"}))

def barts(title,x,w,targets,ds,desc):
    for t in targets: t["interval"]="1m"
    return panel("timeseries",title,x,13,w,9,targets,ds=ds,desc=desc,
      fc={"defaults":{"color":{"mode":"palette-classic"},"unit":"short","decimals":0,"min":0,"max":None,
        "custom":{"drawStyle":"bars","axisSoftMax":4,"fillOpacity":85,"lineWidth":1,"stacking":{"mode":"normal","group":"A"},"barAlignment":0,"showPoints":"never","axisLabel":"events","gradientMode":"none","spanNulls":False}},
        "overrides":[
         {"matcher":{"id":"byRegexp","options":"^added.*"},"properties":[{"id":"color","value":{"mode":"shades","fixedColor":"green"}}]},
         {"matcher":{"id":"byRegexp","options":"^removed.*"},"properties":[{"id":"color","value":{"mode":"shades","fixedColor":"red"}}]}]},
      opts={"legend":{"displayMode":"table","placement":"right","calcs":["sum"],"showLegend":True},"tooltip":{"mode":"multi","sort":"desc"}})
panels.append(barts("Topology changes by kind and protocol (metrics)",0,8,
  [pq(f'sum by (change_kind, discovery_proto)(increase(network_topology_change_total{{{EX}}}[$__rate_interval]))',"A",legend="{{change_kind}} / {{discovery_proto}}")],PROM,
  "increase() of network_topology_change_total per interval, stacked by change_kind and discovery_proto across the selected exporters. Counter resets on exporter restart are handled by increase()."))
panels.append(barts("Change events logged by kind and protocol",8,8,
  [lq(f'sum by (change_kind, proto)(count_over_time({LSEL} | logfmt | event="topology_change" [1m]))',"A",legend="{{change_kind}} / {{proto}}")],LOKI,
  "Topology change log lines from the network-topology-exporter stream, counted per interval from the logfmt body fields change_kind and proto (the stream labels only carry the dominant kind for each batch)."))
panels.append(panel("barchart","Cumulative changes since exporter start",16,13,8,9,
  [pq(f'sum by (discovery_proto, change_kind)(network_topology_change_total{{{EX}}})',"A",fmt="table",instant=True)],
  desc="Raw network_topology_change_total grouped by protocol and kind: includes the initial discovery of every adjacency when each exporter started.",
  fc={"defaults":{"color":{"mode":"palette-classic"},"custom":{"fillOpacity":85,"lineWidth":1,"gradientMode":"none","axisLabel":"changes"},"unit":"short","decimals":0},
      "overrides":[{"matcher":{"id":"byName","options":"added"},"properties":[{"id":"color","value":{"mode":"fixed","fixedColor":"green"}}]},
                   {"matcher":{"id":"byName","options":"removed"},"properties":[{"id":"color","value":{"mode":"fixed","fixedColor":"red"}}]}]},
  opts={"orientation":"horizontal","stacking":"normal","xTickLabelRotation":0,"showValue":"auto","legend":{"displayMode":"list","placement":"bottom","showLegend":True},"tooltip":{"mode":"multi","sort":"desc"},"barWidth":0.8,"groupWidth":0.7},
  tr=[{"id":"groupingToMatrix","options":{"rowField":"discovery_proto","columnField":"change_kind","valueField":"Value"}}]))
panels.append(panel("logs","Change and conflict events",0,22,24,10,
  [lq(f'{LSEL} | logfmt | event=~"topology_change|topology_conflict"',"A")],ds=LOKI,
  desc="Raw topology_change and topology_conflict events (logfmt) from the exporters. Expand a line to see structured fields: src_device, src_port, dst_device, dst_port, direction, change_kind and proto.",
  opts={"showTime":True,"showLabels":False,"showCommonLabels":False,"wrapLogMessage":True,"prettifyLogMessage":False,"enableLogDetails":True,"dedupStrategy":"none","sortOrder":"Descending","enableInfiniteScrolling":False}))

# ---------- Row 3 drift
panels.append(row("Configuration and software drift",32))
vend=f'count by (vendor, os_version)(network_topology_device_info{{{EX}}})'
panels.append(panel("table","Software versions per vendor",0,33,9,10,
  [pq(vend,"A",fmt="table",instant=True),
   pq(f'100 * {vend} / on (vendor) group_left() sum by (vendor)({vend})',"B",fmt="table",instant=True)],
  desc="Devices per vendor and OS version across the selected exporters. Share is the fraction of that vendor's devices on the version; orange marks a minority version (under 50% of the vendor) that deserves a drift review.",
  fc={"defaults":{"custom":{"align":"auto","cellOptions":{"type":"auto"}}},
   "overrides":[
    {"matcher":{"id":"byName","options":"Share of vendor"},"properties":[{"id":"unit","value":"percent"},{"id":"decimals","value":0},{"id":"min","value":0},{"id":"max","value":100},
       {"id":"custom.cellOptions","value":{"type":"gauge","mode":"basic","valueDisplayMode":"text"}},
       {"id":"thresholds","value":thr((AMBER,None),("green",50))},{"id":"color","value":{"mode":"thresholds"}}]},
    {"matcher":{"id":"byName","options":"Devices"},"properties":[{"id":"custom.width","value":90},{"id":"custom.cellOptions","value":{"type":"color-text"}},{"id":"color","value":{"mode":"fixed","fixedColor":"text"}}]}]},
  opts={"showHeader":True,"cellHeight":"sm","sortBy":[{"displayName":"Vendor","desc":False}]},
  tr=[{"id":"merge","options":{}},{"id":"organize","options":{"excludeByName":{"Time":True},"renameByName":{"vendor":"Vendor","os_version":"OS version","Value #A":"Devices","Value #B":"Share of vendor"},"indexByName":{"vendor":0,"os_version":1,"Value #A":2,"Value #B":3}}}]))
panels.append(panel("barchart","Devices per vendor by OS version",9,33,7,10,
  [pq(vend,"A",fmt="table",instant=True)],
  desc="Stacked device counts: one bar per vendor, one segment per OS version in use. More than one segment in a bar means that vendor is running mixed software.",
  fc={"defaults":{"color":{"mode":"palette-classic"},"custom":{"fillOpacity":85,"lineWidth":1,"gradientMode":"none","axisLabel":"devices"},"unit":"short","decimals":0},"overrides":[]},
  opts={"orientation":"vertical","stacking":"normal","showValue":"auto","xTickLabelRotation":0,"legend":{"displayMode":"table","placement":"bottom","showLegend":True},"tooltip":{"mode":"multi","sort":"desc"},"barWidth":0.8,"groupWidth":0.7},
  tr=[{"id":"groupingToMatrix","options":{"rowField":"vendor","columnField":"os_version","valueField":"Value"}}]))
upq=lr(f'bottomk(10, network_topology_device_uptime_seconds{{{EX}}}) * on (instance, device_id) group_left (vendor, os_version) network_topology_device_info{{{EX}}}',by=("device_id","vendor","os_version"),agg="max")
panels.append(panel("table","Shortest uptime - recent reboots",16,33,8,10,
  [pq(upq,"A",fmt="table",instant=True)],
  desc="The ten devices with the lowest sysUpTime (network_topology_device_uptime_seconds joined to device_info). Red means rebooted within 24 hours, orange within 7 days.",
  fc={"defaults":{"custom":{"align":"auto","cellOptions":{"type":"auto"}}},"overrides":[
    {"matcher":{"id":"byName","options":"Uptime"},"properties":[{"id":"unit","value":"dtdurations"},{"id":"custom.cellOptions","value":{"type":"color-background","mode":"gradient"}},
       {"id":"thresholds","value":thr(("red",None),("orange",86400),("green",604800))},{"id":"color","value":{"mode":"thresholds"}}]}]},
  opts={"showHeader":True,"cellHeight":"sm","sortBy":[{"displayName":"Uptime","desc":False}]},
  tr=[{"id":"organize","options":{"excludeByName":{"Time":True,"os_version":True},"renameByName":{"exporter":"Exporter","device_id":"Device","vendor":"Vendor","Value":"Uptime"},"indexByName":{"device_id":0,"vendor":1,"exporter":2,"Value":3}}}]))
panels.append(panel("stat","Devices rebooted in last 24h",0,43,6,4,
  [pq(f'count(network_topology_device_uptime_seconds{{{EX}}} < 86400) or vector(0)',"A",instant=True)],
  desc="Devices whose sysUpTime is under 24 hours.",
  fc={"defaults":{"unit":"short","noValue":"0","decimals":0,"thresholds":thr(("green",None),("orange",1)),"color":{"mode":"thresholds"}},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"background","graphMode":"none","textMode":"value","justifyMode":"center"}))
panels.append(panel("stat","Vendors on mixed software",6,43,6,4,
  [pq(f'count(count by (vendor)(count by (vendor, os_version)(network_topology_device_info{{{EX}}})) > 1) or vector(0)',"A",instant=True)],
  desc="Vendors running more than one OS version across the selected exporters.",
  fc={"defaults":{"unit":"short","noValue":"0","decimals":0,"thresholds":thr(("green",None),("orange",1)),"color":{"mode":"thresholds"}},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"background","graphMode":"none","textMode":"value","justifyMode":"center"}))
panels.append(panel("stat","Median device uptime",12,43,6,4,
  [pq(f'quantile(0.5, network_topology_device_uptime_seconds{{{EX}}})',"A",instant=True)],
  desc="Median sysUpTime across the selected exporters.",
  fc={"defaults":{"unit":"dtdurations","decimals":1,"thresholds":thr(("green",None)),"color":{"mode":"thresholds"}},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"value","graphMode":"none","textMode":"value","justifyMode":"center"}))
panels.append(panel("stat","Fleet size under review",18,43,6,4,
  [pq(f'count(network_topology_device_info{{{EX}}})',"A",instant=True)],
  desc="Devices counted in the drift and uptime panels above.",
  fc={"defaults":{"unit":"short","decimals":0,"thresholds":thr(("blue",None)),"color":{"mode":"thresholds"}},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"value","graphMode":"none","textMode":"value","justifyMode":"center"}))

# ---------- Row 4 discovery health
Y=47
panels.append(row("Discovery pipeline health",Y))
mod=lr(f'max by (instance, module)(network_topology_module_last_status{{{EX}}})',by=("module",),agg="max")
status_map=[{"type":"value","options":{"0":{"text":"OK","color":"green","index":0},"1":{"text":"Degraded","color":"#FF9830","index":1},"2":{"text":"Hard-failed","color":"red","index":2}}}]
panels.append(panel("status-history","Discovery module status",0,Y+1,10,11,[pq(mod,"A",legend="{{exporter}} / {{module}}")],
  desc="network_topology_module_last_status per exporter and discovery module: 0 OK, 1 Degraded, 2 Hard-failed.",
  fc={"defaults":{"mappings":status_map,"color":{"mode":"fixed","fixedColor":"green"},"thresholds":thr(("green",None),("#FF9830",1),("red",2)),"custom":{"fillOpacity":85,"lineWidth":1},"min":0,"max":2},"overrides":[]},
  opts={"showValue":"never","rowHeight":0.9,"colWidth":0.9,"legend":{"showLegend":True,"placement":"bottom"},"tooltip":{"mode":"single","sort":"none"}}))
poly_opts={"autoSizeColumns":True,"autoSizeRows":True,"autoSizePolygons":True,"layoutDisplayLimit":100,"layoutNumColumns":4,"layoutNumRows":4,"globalPolygonSize":"25","globalPolygonBorderSize":2,"globalPolygonBorderColor":"rgba(0,0,0,0)",
 "globalShape":"hexagon_pointed_top","globalAutoScaleFonts":True,"globalLabelFontSize":12,"globalValueFontSize":14,"globalTextFontAutoColorEnabled":True,"globalTextFontFamily":"Inter","ellipseEnabled":False,
 "sortByField":"thresholdLevel","sortByDirection":4,"globalTooltipsEnabled":True,"globalTooltipsShowTimestampEnabled":False,"globalTooltipsShowValueEnabled":True,"tooltipDisplayMode":"all",
 "globalDisplayMode":"all","globalShowValueEnabled":True,"globalOperator":"max","globalDecimals":0,"globalUnitFormat":"short","globalFillColor":"#299c46",
 "globalThresholdsConfig":[{"value":0,"state":0,"color":"#299c46"},{"value":1,"state":1,"color":"#ed8128"},{"value":2,"state":2,"color":"#f53636"}],
 "globalGradientsEnabled":True,"globalClickthrough":"","compositeConfig":{"animationSpeed":"1500","composites":[],"enabled":True},"overrideConfig":{"overrides":[]}}
panels.append(panel("grafana-polystat-panel","Module health map",10,Y+1,7,11,[pq(mod,"A",legend="{{exporter}} / {{module}}")],
  desc="Worst module status over the selected range, one hexagon per exporter and module. Green is OK, orange degraded, red hard-failed.",
  fc={"defaults":{"mappings":status_map,"thresholds":thr(("green",None),("#FF9830",1),("red",2))},"overrides":[]},opts=poly_opts))
modp=lr(f'histogram_quantile(0.95, sum by (le, instance, module)(rate(network_topology_discovery_module_duration_seconds_bucket{{{EX}}}[10m])))',by=("module",),agg="max")
panels.append(panel("bargauge","Module duration p95",17,Y+1,7,11,[pq(modp,"A",legend="{{exporter}} / {{module}}",instant=True)],
  desc="95th percentile of network_topology_discovery_module_duration_seconds over the last 10 minutes, per exporter and module.",
  fc={"defaults":{"unit":"s","decimals":3,"min":0,"max":0.2,"color":{"mode":"continuous-GrYlRd"},"thresholds":thr(("green",None),("orange",0.15),("red",0.5))},"overrides":[]},
  opts={"orientation":"horizontal","displayMode":"gradient","showUnfilled":True,"valueMode":"color","namePlacement":"auto","sizing":"auto","minVizHeight":10,"minVizWidth":8,"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False}}))

Y2=Y+12
walk=lr(f'sum by (instance)(rate(network_topology_snmp_walks_total{{{EX},status="ok"}}[5m])) / sum by (instance)(rate(network_topology_snmp_walks_total{{{EX}}}[5m])) * 100',agg="max")
gopts={"orientation":"auto","showThresholdLabels":False,"showThresholdMarkers":True,"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"sizing":"auto","minVizHeight":75,"minVizWidth":75}
panels.append(panel("gauge","SNMP walk success",0,Y2,6,8,[pq(walk,"A",legend="{{exporter}}",instant=True)],
  desc="Share of SNMP walks with status=ok (network_topology_snmp_walks_total, 5 minute rate) per exporter.",
  fc={"defaults":{"unit":"percent","min":0,"max":100,"decimals":1,"color":{"mode":"thresholds"},"thresholds":thr(("red",None),("orange",90),("green",99))},"overrides":[]},opts=gopts))
hit=lr(f'sum by (instance)(rate(network_topology_snmp_session_pool_hits_total{{{EX}}}[5m])) / (sum by (instance)(rate(network_topology_snmp_session_pool_hits_total{{{EX}}}[5m])) + sum by (instance)(rate(network_topology_snmp_session_pool_misses_total{{{EX}}}[5m]))) * 100',agg="max")
panels.append(panel("gauge","SNMP session pool hit ratio",6,Y2,6,8,[pq(hit,"A",legend="{{exporter}}",instant=True)],
  desc="Share of SNMP session requests served from the pool without re-establishing a session (hits / (hits + misses), 5 minute rate).",
  fc={"defaults":{"unit":"percent","min":0,"max":100,"decimals":1,"color":{"mode":"thresholds"},"thresholds":thr(("red",None),("orange",50),("green",80))},"overrides":[]},opts=gopts))
cred=lr(f'sum by (instance, status)(increase(network_topology_credential_trials_total{{{EX}}}[$__range]))',by=("status",))
panels.append(panel("piechart","Credential trial outcomes",12,Y2,6,8,[pq(cred,"A",legend="{{exporter}} / {{status}}",instant=True)],
  desc="SNMP credential trials over the selected range (network_topology_credential_trials_total), split by exporter and outcome. Any non-ok slice indicates devices rejecting the configured credentials.",
  fc={"defaults":{"unit":"short","decimals":0,"color":{"mode":"palette-classic"}},"overrides":[
     {"matcher":{"id":"byRegexp","options":".*failed.*|.*error.*"},"properties":[{"id":"color","value":{"mode":"fixed","fixedColor":"red"}}]}]},
  opts={"pieType":"donut","displayLabels":["percent"],"legend":{"displayMode":"table","placement":"right","values":["value"],"showLegend":True},"tooltip":{"mode":"single","sort":"none"},"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False}}))
wk=f'sum by (walker, outcome)(increase(network_topology_walker_outcome_total{{{EX}}}[$__range])) or sum by (walker, outcome)(increase(network_topology_bgp_walker_outcome_total{{{EX}}}[$__range]))'
panels.append(panel("barchart","Walker outcomes",18,Y2,6,8,[pq(wk,"A",fmt="table",instant=True)],
  desc="Walk results per protocol walker over the selected range, stacked by outcome (edges, no_neighbours, mib_unimplemented, walker_drift, error). BGP vendor walkers are included.",
  fc={"defaults":{"color":{"mode":"palette-classic"},"custom":{"fillOpacity":85,"lineWidth":1,"gradientMode":"none"},"unit":"short","decimals":0},
      "overrides":[{"matcher":{"id":"byName","options":"edges"},"properties":[{"id":"color","value":{"mode":"fixed","fixedColor":"green"}}]},
                   {"matcher":{"id":"byName","options":"error"},"properties":[{"id":"color","value":{"mode":"fixed","fixedColor":"red"}}]},
                   {"matcher":{"id":"byName","options":"walker_drift"},"properties":[{"id":"color","value":{"mode":"fixed","fixedColor":"orange"}}]}]},
  opts={"orientation":"horizontal","stacking":"normal","showValue":"never","xTickLabelRotation":0,"legend":{"displayMode":"list","placement":"bottom","showLegend":True},"tooltip":{"mode":"multi","sort":"desc"},"barWidth":0.8,"groupWidth":0.7},
  tr=[{"id":"groupingToMatrix","options":{"rowField":"walker","columnField":"outcome","valueField":"Value"}}]))

Y3=Y2+8
def tsline(title,x,w,expr,legend,desc,unit="s",dec=3):
    return panel("timeseries",title,x,Y3,w,8,[pq(expr,"A",legend=legend)],desc=desc,
      fc={"defaults":{"unit":unit,"decimals":dec,"color":{"mode":"palette-classic"},"custom":{"drawStyle":"line","lineWidth":2,"fillOpacity":15,"gradientMode":"opacity","showPoints":"never","spanNulls":True}},"overrides":[]},
      opts={"legend":{"displayMode":"table","placement":"right","calcs":["lastNotNull","max"],"showLegend":True},"tooltip":{"mode":"multi","sort":"desc"}})
panels.append(tsline("Discovery cycle p95",0,8,lr(f'histogram_quantile(0.95, sum by (le, instance)(rate(network_topology_discovery_cycle_duration_seconds_bucket{{{EX}}}[$__rate_interval])))',agg="max"),"{{exporter}}","95th percentile duration of a full discovery cycle per exporter.",dec=2))
panels.append(tsline("SNMP rate-limit wait p95",8,8,lr(f'histogram_quantile(0.95, sum by (le, instance)(rate(network_topology_snmp_rate_limit_wait_seconds_bucket{{{EX}}}[$__rate_interval])))',agg="max"),"{{exporter}}","95th percentile time a poll spent blocked on the per-device SNMP rate limiter."))
panels.append(panel("stat","Cycle budget skips",16,Y3,8,8,[pq(lr(f'sum by (instance)(increase(network_topology_cycle_budget_skips_total{{{EX}}}[$__range]))'),"A",legend="{{exporter}}",instant=True)],
  desc="Devices skipped because a discovery cycle exhausted its time budget (network_topology_cycle_budget_skips_total) over the selected range. Anything above zero means discovery is falling behind.",
  fc={"defaults":{"unit":"short","decimals":0,"noValue":"0","color":{"mode":"thresholds"},"thresholds":thr(("green",None),("orange",1),("red",10))},"overrides":[]},
  opts={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"background","graphMode":"none","textMode":"value_and_name","justifyMode":"center","orientation":"vertical"}))

dash={"uid":UID,"title":"Network Observability - Change Intelligence and Discovery Health","tags":["netobs-showcase","network","change","discovery"],
 "description":"What changed in the global network, and whether the monitoring that tells us is healthy: topology changes and conflicts, software and reboot drift, and discovery module, walker and credential health across every exporter.",
 "editable":True,"graphTooltip":1,"schemaVersion":39,"version":1,"timezone":"utc","refresh":"1m","time":{"from":"now-1h","to":"now"},
 "links":[{"type":"dashboards","title":"Network Observability","tags":["netobs-showcase"],"asDropdown":True,"includeVars":False,"keepTime":True,"icon":"external link","tooltip":"","url":"","targetBlank":False}],
 "templating":{"list":[{"name":"exporter","label":"Exporter","type":"query","datasource":PROM,"definition":'label_values(network_topology_device_info, instance)',
   "query":{"query":'label_values(network_topology_device_info, instance)',"refId":"exporter"},"refresh":2,"multi":True,"includeAll":True,"allValue":".*","current":{"selected":True,"text":["All"],"value":["$__all"]},"sort":1,"hide":0}]},
 "annotations":{"list":[
  {"builtIn":1,"datasource":{"type":"grafana","uid":"-- Grafana --"},"enable":True,"hide":True,"iconColor":"rgba(0, 211, 255, 1)","name":"Annotations & Alerts","type":"dashboard"},
  {"name":"Links lost","datasource":LOKI,"enable":True,"iconColor":"red","expr":f'{LSEL} | logfmt | event="topology_change" | change_kind="removed"',
   "titleFormat":"{{change_kind}} {{proto}}","textFormat":"{{src_device}}:{{src_port}} -> {{dst_device}}","tagKeys":"","target":{"refId":"Anno","expr":f'{LSEL} | logfmt | event="topology_change" | change_kind="removed"',"queryType":"range"}},
  {"name":"Neighbour conflicts","datasource":LOKI,"enable":False,"iconColor":"orange","expr":f'{LSEL} | logfmt | event="topology_conflict"',
   "titleFormat":"{{conflict_type}}","textFormat":"{{src_device}}:{{src_port}} sources={{sources}}","tagKeys":"","target":{"refId":"Anno","expr":f'{LSEL} | logfmt | event="topology_conflict"',"queryType":"range"}},
  {"name":"Links discovered","datasource":LOKI,"enable":True,"iconColor":"green","expr":f'{LSEL} | logfmt | event="topology_change" | change_kind="added"',
   "titleFormat":"{{change_kind}} {{proto}}","textFormat":"{{src_device}}:{{src_port}} -> {{dst_device}}","tagKeys":"","target":{"refId":"Anno","expr":f'{LSEL} | logfmt | event="topology_change" | change_kind="added"',"queryType":"range"}}]},
 "panels":panels}
res={"apiVersion":"dashboard.grafana.app/v1beta1","kind":"Dashboard","metadata":{"name":UID,"annotations":{"grafana.app/folder":"netobs-showcase"}},"spec":dash}
out=os.path.join(os.path.dirname(os.path.abspath(__file__)), "nos-change-and-discovery.json")
json.dump(res,open(out,"w"),indent=2)
print("ok",len(panels))
