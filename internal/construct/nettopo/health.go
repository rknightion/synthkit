// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// unreachableSubset returns true when the device identified by key is in the
// unreachable subset for the current fault intensity. Subset size scales linearly
// with intensity: at intensity=1.0 all devices are affected; at 0.5 half are.
// Deterministic: uses a stable FNV hash of the device key, no RNG.
func unreachableSubset(key string, intensity float64) bool {
	if intensity <= 0 {
		return false
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	// Map hash to [0,1); if below intensity threshold → unreachable.
	u := float64(h.Sum64()) / (float64(math.MaxUint64) + 1)
	return u < intensity
}

// walkerDegradedSubset returns true when the module is in the degraded subset.
// At intensity=1.0 all modules are affected; at 0.5 half are (hash-deterministic).
func walkerDegradedSubset(key string, intensity float64) bool {
	return unreachableSubset(key, intensity) // same deterministic hash logic
}

// expBuckets returns a Prometheus-style exponential bucket series:
// {start, start*factor, start*factor^2, …} of length count.
// Mirrors prometheus.ExponentialBuckets.
func expBuckets(start, factor float64, count int) []float64 {
	b := make([]float64, count)
	v := start
	for i := range b {
		b[i] = v
		v *= factor
	}
	return b
}

// walkerOutcomeProtos is the intersection set: walker_outcome_total only covers
// these four protocols (not bgp, which has its own family).
var walkerOutcomeProtos = map[string]bool{
	ProtoLLDP: true,
	ProtoCDP:  true,
	ProtoOSPF: true,
	ProtoFDB:  true,
}

// bgpVendorWalkers maps dominant-vendor → BGP walker label.
var bgpVendorWalkers = map[string]string{
	"cisco":   "vendor_cisco",
	"arista":  "vendor_arista",
	"juniper": "vendor_juniper",
	"nokia":   "vendor_nokia",
}

// dominantVendor returns the most frequent vendor in the graph's device list.
// Falls back to "arista" when the graph is empty.
func dominantVendor(devices []Device) string {
	counts := map[string]int{}
	for _, d := range devices {
		counts[d.Vendor]++
	}
	best, bestN := "arista", 0
	for v, n := range counts {
		if n > bestN {
			best, bestN = v, n
		}
	}
	return best
}

// buildHealth accumulates the discovery-health, graph-freshness, process self-observability,
// and the presence-gated sub-families (SNMP session pool, federation hub/spoke, OTLP-push)
// into c.st.  Tick collects c.st once after both buildData and buildHealth return.
func (c *Construct) buildHealth(now time.Time, eng *shape.Engine) {
	base := c.baseLabels()
	nDev := len(c.graph.Devices)
	nEdge := len(c.graph.Edges)
	// modules = declared protocols + "snmp" (the system walk).
	modules := append(append([]string{}, c.rc.protocols...), "snmp")

	// ── resolve fault states (scoped by exporter instance) ────────────────────

	slowActive, slowInten := evalFault(eng, now, "nettopo_discovery_slow", c.rc.instance)
	walkerActive, walkerInten := evalFault(eng, now, "nettopo_walker_degraded", c.rc.instance)
	authActive, authInten := evalFault(eng, now, "nettopo_auth_failures", c.rc.instance)
	unreachActive, unreachInten := evalFault(eng, now, "nettopo_devices_unreachable", c.rc.instance)
	spokeDownActive, _ := evalFault(eng, now, "nettopo_spoke_down", c.rc.instance)

	// ── discovery cycle health ─────────────────────────────────────────────────

	// Compute unreachable device count once (shared by health and data paths).
	// Subset is deterministic via hash; size scales linearly with intensity.
	unreachCount := 0
	if unreachActive {
		for _, dev := range c.graph.Devices {
			if unreachableSubset(dev.ID, unreachInten) {
				unreachCount++
			}
		}
	}

	// network_topology_discovery_devices_total — re-set each tick (gauge).
	successCount := float64(nDev - unreachCount)
	if successCount < 0 {
		successCount = 0
	}
	c.st.Set("network_topology_discovery_devices_total",
		lbls(base, "status", "success", "reason", "n/a"),
		successCount)

	// Fault: devices_unreachable → failed/unreachable row
	if unreachActive && unreachCount > 0 {
		c.st.Set("network_topology_discovery_devices_total",
			lbls(base, "status", "failed", "reason", "unreachable"),
			float64(unreachCount))
	}

	// Fault: auth_failures → failed/auth_failed row (fraction of devices)
	if authActive {
		authCount := math.Max(1, math.Round(float64(nDev)*authInten*0.3))
		c.st.Set("network_topology_discovery_devices_total",
			lbls(base, "status", "failed", "reason", "auth_failed"),
			authCount)
	}

	// network_topology_discovery_cycle_duration_seconds — one Observe per tick.
	// Under discovery_slow: duration inflated by FailFactor(magAt1≈10).
	cycleMult := 1.0
	if slowActive {
		cycleMult = 1.0 + (10.0-1.0)*slowInten
	}
	cycleDur := (0.5 + float64(nDev)*0.08) * c.seriesVar(eng, now, "cycle_duration", 0.15) * cycleMult
	c.st.Observe("network_topology_discovery_cycle_duration_seconds",
		base,
		expBuckets(0.5, 2, 10),
		state.LEBare,
		cycleDur)

	// network_topology_discovery_module_duration_seconds — one Observe per module.
	for _, mod := range modules {
		modDur := 0.05 * c.seriesVar(eng, now, "mod_dur|"+mod, 0.2)
		c.st.Observe("network_topology_discovery_module_duration_seconds",
			lbls(base, "module", mod),
			expBuckets(0.05, 2, 10),
			state.LEBare,
			modDur)
	}

	// network_topology_snmp_walks_total — cumulative counter.
	walksOK := float64(nDev * len(modules))
	c.st.Add("network_topology_snmp_walks_total",
		lbls(base, "status", "ok", "reason", "n/a"),
		walksOK*c.seriesVar(eng, now, "snmp_walks_ok", 0.05))

	// Fault: auth_failures + devices_unreachable → timeout walks
	if authActive || unreachActive {
		timeoutInten := authInten
		if unreachInten > timeoutInten {
			timeoutInten = unreachInten
		}
		timeoutWalks := math.Max(1, math.Round(float64(nDev)*timeoutInten*0.25))
		c.st.Add("network_topology_snmp_walks_total",
			lbls(base, "status", "timeout", "reason", "n/a"),
			timeoutWalks)
	}

	// network_topology_credential_trials_total.
	c.st.Add("network_topology_credential_trials_total",
		lbls(base, "status", "ok"),
		float64(nDev)*c.seriesVar(eng, now, "cred_trials_ok", 0.05))

	// Fault: auth_failures → failed credential trials
	if authActive {
		failedTrials := math.Max(1, math.Round(float64(nDev)*authInten*0.4))
		c.st.Add("network_topology_credential_trials_total",
			lbls(base, "status", "failed"),
			failedTrials)
	}

	// network_topology_walker_outcome_total — for protocols in intersection(declared, {lldp,cdp,ospf,fdb}).
	for _, proto := range c.rc.protocols {
		if !walkerOutcomeProtos[proto] {
			continue
		}
		c.st.Add("network_topology_walker_outcome_total",
			lbls(base, "walker", proto, "outcome", "edges"),
			1)

		// Fault: walker_degraded → error + walker_drift outcomes for affected walkers
		if walkerActive && walkerDegradedSubset(proto, walkerInten) {
			c.st.Add("network_topology_walker_outcome_total",
				lbls(base, "walker", proto, "outcome", "error"),
				1)
			c.st.Add("network_topology_walker_outcome_total",
				lbls(base, "walker", proto, "outcome", "walker_drift"),
				1)
		}
	}
	// mib_unimplemented for protocols NOT in the walker set (a small realistic counter).
	for _, proto := range c.rc.protocols {
		if walkerOutcomeProtos[proto] {
			continue
		}
		c.st.Add("network_topology_walker_outcome_total",
			lbls(base, "walker", proto, "outcome", "mib_unimplemented"),
			0) // seed at zero; we just want the series present
	}

	// network_topology_bgp_walker_outcome_total — only when "bgp" ∈ protocols.
	if c.rc.protoSet[ProtoBGP] {
		vendor := dominantVendor(c.graph.Devices)
		walker, ok := bgpVendorWalkers[vendor]
		if !ok {
			walker = "rfc4273"
		}
		c.st.Add("network_topology_bgp_walker_outcome_total",
			lbls(base, "walker", walker, "outcome", "edges"),
			1)
	}

	// network_topology_module_last_status — GAUGE, 0=ok for each module; ≥1 under walker_degraded.
	for _, mod := range modules {
		status := 0.0
		if walkerActive && walkerDegradedSubset(mod, walkerInten) {
			// intensity < 0.7 → degraded(1); intensity >= 0.7 → hard-failed(2)
			if walkerInten >= 0.7 {
				status = 2.0
			} else {
				status = 1.0
			}
		}
		c.st.Set("network_topology_module_last_status",
			lbls(base, "module", mod),
			status)
	}

	// Fault: walker_degraded → fault-only decode/degraded/hard_fail families (ABSENT when inactive).
	// These are NEVER touched when walkerActive is false — preserving the absence invariant.
	if walkerActive {
		// Emit one representative entry per fault-only family so dashboards get a signal.
		// Use the first module that is in the degraded subset for the oid/module keys.
		faultMod := "snmp" // fallback
		for _, m := range modules {
			if walkerDegradedSubset(m, walkerInten) {
				faultMod = m
				break
			}
		}

		// Per-module (oid, reason) pairs for discovery_decode_issues_total.
		// Each tuple is sourced from network-topology-exporter docs/metrics.md #99 (2026-06-17):
		//   lldp  oid="1.0.8802.1.1.2.1.4.1" (LLDP remote table, LLDP-MIB lldpRemTable)
		//         reason ∈ {chassis_subtype_invalid, port_subtype_invalid, chassis_mac_bad_length, port_mac_bad_length, chassis_addr_malformed}
		//   cdp   oid="1.3.6.1.4.1.9.9.23.1.2.1" (Cisco CISCO-CDP-MIB cdpCacheTable)
		//         reason ∈ {index_unparseable, empty_device_id}
		//   ospf  oid="1.3.6.1.2.1.14.10" (OSPF-MIB ospfNbrTable)
		//         reason ∈ {oid_suffix_malformed, nbr_ip_undecodable}
		//   fdb   oid="1.3.6.1.2.1.17.1.4" (dot1dBasePortTable, B-MIB — oidBasePortTable in exporter)
		//         reason ∈ {bridge_port_index_invalid, ifindex_unmapped}
		// bgp/isis/snmp/mpls_te have no per-row decode_issues surface in the exporter → skip.
		// For absent modules fall through to emitting degraded/hard_fail only (module-generic).
		type decodeEntry struct{ oid, reason string }
		moduleDecodeIssues := map[string]decodeEntry{
			ProtoLLDP: {"1.0.8802.1.1.2.1.4.1", "chassis_subtype_invalid"},
			ProtoCDP:  {"1.3.6.1.4.1.9.9.23.1.2.1", "index_unparseable"},
			ProtoOSPF: {"1.3.6.1.2.1.14.10", "oid_suffix_malformed"},
			ProtoFDB:  {"1.3.6.1.2.1.17.1.4", "ifindex_unmapped"},
		}
		if entry, ok := moduleDecodeIssues[faultMod]; ok {
			c.st.Add("network_topology_discovery_decode_issues_total",
				lbls(base, "module", faultMod, "oid", entry.oid, "reason", entry.reason),
				math.Max(1, math.Round(walkerInten*5)))
		}
		// degraded: source network-topology-exporter internal/discovery/discovery.go
		//   DegradedReasonRequiredTablePartialDecode → "required_table_partial_decode"
		c.st.Add("network_topology_discovery_degraded_total",
			lbls(base, "module", faultMod, "reason", "required_table_partial_decode"),
			math.Max(1, math.Round(walkerInten*3)))
		// hard_fail: source network-topology-exporter internal/discovery/snmp/pdu.go
		//   EvaluateRequiredTablePolicy → "required_table_no_valid_rows"
		c.st.Add("network_topology_discovery_hard_fail_total",
			lbls(base, "module", faultMod, "reason", "required_table_no_valid_rows"),
			math.Max(1, math.Round(walkerInten*2)))
	}

	// network_topology_system_walk_anomaly_total — nearly always 0.
	c.st.Add("network_topology_system_walk_anomaly_total",
		lbls(base, "reason", "empty_sysname"),
		0)

	// network_topology_cycle_budget_skips_total — 0 normally; non-zero under discovery_slow.
	skipDelta := 0.0
	if slowActive {
		skipDelta = math.Max(1, math.Round(slowInten*3))
	}
	c.st.Add("network_topology_cycle_budget_skips_total", base, skipDelta)

	// network_topology_fdb_suppressed_macs_total — small if fdb in protocols, else 0.
	if c.rc.protoSet[ProtoFDB] {
		c.st.Add("network_topology_fdb_suppressed_macs_total", base,
			float64(nDev)*0.5*c.seriesVar(eng, now, "fdb_suppressed", 0.2))
	} else {
		c.st.Add("network_topology_fdb_suppressed_macs_total", base, 0)
	}

	// network_topology_snmp_rate_limit_wait_seconds — tiny values.
	snmpRLVal := 0.002 * c.seriesVar(eng, now, "snmp_rl_wait", 0.3)
	c.st.Observe("network_topology_snmp_rate_limit_wait_seconds",
		base,
		expBuckets(0.001, 2, 12),
		state.LEBare,
		snmpRLVal)

	// ── graph freshness ────────────────────────────────────────────────────────

	// graph_stale=1 only on the very first live cycle (serving the cold-start snapshot),
	// 0 thereafter — mirrors the exporter clearing stale after its first discovery cycle.
	// buildData runs before buildHealth and sets bootTime, so now==bootTime ⇒ first tick.
	// Also forced to 1 under nettopo_discovery_slow (cycle did not complete on time).
	stale := 0.0
	if (!c.bootTime.IsZero() && now.Equal(c.bootTime)) || slowActive {
		stale = 1
	}
	c.st.Set("network_topology_graph_stale", base, stale)
	c.st.Set("network_topology_snapshot_last_written_timestamp_seconds", base, float64(now.Unix()))
	c.st.Set("network_topology_snapshot_loaded_devices_total", base, float64(nDev))
	c.st.Set("network_topology_snapshot_queue_depth", base, 0)
	c.st.Add("network_topology_snapshot_drops_total", lbls(base, "reason", "queue_full"), 0)
	c.st.Add("network_topology_snapshot_drops_total", lbls(base, "reason", "write_in_flight"), 0)

	// ── process self-observability ─────────────────────────────────────────────

	// network_topology_metrics_render_duration_seconds.
	seriesCount := float64(2*nDev + nEdge + 20)
	renderDur := 0.001 * seriesCount / 100.0 * c.seriesVar(eng, now, "render_dur", 0.15)
	c.st.Observe("network_topology_metrics_render_duration_seconds",
		base,
		expBuckets(0.001, 2, 16),
		state.LEBare,
		renderDur)

	// network_topology_metrics_payload_bytes.
	payloadBytes := float64(1024 + (nDev+nEdge)*200)
	c.st.Observe("network_topology_metrics_payload_bytes",
		base,
		expBuckets(1024, 4, 9),
		state.LEBare,
		payloadBytes*c.seriesVar(eng, now, "payload_bytes", 0.1))

	// network_topology_last_scrape_duration_seconds — small living gauge.
	scrapeDur := 0.005 * c.seriesVar(eng, now, "scrape_dur", 0.2)
	c.st.Set("network_topology_last_scrape_duration_seconds", base, scrapeDur)

	// network_topology_last_scrape_samples_total ≈ 2*nDev + nEdge + ~6.
	c.st.Set("network_topology_last_scrape_samples_total", base,
		float64(2*nDev+nEdge+6)*c.seriesVar(eng, now, "scrape_samples", 0.05))

	// network_topology_goroutines ≈ 20 + slow wander.
	goroutines := 20.0 * c.seriesVar(eng, now, "goroutines", 0.1)
	if goroutines < 5 {
		goroutines = 5
	}
	c.st.Set("network_topology_goroutines", base, goroutines)

	// network_topology_panics_total — always 0.
	c.st.Add("network_topology_panics_total", lbls(base, "site", "discovery_loop"), 0)

	// ── GATED: SNMP session pool ───────────────────────────────────────────────

	if c.rc.sessionPool {
		c.st.Set("network_topology_snmp_session_pool_size", base, float64(nDev))
		c.st.Add("network_topology_snmp_session_pool_hits_total", base,
			float64(nDev)*c.seriesVar(eng, now, "sp_hits", 0.1))
		c.st.Add("network_topology_snmp_session_pool_misses_total", base,
			float64(nDev)*0.1*c.seriesVar(eng, now, "sp_misses", 0.2))
		c.st.Add("network_topology_snmp_session_pool_evictions_total",
			lbls(base, "reason", "idle"), 0)
		c.st.Add("network_topology_snmp_session_pool_evictions_total",
			lbls(base, "reason", "credential_rotation"), 0)
		c.st.Add("network_topology_snmp_session_pool_evictions_total",
			lbls(base, "reason", "connection_error"), 0)
	}

	// ── GATED: federation HUB ─────────────────────────────────────────────────

	if c.rc.role == RoleHub {
		// Under nettopo_spoke_down: determine which spokes are "down" (subset by hash+intensity).
		// At intensity=1.0 the first spoke in the sorted list is always affected.
		// Spoke-down subset uses the same deterministic hash as unreachableSubset, keyed on spoke_id.
		// Spoke liveness: spoke_up=1 normally, 0 for down spokes; stale last_push for down spokes.
		for _, sp := range c.rc.spokes {
			spokeIsDown := spokeDownActive && unreachableSubset(sp, 1.0) // at any intensity, first spoke is down
			if spokeIsDown {
				c.st.Set("network_topology_federation_spoke_up",
					lbls(base, "spoke_id", sp), 0)
				// Stale last_push: set to a past timestamp (2 cycles ago).
				c.st.Set("network_topology_federation_spoke_last_push_timestamp_seconds",
					lbls(base, "spoke_id", sp), float64(now.Unix()-120))
			} else {
				c.st.Set("network_topology_federation_spoke_up",
					lbls(base, "spoke_id", sp), 1)
				c.st.Set("network_topology_federation_spoke_last_push_timestamp_seconds",
					lbls(base, "spoke_id", sp), float64(now.Unix()))
			}
		}
		c.st.Set("network_topology_hub_oos_unmatched_total", base, 0)
		c.st.Add("network_topology_graph_updates_rejected_total",
			lbls(base, "reason", "size_budget_exceeded"), 0)
		c.st.Add("network_topology_graph_updates_rejected_total",
			lbls(base, "reason", "invalid_label_key"), 0)
		c.st.Add("network_topology_graph_updates_rejected_total",
			lbls(base, "reason", "structural_invalid"), 0)

		// Fault: spoke_down → increment rejected updates (dropped/invalid pushes from the down spoke)
		if spokeDownActive {
			c.st.Add("network_topology_graph_updates_rejected_total",
				lbls(base, "reason", "stale_generation"), 2)
		}

		// boundary_observation_info — emit oosCount synthetic boundary observations. The
		// reporting device is a REAL in-graph device (the LD-15 recording rule joins on it),
		// observing an out-of-scope external neighbour. peer_a/peer_b are the two endpoints in
		// canonical (alphabetically-smaller-first) order, matching the exporter's contract.
		for i := 0; i < c.rc.oosCount; i++ {
			reportingDev := c.rc.instance // fallback when the graph has no devices
			srcPort := fmt.Sprintf("Ethernet%d", i+1)
			if nDev > 0 {
				dev := c.graph.Devices[i%nDev]
				reportingDev = dev.ID
			}
			neighbour := fmt.Sprintf("ext-peer-%02d", i+1) // out-of-scope (unpolled) neighbour
			peerA, peerB := reportingDev, neighbour
			if peerB < peerA {
				peerA, peerB = peerB, peerA
			}
			c.st.Set("network_topology_boundary_observation_info",
				lbls(base,
					"peer_a", peerA,
					"peer_b", peerB,
					"reporting_device", reportingDev,
					"src_port", srcPort,
					"proto", ProtoLLDP,
				), 1)
		}
	}

	// ── GATED: federation SPOKE ──────────────────────────────────────────────

	if c.rc.role == RoleSpoke {
		// Under nettopo_spoke_down: increment push_failures_total instead of 0.
		pushFailures := 0.0
		if spokeDownActive {
			pushFailures = 3 // realistic: a few failed push attempts per cycle
		}
		c.st.Add("network_topology_federation_spoke_push_failures_total", base, pushFailures)
		c.st.Set("network_topology_federation_spoke_push_last_success_unix", base,
			float64(now.Unix()))
		c.st.Add("network_topology_federation_spoke_push_drops_total",
			lbls(base, "reason", "superseded"), 0)
		c.st.Add("network_topology_federation_spoke_push_drops_total",
			lbls(base, "reason", "shutdown"), 0)
		c.st.Set("network_topology_federation_spoke_push_queue_depth", base, 0)
	}

	// ── GATED: OTLP output ─────────────────────────────────────────────────────

	if c.rc.otlpOutput {
		c.st.Add("network_topology_otlp_push_total",
			lbls(base, "status", "ok", "reason", "n/a"),
			1*c.seriesVar(eng, now, "otlp_ok", 0.05))
	}
}

// evalFault is a nil-safe wrapper around eng.Eval that returns (false, 0) when eng is nil.
func evalFault(eng *shape.Engine, now time.Time, mode, scope string) (bool, float64) {
	if eng == nil {
		return false, 0
	}
	return eng.Eval(now, mode, scope)
}

// lbls returns a new label map that starts as a copy of base and then has the given
// key=value pairs set (pairs must be even-length: k0,v0,k1,v1,…).
func lbls(base map[string]string, kvs ...string) map[string]string {
	m := make(map[string]string, len(base)+len(kvs)/2)
	for k, v := range base {
		m[k] = v
	}
	for i := 0; i+1 < len(kvs); i += 2 {
		m[kvs[i]] = kvs[i+1]
	}
	return m
}
