// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// Churn model: cold-start discovery burst + real edge/device transitions drive change_total.
const (
	// changeLogSampleCap bounds the topology-change log lines emitted per tick; the
	// change_total counter still carries the true volume (a cold-start burst can add
	// hundreds of edges at once — we log only a sample).
	changeLogSampleCap = 6
)

// hashUnit maps key to a stable uniform value in [0,1).
func hashUnit(key string) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return float64(h.Sum64()) / (float64(math.MaxUint64) + 1)
}

// UptimeBaseSeconds returns a deterministic per-device uptime base in [uptimeBaseMinSecs,
// uptimeBaseMaxSecs] seconds, derived from a stable FNV hash of deviceID+seed. Exported for
// use by graph.go and graph_test.go.
func UptimeBaseSeconds(deviceID, seed string) float64 {
	h := fnv.New64a()
	h.Write([]byte(deviceID))
	h.Write([]byte{0})
	h.Write([]byte(seed))
	v := h.Sum64()
	// Map hash uniformly into [uptimeBaseMinSecs, uptimeBaseMaxSecs] using modulo
	// (slight bias is fine for synthetic data).
	span := uint64(uptimeBaseMaxSecs-uptimeBaseMinSecs) + 1
	return uptimeBaseMinSecs + float64(v%span)
}

// edgeVisible returns true iff the edge should currently appear in edge_info gauges and
// in transition-counting logic. An edge is visible only when it is not in a flap-down
// window AND neither endpoint device is in a reboot-down window. This single predicate
// drives BOTH edge_info emission and change_total transition counting, ensuring the
// counter↔inventory invariant is exact (no double-count gap).
func edgeVisible(edge Edge, seed string, bootTime, t time.Time) bool {
	return !edgeDown(edgeKey(edge), seed, bootTime, t) &&
		!deviceRebooting(edge.SrcDevice, seed, bootTime, t) &&
		!deviceRebooting(edge.DstDevice, seed, bootTime, t)
}

