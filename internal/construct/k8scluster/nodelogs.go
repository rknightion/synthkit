// SPDX-License-Identifier: AGPL-3.0-only

// nodelogs.go — node journal log streams for k8scluster (I15: low-card stream labels).
// Gated on Features["node_logs"]. Emits journal-unit streams per node.
//
// Stream labels: cluster, k8s_cluster_name, job="integrations/kubernetes/journal",
// instance=<node.Hostname>, source="journal", unit, service_name=<unit>, level (UPPERCASE).
// No blueprint label (ScopeSubstrate).
package k8scluster

import (
	"context"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

const jobJournal = "integrations/kubernetes/journal"

// journalUnit describes one systemd unit's synthetic log lines.
type journalUnit struct {
	unit   string
	level  string
	bodies []string
}

// unitsForOS returns the journal units to emit for a given OS ID.
func unitsForOS(osid string) []journalUnit {
	switch osid {
	case "bottlerocket":
		return []journalUnit{
			{
				"host-containers@control.service",
				"INFO",
				[]string{
					"2026-06-15 10:46:12 INFO [CredentialRefresher] Next credential rotation will be in 12 minutes",
					"2026-06-15 10:47:15 INFO [CredentialRefresher] Credentials ready",
				},
			},
			{
				"init.scope",
				"UNKNOWN",
				[]string{
					"Finished Send a Metricdog Ping.",
					"metricdog.service: Deactivated successfully.",
				},
			},
		}
	default:
		return []journalUnit{
			{
				"kubelet.service",
				"INFO",
				[]string{`I0615 10:46:12 kubelet.go] "Successfully registered node"`},
			},
			{
				"containerd.service",
				"INFO",
				[]string{`level=info msg="loading plugin"`},
			},
		}
	}
}

// emitNodeLogs writes node journal log streams; returns nil and writes nothing when gated off.
func emitNodeLogs(
	ctx context.Context,
	now time.Time,
	cluster string,
	cl *fixture.Cluster,
	w *core.World,
) error {
	streams := buildNodeLogStreams(now, cluster, cl)
	if len(streams) == 0 {
		return nil
	}
	return w.Logs.Write(ctx, streams)
}

// buildNodeLogStreams constructs node journal Loki streams (pure, no I/O).
// Returns nil when the feature is off.
func buildNodeLogStreams(now time.Time, cluster string, cl *fixture.Cluster) []loki.Stream {
	if !cl.K8sMonitoring.Features["node_logs"] {
		return nil
	}

	osid := cl.Platform.OSID
	units := unitsForOS(osid)

	var out []loki.Stream
	for _, node := range cl.Nodes {
		hostname := node.Hostname
		for _, u := range units {
			var lines []loki.Line
			for _, body := range u.bodies {
				lines = append(lines, loki.Line{T: now, Body: body})
			}
			labels := map[string]string{
				"cluster":          cluster,
				"k8s_cluster_name": cluster,
				"job":              jobJournal,
				"instance":         hostname,
				"source":           "journal",
				"unit":             u.unit,
				"service_name":     u.unit,
				"level":            u.level,
			}
			// detected_level is Loki's lowercase auto-detected level (live reference 2026-06-15);
			// omitted when no level is detectable (e.g. init.scope → UNKNOWN), per I13.
			if u.level != "UNKNOWN" {
				labels["detected_level"] = strings.ToLower(u.level)
			}
			out = append(out, loki.Stream{Labels: labels, Lines: lines})
		}
	}
	return out
}
