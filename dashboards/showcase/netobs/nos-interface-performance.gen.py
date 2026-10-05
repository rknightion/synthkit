import json
import os
OUT=os.path.join(os.path.dirname(os.path.abspath(__file__)), "nos-interface-performance.json")
PROM={"type":"prometheus","uid":"grafanacloud-prom"}
LOKI={"type":"loki","uid":"grafanacloud-logs"}
HF='instance=~"$host",device=~"$device"'
panels=[]
_id=[0]
def nid():
    _id[0]+=1; return _id[0]

def tgt(expr,legend="",ref="A",instant=False,fmt=None,ds=PROM):
    t={"datasource":ds,"expr":expr,"legendFormat":legend,"refId":ref,"editorMode":"code"}
    if instant: t["instant"]=True; t["range"]=False
    else: t["range"]=True; t["instant"]=False
    if fmt: t["format"]=fmt
    return t

def panel(ptype,title,x,y,w,h,targets,desc="",ds=PROM,options=None,defaults=None,overrides=None,transformations=None,extra=None):
    if defaults is not None and any(k in json.dumps(targets) for k in ("probe_","sm_check")) and ptype!="logs" and "dynamictext" not in ptype:
        defaults=dict(defaults,noValue="Awaiting probe results")
    p={"id":nid(),"type":ptype,"title":title,"description":desc,"datasource":ds,
       "gridPos":{"x":x,"y":y,"w":w,"h":h},"targets":targets,
       "fieldConfig":{"defaults":defaults or {},"overrides":overrides or []},
       "options":options or {}}
    if transformations: p["transformations"]=transformations
    if extra: p.update(extra)
    panels.append(p); return p

def row(title,y):
    panels.append({"id":nid(),"type":"row","title":title,"collapsed":False,"gridPos":{"x":0,"y":y,"w":24,"h":1},"panels":[]})

RX=f'rate(node_network_receive_bytes_total{{{HF}}}[$__rate_interval])*8'
TX=f'rate(node_network_transmit_bytes_total{{{HF}}}[$__rate_interval])*8'
PJ='probe_success * on(instance,job,probe,config_version) group_left(region) sm_check_info{job=~"$check",region=~"$region"}'
WH='instance=~"$host"'
RXA=f'({RX} or label_replace(rate(windows_net_bytes_received_total{{{WH}}}[$__rate_interval])*8,"device","$1","nic","(.*)"))'
TXA=f'({TX} or label_replace(rate(windows_net_bytes_sent_total{{{WH}}}[$__rate_interval])*8,"device","$1","nic","(.*)"))'
thr_green_red=[{"color":"green","value":None},{"color":"#EAB839","value":70},{"color":"red","value":90}]