// buildData accumulates the topology DATA families into c.st (device inventory, edge_info,
// graph totals, change/conflict counters, out-of-scope/boundary observations) and returns
// the Loki change/conflict log streams for this tick.
func (c *Construct) buildData(now time.Time, eng *shape.Engine) []loki.Stream {
	base := c.baseLabels()

	// Anchor the cold-start / first-discovery-cycle on the first tick.
	coldStart := c.bootTime.IsZero()
	if coldStart {
		c.bootTime = now
	}

	// Resolve devices_unreachable fault (scoped by instance) for device-side omission.
	unreachActive, unreachInten := evalFault(eng, now, "nettopo_devices_unreachable", c.rc.instance)

	// ── device_info (one info gauge per device) ───────────────────────────────
	// Labels: {job, instance, device_id, vendor, os_version, site} — model is OMITTED.
	// The real exporter has no ENTITY-MIB walk and therefore never populates model.
	// os_version advances as upgrade-reboots accumulate since cold-start (currentOSVersion).
	// Under nettopo_devices_unreachable: omit device_info and device_uptime_seconds for
	// devices in the unreachable subset (mirrors the real exporter dropping polled devices
	// that time out from the reconciled graph). DeleteGauge ensures absence, not value-0.
	for _, dev := range c.graph.Devices {
		devLbls := map[string]string{
			"job":       base["job"],
			"instance":  base["instance"],
			"device_id": dev.ID,
		}
		uptimeLbls := map[string]string{
			"job":       base["job"],
			"instance":  base["instance"],
			"device_id": dev.ID,
		}

		if unreachActive && unreachableSubset(dev.ID, unreachInten) {
			// Device is unreachable — remove from gauge set so it is absent in Collect output.
			c.st.DeleteGauge("network_topology_device_info", map[string]string{
				"job": base["job"], "instance": base["instance"],
				"device_id": dev.ID, "vendor": dev.Vendor,
				"os_version": dev.OSVersion, "site": dev.Site,
			})
			c.st.DeleteGauge("network_topology_device_uptime_seconds", uptimeLbls)
			continue
		}

		osv := currentOSVersion(dev.Vendor, dev.OSVersion, osVersionIndex(dev.ID, c.seed, c.bootTime, now))
		infoLbls := map[string]string{
			"job":        devLbls["job"],
			"instance":   devLbls["instance"],
			"device_id":  dev.ID,
			"vendor":     dev.Vendor,
			"os_version": osv,
			"site":       dev.Site,
		}
		c.st.Set("network_topology_device_info", infoLbls, 1)

		// ── device_uptime_seconds ─────────────────────────────────────────────
		// Starts at the declared/hash-derived base on the cold-start tick (now == bootTime),
		// grows each tick, resets on rare reboots, and is always clamped below the ~497-day
		// sysUpTime wrap ceiling. The base is the blueprint-declared `uptime:` or a stable
		// per-device hash, resolved at graph-build time (see graph.go).
		uptime := deviceUptimeSecs(dev.ID, c.seed, dev.UptimeBaseSecs, c.bootTime, now)
		c.st.Set("network_topology_device_uptime_seconds", uptimeLbls, uptime)
	}

	// ── edge_info (one info gauge per edge) ───────────────────────────────────
	// ONLY the 7 edge keys + job/instance — no confidence/adjacency/metadata creep.
	// Suppressed (absent, not zero) while an edge is in a flap-down window OR while
	// either endpoint device is in a reboot-down window — mirrors the real exporter's
	// reconciliation behaviour where a reconverging link is removed from the topology.
	// DeleteGauge is called so previously-Set series become absent in the next Collect.
	for _, edge := range c.graph.Edges {
		lbls := map[string]string{
			"job":             base["job"],
			"instance":        base["instance"],
			"src_device":      edge.SrcDevice,
			"src_port":        edge.SrcPort,
			"dst_device":      edge.DstDevice,
			"dst_port":        edge.DstPort,
			"discovery_proto": edge.Proto,
			"link_kind":       edge.LinkKind,
			"direction":       edge.Direction,
		}
		if edgeVisible(edge, c.seed, c.bootTime, now) {
			c.st.Set("network_topology_edge_info", lbls, 1)
		} else {
			// Edge is down: remove from gauge set so it is absent in Collect output.
			c.st.DeleteGauge("network_topology_edge_info", lbls)
		}
	}

	// ── graph totals (no extra labels) ────────────────────────────────────────
	c.st.Set("network_topology_graph_devices_total", base, float64(len(c.graph.Devices)))
	c.st.Set("network_topology_graph_edges_total", base, float64(len(c.graph.Edges)))

	// ── out-of-scope neighbours (always emit; 0 is valid) ─────────────────────
	c.st.Set("network_topology_out_of_scope_neighbours_total", base, float64(c.rc.oosCount))

	// ── change_total: cold-start discovery burst, then decay to low steady-state churn ──
	// A real exporter adds its ENTIRE reconciled graph in the first discovery cycle, then
	// settles to rare changes. We mirror that: a one-time "added" burst equal to the
	// per-protocol edge inventory on the first tick, plus a per-tick churn intensity that
	// decays exponentially from a warm-up spike to a low background rate as discovery
	// converges. change_total is cumulative (st.Add — push totals, never deltas, I3).
	protocols := c.rc.protocols
	if len(protocols) == 0 {
		protocols = []string{ProtoLLDP}
	}
	var streams []loki.Stream
	var changeLogLines []loki.Line

	// Group edges by protocol for the cold-start bulk + proportional churn weighting.
	edgesByProto := map[string][]Edge{}
	for _, e := range c.graph.Edges {
		edgesByProto[e.Proto] = append(edgesByProto[e.Proto], e)
	}

	addChange := func(kind, proto string, delta float64, sample *Edge) {
		if delta <= 0 {
			return
		}
		c.st.Add("network_topology_change_total", map[string]string{
			"job": base["job"], "instance": base["instance"],
			"change_kind": kind, "discovery_proto": proto,
		}, delta)
		// The counter carries the true change volume; log lines are a capped per-tick
		// sample so a large cold-start burst doesn't emit hundreds of lines in one tick.
		if sample != nil && len(changeLogLines) < changeLogSampleCap {
			level := "info"
			if kind == "removed" {
				level = "warn"
			}
			body := fmt.Sprintf(
				"level=%s source=network-topology-exporter event=topology_change change_kind=%s proto=%s src_device=%s src_port=%s dst_device=%s dst_port=%s direction=%s",
				level, kind, proto, sample.SrcDevice, sample.SrcPort, sample.DstDevice, sample.DstPort, sample.Direction,
			)
			changeLogLines = append(changeLogLines, loki.Line{T: now, Body: body, Meta: map[string]string{
				"src_device": sample.SrcDevice, "src_port": sample.SrcPort,
				"dst_device": sample.DstDevice, "dst_port": sample.DstPort, "direction": sample.Direction,
			}})
		}
	}

	if coldStart {
		// First live cycle: the whole graph is "discovered" → one big added burst per protocol.
		for _, proto := range protocols {
			edges := edgesByProto[proto]
			if len(edges) == 0 {
				continue
			}
			e := edges[0]
			addChange("added", proto, float64(len(edges)), &e)
		}
	}

	// Ongoing real-transition churn (skipped on the cold-start tick to avoid double-counting
	// with the burst above): for each edge, compare edgeVisible(edge, now) vs
	// edgeVisible(edge, prevBucketTime) using the UNIFIED visibility predicate. A single
	// true→false or false→true transition per edge per tick is counted — no separate
	// flap/reboot branches, no attribution rule, no double-count gap.
	if !coldStart {
		bucketSecs := int64(60)
		curBucket := now.Unix() / bucketSecs
		prevBucketTime := time.Unix((curBucket-1)*bucketSecs, 0)

		for i, edge := range c.graph.Edges {
			sampleEdge := &c.graph.Edges[i]
			curVis := edgeVisible(edge, c.seed, c.bootTime, now)
			prevVis := edgeVisible(edge, c.seed, c.bootTime, prevBucketTime)
			if prevVis && !curVis {
				// Edge just became invisible (link went down or device entered reboot).
				addChange("removed", edge.Proto, 1, sampleEdge)
			} else if !prevVis && curVis {
				// Edge just became visible (link came up or device exited reboot).
				addChange("added", edge.Proto, 1, sampleEdge)
			}
		}
	}

	// Emit the change log as ONE stream per tick (low-cardinality stream labels only).
	if len(changeLogLines) > 0 {
		streams = append(streams, loki.Stream{
			Labels: map[string]string{
				"source":          "network-topology-exporter",
				"change_kind":     "added",
				"discovery_proto": protocols[0],
				"job":             base["job"],
				"instance":        base["instance"],
			},
			Lines: changeLogLines,
		})
	}

	// ── conflict_total: rare increments (~1 every ~7 ticks) ───────────────────
	// Fire when the 7-minute epoch bucket index is congruent to a stable per-instance
	// hash mod 7. Over 50 60s ticks this covers ~7 distinct 7-minute buckets so at
	// least one fires, making the series reliably present.
	{
		hh := fnv.New64a()
		hh.Write([]byte(base["instance"]))
		instanceOffset := hh.Sum64() % 7
		const sevenMinSecs = 7 * 60
		bucketIdx := uint64(now.Unix()) / sevenMinSecs
		if bucketIdx%7 == instanceOffset {
			conflictLbls := map[string]string{
				"job":           base["job"],
				"instance":      base["instance"],
				"conflict_type": "neighbour_disagreement",
			}
			c.st.Add("network_topology_conflict_total", conflictLbls, 1)

			var srcDev, srcPort string
			if len(c.graph.Edges) > 0 {
				srcDev = c.graph.Edges[0].SrcDevice
				srcPort = c.graph.Edges[0].SrcPort
			}
			conflictBody := fmt.Sprintf(
				"level=warn source=network-topology-exporter event=topology_conflict conflict_type=neighbour_disagreement src_device=%s src_port=%s sources=lldp,cdp",
				srcDev, srcPort,
			)
			streams = append(streams, loki.Stream{
				Labels: map[string]string{
					"source":        "network-topology-exporter",
					"conflict_type": "neighbour_disagreement",
					"job":           base["job"],
					"instance":      base["instance"],
				},
				Lines: []loki.Line{{T: now, Body: conflictBody, Meta: map[string]string{
					"src_device": srcDev,
					"src_port":   srcPort,
				}}},
			})
		}
	}

	return streams
}
