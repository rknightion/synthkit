# dashboards/showcase - plugin-heavy dashboards on the synthetic estate

Hand-designed dashboards that use community panel plugins (Flow, Business Charts, Polystat,
ESnet networkmap/matrix, weathermap, Sankey, Business Text) which the Go `dashboard/` builder does
not model. They target the same synthetic telemetry as `examples/` and push to the target stack.

## Source and output

Each `<uid>.gen.py` is the source; it writes `<uid>.json` beside itself. Edit the generator,
never the JSON, then regenerate:

```bash
python3 dashboards/showcase/netobs/<uid>.gen.py
```

## netobs - network operations set

Nine dashboards in folder uid `netobs-showcase` ("Global Network Operations"), framed as one
customer's production NOC. Data comes from the `netobs-enterprise`, `netobs-global`,
`netobs-spoke`, `hostfleet` and `synthetic-checks` blueprints; probe panels need the SM checks
registered with `sm-provision` first. `BRIEF.md` is the build brief: data inventory, plugin
findings and verification rules.

| uid | Dashboard | Required plugins |
|---|---|---|
| `nos-noc-wallboard` | NOC Wallboard | andrewbmchugh-flow-panel, grafana-polystat-panel, grafana-clock-panel |
| `nos-wan-maps` | Global WAN and Federation Maps | esnet-networkmap-panel, tamirsuliman-weathermap-panel |
| `nos-topology-explorer` | Topology Explorer | volkovlabs-echarts-panel, netsage-sankey-panel, esnet-matrix-panel, marcusolsson-treemap-panel |
| `nos-network-analytics` | Network Analytics | volkovlabs-echarts-panel |
| `nos-interface-performance` | Interface and Path Performance | marcusolsson-dynamictext-panel, grafana-polystat-panel |
| `nos-change-and-discovery` | Change Intelligence and Discovery Health | marcusolsson-dynamictext-panel, grafana-polystat-panel |
| `nos-noc-triage` | NOC Triage | built-ins only (stat, table, state-timeline, logs) |
| `nos-device-investigation` | Device Investigation (vars `exporter`, `device`) | volkovlabs-echarts-panel, marcusolsson-dynamictext-panel |
| `nos-site-investigation` | WAN Site Investigation (var `site`) | volkovlabs-echarts-panel, marcusolsson-dynamictext-panel |

Push the folder first, then each dashboard:

```bash
gcx --context <target-stack> resources push -p dashboards/showcase/netobs/folder.json
gcx --context <target-stack> resources push -p dashboards/showcase/netobs/<uid>.json
```