# ---- header (business text)
css="""
.dt-row{padding:0;}
.nos-wrap{display:flex;gap:12px;align-items:stretch;height:100%;font-family:Inter,sans-serif;}
.nos-title{flex:2.2;display:flex;flex-direction:column;justify-content:center;padding:6px 14px;border-left:4px solid #5794F2;}
.nos-title h2{margin:0 0 4px 0;font-size:22px;font-weight:600;}
.nos-title p{margin:0;opacity:.75;font-size:13px;line-height:1.35;}
.nos-kpi{flex:1;display:flex;flex-direction:column;justify-content:center;align-items:flex-start;padding:6px 14px;border-radius:6px;background:rgba(128,128,128,.12);}
.nos-kpi .v{font-size:30px;font-weight:600;line-height:1.1;}
.nos-kpi .l{font-size:12px;text-transform:uppercase;letter-spacing:.06em;opacity:.7;}
.nos-kpi .s{font-size:12px;opacity:.65;}
.ok{color:#73BF69}.warn{color:#FF9830}.bad{color:#F2495C}
"""
content="""<div class="nos-wrap">
<div class="nos-title"><h2>Interface and path performance</h2>
<p>Per-interface throughput, utilisation, errors and link state across the server fleet, and end-to-end reachability and latency from the external probes. Live summary of the current selection.</p></div>
<div class="nos-kpi"><span class="l">Hosts reporting</span><span class="v">{{data.[0].[0].[Value #A]}}</span><span class="s">{{data.[1].[0].[Value #B]}} interfaces tracked</span></div>
<div class="nos-kpi"><span class="l">Interfaces up</span><span class="v {{#if data.[7].[0].[Value #H]}}warn{{else}}ok{{/if}}">{{data.[2].[0].[Value #C]}} / {{data.[3].[0].[Value #D]}}</span><span class="s">operstate reported by the host</span></div>
<div class="nos-kpi"><span class="l">Total throughput</span><span class="v">{{data.[4].[0].[Value #E]}}</span><span class="s">in + out, all selected interfaces</span></div>
<div class="nos-kpi"><span class="l">Checks passing</span>{{#if data.[6].[0].[Value #G]}}<span class="v {{#if data.[8].[0].[Value #I]}}bad{{else}}ok{{/if}}">{{data.[5].[0].[Value #F]}} / {{data.[6].[0].[Value #G]}}</span><span class="s">external probe checks</span>{{else}}<span class="v" style="opacity:.5">-</span><span class="s">awaiting probe results</span>{{/if}}</div>
</div>"""
defaultContent="<div class='nos-title'><h2>Interface and path performance</h2><p>Awaiting data for the current selection.</p></div>"
helpers=""
hdr_t=[
 tgt('count(count by (instance)(node_network_receive_bytes_total{instance=~"$host"}))',ref="A",instant=True,fmt="table"),
 tgt(f'count(node_network_receive_bytes_total{{{HF}}})',ref="B",instant=True,fmt="table"),
 tgt(f'count(node_network_up{{{HF}}} == 1) or vector(0)',ref="C",instant=True,fmt="table"),
 tgt(f'count(node_network_up{{{HF}}})',ref="D",instant=True,fmt="table"),
 tgt(f'sum({RXA}+{TXA})',ref="E",instant=True,fmt="table"),
 tgt(f'count({PJ} == 1) or vector(0)',ref="F",instant=True,fmt="table"),
 tgt(f'count({PJ}) or vector(0)',ref="G",instant=True,fmt="table"),
 tgt(f'count(node_network_up{{{HF}}} == 0) or vector(0)',ref="H",instant=True,fmt="table"),
 tgt(f'count({PJ} == 0) or vector(0)',ref="I",instant=True,fmt="table"),
]
import os
if os.environ.get("DBGJSON"): content="<pre style=\"font-size:10px\">{{json data}}</pre>"
panel("marcusolsson-dynamictext-panel","",0,0,24,4,hdr_t,
  options={"renderMode":"data","content":content,"defaultContent":defaultContent,"helpers":helpers,"styles":css,"wrap":True,"status":"","editors":["default","styles"],"externalStyles":[],"afterRender":"","contentPartials":[],"editor":{"language":"html","format":"auto"}},
  overrides=[{"matcher":{"id":"byFrameRefID","options":"E"},"properties":[{"id":"unit","value":"bps"},{"id":"decimals","value":2}]}],
  extra={"transparent":True})

