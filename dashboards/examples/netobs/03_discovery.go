// SPDX-License-Identifier: AGPL-3.0-only

package netobs

import "github.com/rknightion/synthkit/dashboard"

// Discovery is the SNMP discovery-health board: per-module status and latency, walker outcomes,
// SNMP walk and credential results, session pooling and the exporter's own process health.
// uid: netobs-discovery. The fault-only families (decode issues, degraded, hard fail) sit in a
// conditional row that renders only when they carry data.
func Discovery(_ *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("netobs-discovery", "Network Observability - Discovery health")
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	instanceVar(&d)

	series(&d, "cycle-p95", "Discovery cycle p95", "s",
		dashboard.ClassicHistogramQuantile(0.95, "network_topology_discovery_cycle_duration_seconds", sel(""), []string{"instance"}),
		"{{instance}}")
	health(&d, "walk-ok", "SNMP walk success", "percentunit",
		`sum(rate(network_topology_snmp_walks_total`+sel(`status="ok"`)+`[$__rate_interval])) / sum(rate(network_topology_snmp_walks_total`+sel("")+`[$__rate_interval]))`,
		[]dashboard.Threshold{{Value: 0, Color: "red"}, {Value: 0.95, Color: "orange"}, {Value: 0.99, Color: "green"}})
	health(&d, "pool-hit", "SNMP session pool hit ratio", "percentunit",
		`sum(rate(network_topology_snmp_session_pool_hits_total`+sel("")+`[$__rate_interval])) / (sum(rate(network_topology_snmp_session_pool_hits_total`+sel("")+`[$__rate_interval])) + sum(rate(network_topology_snmp_session_pool_misses_total`+sel("")+`[$__rate_interval])))`,
		[]dashboard.Threshold{{Value: 0, Color: "orange"}, {Value: 0.8, Color: "green"}})
	health(&d, "budget-skips", "Cycle budget skips (1h)", "short",
		`sum(increase(network_topology_cycle_budget_skips_total`+sel("")+`[1h])) or vector(0)`, warnZero)
	health(&d, "panics", "Recovered panics (1h)", "short",
		`sum(increase(network_topology_panics_total`+sel("")+`[1h])) or vector(0)`, goodZero)
	dashboard.AddPanel(&d, "module-status", dashboard.StateTimelinePanel("Module status (0 ok, 1 degraded, 2 hard-failed)",
		dashboard.PromTarget(`max by (instance, module) (network_topology_module_last_status`+sel("")+`)`, "{{instance}} {{module}}")))
	series(&d, "module-p95", "Module duration p95", "s",
		dashboard.ClassicHistogramQuantile(0.95, "network_topology_discovery_module_duration_seconds", sel(""), []string{"module"}),
		"{{module}}")
	series(&d, "walkers", "Neighbour walker outcomes", "ops",
		`sum by (walker, outcome) (rate(network_topology_walker_outcome_total`+sel("")+`[$__rate_interval]))`, "{{walker}} {{outcome}}")
	series(&d, "bgp-walkers", "BGP walker outcomes", "ops",
		`sum by (walker, outcome) (rate(network_topology_bgp_walker_outcome_total`+sel("")+`[$__rate_interval]))`, "{{walker}} {{outcome}}")
	series(&d, "walks", "SNMP walks by status", "ops",
		`sum by (status, reason) (rate(network_topology_snmp_walks_total`+sel("")+`[$__rate_interval]))`, "{{status}} {{reason}}")
	series(&d, "creds", "Credential trials by status", "ops",
		`sum by (status) (rate(network_topology_credential_trials_total`+sel("")+`[$__rate_interval]))`, "{{status}}")
	series(&d, "devices-status", "Devices by discovery status", "short",
		`sum by (status, reason) (network_topology_discovery_devices_total`+sel("")+`)`, "{{status}} {{reason}}")
	series(&d, "anomalies", "System walk anomalies and suppressed FDB MACs", "ops",
		`sum by (reason) (rate(network_topology_system_walk_anomaly_total`+sel("")+`[$__rate_interval])) or label_replace(sum(rate(network_topology_fdb_suppressed_macs_total`+sel("")+`[$__rate_interval])), "reason", "fdb_suppressed_macs", "", "")`,
		"{{reason}}")
	series(&d, "pool-size", "SNMP session pool size", "short",
		`sum by (instance) (network_topology_snmp_session_pool_size`+sel("")+`)`, "{{instance}}")
	series(&d, "pool-evictions", "Session pool evictions", "ops",
		`sum by (reason) (rate(network_topology_snmp_session_pool_evictions_total`+sel("")+`[$__rate_interval]))`, "{{reason}}")
	series(&d, "rate-limit", "SNMP rate-limit wait p95", "s",
		dashboard.ClassicHistogramQuantile(0.95, "network_topology_snmp_rate_limit_wait_seconds", sel(""), []string{"instance"}),
		"{{instance}}")

	series(&d, "decode", "Decode issues", "ops",
		`sum by (module, reason) (rate(network_topology_discovery_decode_issues_total`+sel("")+`[$__rate_interval]))`, "{{module}} {{reason}}")
	series(&d, "degraded", "Degraded walks", "ops",
		`sum by (module, reason) (rate(network_topology_discovery_degraded_total`+sel("")+`[$__rate_interval]))`, "{{module}} {{reason}}")
	series(&d, "hard-fail", "Hard failures", "ops",
		`sum by (module, reason) (rate(network_topology_discovery_hard_fail_total`+sel("")+`[$__rate_interval]))`, "{{module}} {{reason}}")

	series(&d, "goroutines", "Goroutines", "short",
		`sum by (instance) (network_topology_goroutines`+sel("")+`)`, "{{instance}}")
	series(&d, "scrape", "Last scrape duration", "s",
		`max by (instance) (network_topology_last_scrape_duration_seconds`+sel("")+`)`, "{{instance}}")
	series(&d, "payload", "Metrics payload p95", "bytes",
		dashboard.ClassicHistogramQuantile(0.95, "network_topology_metrics_payload_bytes", sel(""), []string{"instance"}),
		"{{instance}}")
	series(&d, "snapshot", "Snapshot queue depth", "short",
		`sum by (instance) (network_topology_snapshot_queue_depth`+sel("")+`)`, "{{instance}}")

	dashboard.WithRows(&d,
		dashboard.Section("Summary",
			dashboard.Stat("walk-ok"), dashboard.Stat("pool-hit"), dashboard.Stat("budget-skips"), dashboard.Stat("panics"),
			dashboard.Full("cycle-p95")),
		dashboard.Section("Modules", dashboard.Full("module-status"), dashboard.Full("module-p95")),
		dashboard.Section("Walkers and SNMP",
			dashboard.Half("walkers"), dashboard.Half("bgp-walkers"),
			dashboard.Third("walks"), dashboard.Third("creds"), dashboard.Third("devices-status"),
			dashboard.Half("anomalies"), dashboard.Half("rate-limit")),
		dashboard.Section("Session pool", dashboard.Half("pool-size"), dashboard.Half("pool-evictions")),
		dashboard.ConditionalSection("Discovery faults",
			dashboard.Third("decode"), dashboard.Third("degraded"), dashboard.Third("hard-fail")),
		dashboard.CollapsedSection("Exporter process",
			dashboard.Half("goroutines"), dashboard.Half("scrape"), dashboard.Half("payload"), dashboard.Half("snapshot")),
	)
	return d, nil
}