# ---- KPI stats
def stat(title,x,expr,unit,desc,thr=None,decimals=None,color_mode="value",ref="A",mn=None,mx=None,spark=True,fixed=None):
    d={"unit":unit,"color":{"mode":"thresholds"},"thresholds":{"mode":"absolute","steps":thr or [{"color":"blue","value":None}]}}
    if fixed: d["color"]={"mode":"fixed","fixedColor":fixed}
    if decimals is not None: d["decimals"]=decimals
    if mn is not None: d["min"]=mn
    if mx is not None: d["max"]=mx
    panel("stat",title,x,4,4,4,[tgt(expr,ref=ref)],desc,
      options={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":color_mode,"graphMode":"area" if spark else "none","textMode":"value","justifyMode":"center","orientation":"auto"},
      defaults=d)
stat("Inbound",0,f'sum({RXA})',"bps","Aggregate receive rate across the selected interfaces.",fixed="#5794F2",decimals=2)
stat("Outbound",4,f'sum({TXA})',"bps","Aggregate transmit rate across the selected interfaces.",fixed="#B877D9",decimals=2)
stat("Peak interface utilisation",8,f'max(100 * rate(node_network_receive_bytes_total{{{HF}}}[$__rate_interval]) / on(instance,device) (node_network_speed_bytes > 0))',"percent","Busiest interface by receive rate against its negotiated link speed (node_network_speed_bytes).",thr=thr_green_red,decimals=2,mn=0,mx=100)
stat("Errors and drops",12,f'sum(rate(node_network_receive_errs_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_errs_total{{{HF}}}[$__rate_interval])+rate(node_network_receive_drop_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_drop_total{{{HF}}}[$__rate_interval]))',"pps","Interface errors plus drops per second, both directions.",thr=[{"color":"green","value":None},{"color":"orange","value":0.01},{"color":"red","value":1}],decimals=2)
stat("Packets per second",16,f'sum(rate(node_network_receive_packets_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_packets_total{{{HF}}}[$__rate_interval]))',"pps","Receive plus transmit packet rate across the selected interfaces.",fixed="#73BF69",decimals=1)
stat("Interfaces up",20,f'100 * count(node_network_up{{{HF}}} == 1) / count(node_network_up{{{HF}}})',"percent","Share of interfaces with operstate up (node_network_up).",thr=[{"color":"red","value":None},{"color":"orange","value":95},{"color":"green","value":100}],decimals=0,mn=0,mx=100,spark=False)

# ---- Throughput row
y=8
row("Interface throughput and utilisation",y); y+=1
neg=lambda rx: [{"matcher":{"id":"byRegexp","options":".* out$"},"properties":[{"id":"custom.transform","value":"negative-Y"}]}]
ts_custom={"drawStyle":"line","lineInterpolation":"smooth","lineWidth":2,"fillOpacity":18,"gradientMode":"opacity","showPoints":"never","spanNulls":True,"axisPlacement":"auto","axisLabel":"","stacking":{"mode":"none","group":"A"},"thresholdsStyle":{"mode":"off"}}
panel("timeseries","Throughput in and out per interface",0,y,12,11,
  [tgt(f'sum by (instance,device)({RXA})',"{{instance}} {{device}} in","A"),tgt(f'sum by (instance,device)({TXA})',"{{instance}} {{device}} out","B")],
  "Receive rate above the axis, transmit rate mirrored below. Driven by rate(node_network_receive_bytes_total / node_network_transmit_bytes_total) x 8, plus windows_net_bytes_received_total / windows_net_bytes_sent_total for Windows hosts (all NICs of the selected hosts).",
  options={"legend":{"displayMode":"table","placement":"right","calcs":["mean","max"],"showLegend":True,"width":330},"tooltip":{"mode":"multi","sort":"desc"}},
  defaults={"unit":"bps","custom":ts_custom,"color":{"mode":"palette-classic"},"decimals":2},overrides=neg(0))
panel("timeseries","Link utilisation",12,y,12,11,
  [tgt(f'100 * rate(node_network_receive_bytes_total{{{HF}}}[$__rate_interval]) / on(instance,device) (node_network_speed_bytes > 0)',"{{instance}} {{device}} in","A"),
   tgt(f'100 * rate(node_network_transmit_bytes_total{{{HF}}}[$__rate_interval]) / on(instance,device) (node_network_speed_bytes > 0)',"{{instance}} {{device}} out","B")],
  "Percent of negotiated link speed in use, where node_network_speed_bytes is non-zero. Receive above the axis, transmit mirrored below. ",
  options={"legend":{"displayMode":"list","placement":"bottom","showLegend":True},"tooltip":{"mode":"multi","sort":"desc"}},
  defaults={"unit":"percent","decimals":2,"custom":ts_custom,"color":{"mode":"palette-classic"}},overrides=neg(0))
y+=11

# ---- Errors row
row("Errors, drops, packets and link state",y); y+=1
panel("timeseries","Errors and drops per second",0,y,8,8,
  [tgt(f'sum(rate(node_network_receive_errs_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_errs_total{{{HF}}}[$__rate_interval]))',"errors","A"),
   tgt(f'sum(rate(node_network_receive_drop_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_drop_total{{{HF}}}[$__rate_interval]))',"drops","B")],
  "Receive plus transmit errors (node_network_*_errs_total) and drops (node_network_*_drop_total) per second, summed over the selected interfaces. A flat zero line means clean links.",
  options={"legend":{"displayMode":"list","placement":"bottom","showLegend":True},"tooltip":{"mode":"multi","sort":"desc"}},
  defaults={"unit":"pps","decimals":2,"min":0,"custom":dict(ts_custom,fillOpacity=30,axisSoftMax=1),"color":{"mode":"palette-classic"}})
panel("timeseries","Packets per second in and out",8,y,8,8,
  [tgt(f'sum by (instance,device)(rate(node_network_receive_packets_total{{{HF}}}[$__rate_interval]))',"{{instance}} {{device}} in","A"),
   tgt(f'sum by (instance,device)(rate(node_network_transmit_packets_total{{{HF}}}[$__rate_interval]))',"{{instance}} {{device}} out","B")],
  "Packet rate per interface, receive above the axis and transmit mirrored below.",
  options={"legend":{"displayMode":"list","placement":"bottom","showLegend":True},"tooltip":{"mode":"multi","sort":"desc"}},
  defaults={"unit":"pps","decimals":1,"custom":ts_custom,"color":{"mode":"palette-classic"}},overrides=neg(0))
sh_map=[{"type":"value","options":{"0":{"text":"DOWN","color":"red","index":0},"1":{"text":"UP","color":"green","index":1}}}]
panel("status-history","Link state: operstate and carrier",16,y,8,8,
  [tgt(f'node_network_up{{{HF}}}',"{{instance}} {{device}} up","A"),tgt(f'node_network_carrier{{{HF}}}',"{{instance}} {{device}} carrier","B")],
  "node_network_up (operstate) and node_network_carrier per interface. Green is up, red is down.",
  options={"showValue":"never","rowHeight":0.85,"colWidth":0.95,"legend":{"showLegend":False,"placement":"bottom"},"tooltip":{"mode":"single"}},
  defaults={"mappings":sh_map,"color":{"mode":"thresholds"},"thresholds":{"mode":"absolute","steps":[{"color":"red","value":None},{"color":"green","value":1}]},"custom":{"fillOpacity":80,"lineWidth":1}})
y+=8

# ---- Inventory + Top N
row("Interface inventory and top talkers",y); y+=1
inv_targets=[
 tgt(f'max by (instance,device,operstate,adminstate,address)(node_network_info{{{HF}}})',ref="A",instant=True,fmt="table"),
 tgt(f'max by (instance,device)(node_network_speed_bytes{{{HF}}})*8',ref="B",instant=True,fmt="table"),
 tgt(f'max by (instance,device)(node_network_mtu_bytes{{{HF}}})',ref="C",instant=True,fmt="table"),
 tgt(f'sum by (instance,device)({RX})',ref="D",instant=True,fmt="table"),
 tgt(f'sum by (instance,device)({TX})',ref="E",instant=True,fmt="table"),
 tgt(f'sum by (instance,device)(rate(node_network_receive_errs_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_errs_total{{{HF}}}[$__rate_interval])+rate(node_network_receive_drop_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_drop_total{{{HF}}}[$__rate_interval]))',ref="F",instant=True,fmt="table"),
]
def ov(name,props): return {"matcher":{"id":"byName","options":name},"properties":props}
inv_ov=[
 ov("operstate",[{"id":"custom.cellOptions","value":{"type":"color-background","mode":"basic"}},{"id":"mappings","value":[{"type":"value","options":{"up":{"text":"UP","color":"green","index":0},"down":{"text":"DOWN","color":"red","index":1},"unknown":{"text":"UNKNOWN","color":"orange","index":2}}},{"type":"special","options":{"match":"null+nan","result":{"text":"n/a","color":"#464C54","index":3}}}]},{"id":"custom.width","value":95}]),
 ov("Link speed",[{"id":"unit","value":"bps"},{"id":"decimals","value":0},{"id":"custom.width","value":95}]),
 ov("MTU",[{"id":"unit","value":"none"},{"id":"decimals","value":0},{"id":"custom.width","value":70}]),
 ov("In",[{"id":"unit","value":"bps"},{"id":"decimals","value":2},{"id":"custom.cellOptions","value":{"type":"gauge","mode":"gradient"}},{"id":"min","value":0},{"id":"color","value":{"mode":"fixed","fixedColor":"#5794F2"}}]),
 ov("Out",[{"id":"unit","value":"bps"},{"id":"decimals","value":2},{"id":"custom.cellOptions","value":{"type":"gauge","mode":"gradient"}},{"id":"min","value":0},{"id":"color","value":{"mode":"fixed","fixedColor":"#B877D9"}}]),
 ov("Errors+drops /s",[{"id":"unit","value":"pps"},{"id":"decimals","value":2},{"id":"custom.cellOptions","value":{"type":"color-text"}},{"id":"thresholds","value":{"mode":"absolute","steps":[{"color":"green","value":None},{"color":"orange","value":0.01},{"color":"red","value":1}]}},{"id":"color","value":{"mode":"thresholds"}}]),
]
inv_tr=[
 {"id":"merge","options":{}},
 {"id":"organize","options":{"excludeByName":{"Time":True,"Value #A":True,"adminstate":True,"address":True},
   "indexByName":{"instance":0,"device":1,"operstate":2,"adminstate":3,"Value #B":4,"Value #C":5,"Value #D":6,"Value #E":7,"Value #F":8,"address":9},
   "renameByName":{"instance":"Host","device":"Interface","adminstate":"Admin","Value #B":"Link speed","Value #C":"MTU","Value #D":"In","Value #E":"Out","Value #F":"Errors+drops /s","address":"MAC"}}},
]
panel("table","Interface inventory",0,y,15,9,inv_targets,
  "One row per interface: operstate and admin state from node_network_info, negotiated speed, MTU, live in and out rates and the current error plus drop rate.",
  options={"showHeader":True,"cellHeight":"md","footer":{"show":False},"sortBy":[{"displayName":"In","desc":True}]},
  defaults={"custom":{"align":"auto","cellOptions":{"type":"auto"},"inspect":False}},overrides=inv_ov,transformations=inv_tr)
panel("bargauge","Utilisation per interface",15,y,9,9,
  [tgt(f'100 * rate(node_network_receive_bytes_total{{{HF}}}[$__rate_interval]) / on(instance,device) (node_network_speed_bytes > 0)',"{{instance}} {{device}} in","A",instant=True),
   tgt(f'100 * rate(node_network_transmit_bytes_total{{{HF}}}[$__rate_interval]) / on(instance,device) (node_network_speed_bytes > 0)',"{{instance}} {{device}} out","B",instant=True)],
  "Percent of negotiated link speed in use right now, per interface and direction.",
  options={"displayMode":"gradient","orientation":"horizontal","valueMode":"color","showUnfilled":True,"namePlacement":"left","sizing":"auto","minVizHeight":10,"minVizWidth":8,"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"legend":{"showLegend":False}},
  defaults={"unit":"suffix: %","decimals":2,"min":0,"color":{"mode":"continuous-BlPu"},"thresholds":{"mode":"absolute","steps":[{"color":"green","value":None},{"color":"red","value":1}]}})
y+=9

def topn(title,x,expr,unit,desc,col,colorscheme,thr=None):
    ov_=[ov("Throughput" if unit=="bps" else "Errors+drops /s",[{"id":"unit","value":unit},{"id":"decimals","value":2},{"id":"custom.cellOptions","value":{"type":"gauge","mode":"gradient","valueDisplayMode":"text"}},{"id":"color","value":{"mode":colorscheme}},{"id":"min","value":0}]
        +([{"id":"thresholds","value":{"mode":"absolute","steps":thr}}] if thr else [])),
         ov("Trend",[{"id":"custom.cellOptions","value":{"type":"sparkline","drawStyle":"line","lineInterpolation":"smooth","fillOpacity":25,"lineWidth":1,"hideValue":True}},{"id":"color","value":{"mode":"fixed","fixedColor":col}}])]
    tr=[{"id":"timeSeriesTable","options":{"A":{"stat":"lastNotNull","timeField":"Time"}}},{"id":"merge","options":{}},
        {"id":"organize","options":{"excludeByName":{"Time":True},"indexByName":{"instance":0,"device":1,"Value":2,"Trend #A":3},"renameByName":{"instance":"Host","device":"Interface","Value":"Throughput" if unit=="bps" else "Errors+drops /s","Trend #A":"Trend"}}}]
    panel("table",title,x,y,12,8,[tgt(expr,ref="A"),tgt(expr,ref="B",instant=True,fmt="table")],desc,
      options={"showHeader":True,"cellHeight":"md","footer":{"show":False},"sortBy":[{"displayName":"Throughput" if unit=="bps" else "Errors+drops /s","desc":True}]},
      defaults={"custom":{"align":"auto","cellOptions":{"type":"auto"},"inspect":False}},overrides=ov_,transformations=tr)
topn("Top interfaces by throughput",0,f'sum by (instance,device)({RXA}+{TXA})',"bps","Interfaces ranked by combined in plus out rate, with the trend over the selected window.","#5794F2","continuous-BlPu")
topn("Top interfaces by error rate",12,f'sum by (instance,device)(rate(node_network_receive_errs_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_errs_total{{{HF}}}[$__rate_interval])+rate(node_network_receive_drop_total{{{HF}}}[$__rate_interval])+rate(node_network_transmit_drop_total{{{HF}}}[$__rate_interval]))',"pps","Interfaces ranked by errors plus drops per second, with the trend over the selected window.","#F2495C","continuous-RdYlGr",[{"color":"green","value":None},{"color":"orange","value":0.01},{"color":"red","value":1}])
y+=8
panel("heatmap","Throughput heatmap by host",0,y,24,8,
  [tgt(f'sum by (instance)({RXA}+{TXA})',"{{instance}}","A")],
  "Combined in plus out rate per host over time. Each row is a host, colour is bits per second.",
  options={"calculate":False,"yAxis":{"axisPlacement":"left","reverse":False},"rowsFrame":{"layout":"auto"},"color":{"mode":"scheme","scheme":"Turbo","fill":"dark-orange","scale":"exponential","exponent":0.5,"steps":64,"reverse":False},
           "cellGap":2,"legend":{"show":True},"tooltip":{"mode":"single","showColorScale":True},"showValue":"never","filterValues":{"le":1e-9}},
  defaults={"unit":"bps","decimals":2,"custom":{"hideFrom":{"legend":False,"tooltip":False,"viz":False},"scaleDistribution":{"type":"linear"}}})
y+=8

# ---- Path performance
row("Path performance: external probes",y); y+=1
SMJ='* on(instance,job,probe,config_version) group_left(region) sm_check_info{job=~"$check",region=~"$region"}'
panel("stat","Probe success by check and region",0,y,12,7,
  [tgt(f'100 * avg by (job,region)(probe_success {SMJ})',"{{job}} ({{region}})","A",instant=True)],
  "Success percentage of each check, split by probe region. Driven by probe_success joined to sm_check_info.",
  options={"reduceOptions":{"calcs":["lastNotNull"],"fields":"","values":False},"colorMode":"value","graphMode":"none","textMode":"value_and_name","justifyMode":"center","orientation":"auto"},
  defaults={"unit":"percent","decimals":2,"min":0,"max":100,"color":{"mode":"thresholds"},"thresholds":{"mode":"absolute","steps":[{"color":"red","value":None},{"color":"orange","value":95},{"color":"green","value":99}]}})
panel("state-timeline","Check state over time",12,y,12,7,
  [tgt(f'probe_success {SMJ}',"{{job}} ({{region}})","A")],
  "probe_success per check over time: green is passing, red is failing.",
  options={"showValue":"never","mergeValues":True,"rowHeight":0.85,"legend":{"showLegend":False},"alignValue":"center"},
  defaults={"mappings":[{"type":"value","options":{"0":{"text":"FAIL","color":"red","index":0},"1":{"text":"OK","color":"green","index":1}}}],"color":{"mode":"thresholds"},"thresholds":{"mode":"absolute","steps":[{"color":"red","value":None},{"color":"green","value":1}]},"custom":{"fillOpacity":80,"lineWidth":0}})
y+=7
BK='probe_all_duration_seconds_bucket'
panel("timeseries","Probe latency p50 and p95",0,y,12,8,
  [tgt(f'histogram_quantile(0.5, sum by (le)(rate({BK}[$__rate_interval]) {SMJ}))',"p50","A"),
   tgt(f'histogram_quantile(0.95, sum by (le)(rate({BK}[$__rate_interval]) {SMJ}))',"p95","B"),
   tgt(f'histogram_quantile(0.95, sum by (le,region)(rate({BK}[$__rate_interval]) {SMJ}))',"p95 {{region}}","C")],
  "Latency quantiles from the probe_all_duration_seconds histogram, across the selected checks and regions, plus p95 per region.",
  options={"legend":{"displayMode":"table","placement":"bottom","calcs":["mean","max","lastNotNull"],"showLegend":True},"tooltip":{"mode":"multi","sort":"desc"}},
  defaults={"unit":"s","decimals":3,"custom":dict(ts_custom,fillOpacity=10),"color":{"mode":"palette-classic"}},
  overrides=[ov("p50",[{"id":"color","value":{"mode":"fixed","fixedColor":"green"}}]),ov("p95",[{"id":"color","value":{"mode":"fixed","fixedColor":"orange"}}])])
panel("heatmap","Probe latency distribution",12,y,12,8,
  [tgt(f'sum by (le)(increase({BK}[$__rate_interval]) {SMJ})',"{{le}}","A",fmt="heatmap")],
  "Distribution of probe durations over time from the histogram buckets of probe_all_duration_seconds.",
  options={"calculate":False,"yAxis":{"axisPlacement":"left","unit":"s"},"rowsFrame":{"layout":"le"},"color":{"mode":"scheme","scheme":"Spectral","scale":"exponential","exponent":0.5,"steps":64,"reverse":False},"cellGap":1,"legend":{"show":True},"tooltip":{"mode":"single","showColorScale":True}},
  defaults={"custom":{"hideFrom":{"legend":False,"tooltip":False,"viz":False},"scaleDistribution":{"type":"linear"}}})
y+=8
panel("grafana-polystat-panel","Check health",0,y,10,9,
  [tgt(f'100 * avg by (job)(probe_success {SMJ})',"{{job}}","A",instant=True)],
  "One hexagon per check, coloured by current success percentage.",
  options={"globalShape":"hexagon_pointed_top","globalUnitFormat":"percent","globalDecimals":0,"globalOperator":"lastNotNull","globalAutoScaleFonts":False,"globalLabelFontSize":11,"globalValueFontSize":13,"globalCompositeValueFontSize":13,"globalTextFontAutoColorEnabled":True,"globalGradientsEnabled":True,"globalDisplayMode":"all","globalShowValueEnabled":True,
           "autoSizePolygons":True,"autoSizeColumns":True,"autoSizeRows":True,"sortByField":"name","sortByDirection":1,"globalTooltipsEnabled":True,"globalTooltipsShowValueEnabled":True,"globalTooltipsShowTimestampEnabled":False,"tooltipDisplayMode":"all",
           "globalThresholdsConfig":[{"value":0,"state":2,"color":"#F2495C"},{"value":95,"state":1,"color":"#FF9830"},{"value":99,"state":0,"color":"#73BF69"}],
           "globalFillColor":"rgba(10, 67, 124, 0.4)","globalPolygonBorderColor":"rgba(255,255,255,0.2)","globalPolygonBorderSize":2,"ellipseEnabled":True,"ellipseCharacters":13,
           "compositeConfig":{"composites":[],"enabled":True,"animationSpeed":"1500"},"overrideConfig":{"overrides":[]}},
  defaults={"unit":"percent","color":{"mode":"thresholds"}})
panel("logs","Recent check results",10,y,14,9,
  [dict(tgt('{source="synthetic-monitoring-agent", job=~"$check", region=~"$region"}',ref="A",ds=LOKI),queryType="range")],
  "Latest result lines from the probe agent for the selected checks. A failed check is logged at error level with probe_success=0 on the stream.",ds=LOKI,
  options={"showTime":True,"showLabels":False,"showCommonLabels":False,"wrapLogMessage":True,"prettifyLogMessage":False,"enableLogDetails":True,"sortOrder":"Descending","dedupStrategy":"none"})
y+=9

# ---- templating
def qvar(name,label,query,multi=True,ds=PROM,regex="",desc=""):
    return {"name":name,"label":label,"type":"query","datasource":ds,"query":{"query":query,"refId":"v","qryType":1} ,"definition":query,"refresh":2,"sort":1,
            "multi":multi,"includeAll":True,"allValue":".*","current":{"selected":True,"text":["All"],"value":["$__all"]},"regex":regex,"hide":0,"options":[]}
templating=[
 qvar("host","Host",'label_values({__name__=~"node_network_receive_bytes_total|windows_net_bytes_received_total"},instance)'),
 qvar("device","Interface",'label_values(node_network_receive_bytes_total{instance=~"$host"},device)'),
 qvar("region","Probe region",'label_values(sm_check_info,region)'),
 qvar("check","Check",'label_values(sm_check_info{region=~"$region"},job)'),
]
dash={
 "apiVersion":"dashboard.grafana.app/v1beta1","kind":"Dashboard",
 "metadata":{"name":"nos-interface-performance","annotations":{"grafana.app/folder":"netobs-showcase"}},
 "spec":{"uid":"nos-interface-performance","title":"Network Observability - Interface and Path Performance","description":"Interface throughput, utilisation, errors and link state across the server fleet, plus end-to-end path reachability and latency from the external probes.",
   "tags":["netobs-showcase"],"timezone":"browser","schemaVersion":41,"editable":True,"graphTooltip":1,"refresh":"1m",
   "time":{"from":"now-30m","to":"now"},"templating":{"list":templating},"annotations":{"list":[]},
   "links":[{"type":"dashboards","title":"Network Observability","tags":["netobs-showcase"],"asDropdown":True,"includeVars":False,"keepTime":True,"icon":"external link","targetBlank":False}],
   "panels":panels}}
json.dump(dash,open(OUT,"w"),indent=1)
print("panels",len(panels))
