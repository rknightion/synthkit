// SPDX-License-Identifier: AGPL-3.0-only

// Package etcd implements the "etcd" construct.
//
// Kind:     "etcd"
// Scope:    core.ScopeSubstrate (cluster disambiguates; no blueprint label)
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   Config{} (empty — all identity from fx.Cluster)
//
// Build requires fx.Cluster (non-nil).
//
// Signal contract (signals/k8s-addons.md [slug: k8s-etcd]):
//
//	Families: etcd_*, grpc_server_*, process_*
//	Job:      "integrations/etcd"
//	Labels:   cluster + k8s_cluster_name + job on every series; NO blueprint label
//	Instance: <node.PrivateIP>:2381 (one per control-plane node, capped at 3)
//
// Values are doc-sourced representative healthy-steady-state values
// (managed EKS does not expose etcd directly).
//
// ARCHITECTURE invariants honoured:
//   - I3:  counters via state.Add (cumulative); gauges via state.Set
//   - I13: no empty/sentinel labels — absent dims are omitted
//   - I21: ScopeSubstrate — no blueprint label
package etcd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "etcd"
	interval = 60 * time.Second

	etcdJob  = "integrations/etcd"
	etcdPort = "2381"

	// etcdQuorumCap is the maximum number of control-plane instances to model
	// (etcd quorum realism — 3 is the standard quorum size).
	etcdQuorumCap = 3
)

// etcdWALBounds are the histogram bucket boundaries for etcd_disk_wal_fsync_duration_seconds.
// Fast NVMe-backed etcd — most syncs complete in <4ms.
var etcdWALBounds = []float64{
	0.001, 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128, 0.256, 0.512, 1.024,
}

// etcdBackendBounds are the histogram bucket boundaries for etcd_disk_backend_commit_duration_seconds.
var etcdBackendBounds = []float64{
	0.001, 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128, 0.256, 0.512, 1.024,
}

// etcdRTTBounds are the histogram bucket boundaries for etcd_network_peer_round_trip_time_seconds.
var etcdRTTBounds = []float64{
	0.001, 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128, 0.256, 0.512, 1.024,
}

// grpcHandlingBounds are the histogram bucket boundaries for grpc_server_handling_seconds.
var grpcHandlingBounds = []float64{
	0.001, 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128, 0.256, 0.512, 1.024,
}

// grpcCombos is the set of (grpc_type, grpc_service, grpc_method, grpc_code) tuples
// emitted for the gRPC server metrics. Doc-sourced healthy-steady-state combinations.
var grpcCombos = []struct {
	grpcType, grpcService, grpcMethod, grpcCode string
}{
	{"unary", "etcdserverpb.KV", "Range", "OK"},
	{"unary", "etcdserverpb.KV", "Put", "OK"},
	{"unary", "etcdserverpb.Watch", "Watch", "OK"},
	{"unary", "etcdserverpb.Lease", "Watch", "OK"},
}

// Config is the construct config struct (empty — all identity from fixtures).
type Config struct{}

// Construct is one etcd instance covering one EKS cluster's control plane.
type Construct struct {
	clust     *fixture.Cluster
	st        *state.State
	instances []string // "<ip>:2381" — one per quorum node
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// New builds a Construct from cfg and the resolved fixtures.
// Returns an error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("etcd: fixture.Cluster is required (nil)")
	}

	// Cap the number of instances at etcdQuorumCap for quorum realism.
	nodes := fx.Cluster.Nodes
	if len(nodes) > etcdQuorumCap {
		nodes = nodes[:etcdQuorumCap]
	}

	instances := make([]string, 0, len(nodes))
	for _, n := range nodes {
		instances = append(instances, fmt.Sprintf("%s:%s", n.PrivateIP, etcdPort))
	}
	// If no nodes (unusual), use a deterministic synthetic fallback.
	if len(instances) == 0 {
		ip := fixture.PrivateIP(fx.Seed, fx.Cluster.Name, "etcd", "0")
		instances = append(instances, fmt.Sprintf("%s:%s", ip, etcdPort))
	}

	return &Construct{
		clust:     fx.Cluster,
		st:        state.NewState(),
		instances: instances,
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics only.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one etcd metric snapshot for the cluster's control plane.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	tickSec := interval.Seconds()
	scale := tickSec / 30.0

	base := map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"job":              etcdJob,
	}

	withExtra := func(extra map[string]string) map[string]string {
		m := make(map[string]string, len(base)+len(extra))
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// ── Per-instance metrics ──────────────────────────────────────────────────
	for i, inst := range c.instances {
		instLbls := withExtra(map[string]string{"instance": inst})

		// ── etcd_server_has_leader (G; per instance) — always 1 (healthy) ──────
		c.st.Set("etcd_server_has_leader", instLbls, 1.0)

		// ── etcd_server_leader_changes_seen_total (C; per instance) ──────────
		// Small value — cluster is stable; changes are rare.
		c.st.Add("etcd_server_leader_changes_seen_total", instLbls, 0.01*scale)

		// ── etcd_server_proposals_failed_total (C; per instance) — 0 (healthy)
		c.st.Add("etcd_server_proposals_failed_total", instLbls, 0)

		// ── etcd_server_quota_backend_bytes (G; per instance) — ~8GiB default
		c.st.Set("etcd_server_quota_backend_bytes", instLbls, 8589934592.0)

		// ── etcd_disk_wal_fsync_duration_seconds (H; per instance) ───────────
		// Fast NVMe: typical sync 1-4ms.
		for range 3 {
			c.st.Observe("etcd_disk_wal_fsync_duration_seconds", instLbls,
				etcdWALBounds, state.LEBare, 0.001+0.002*float64(i+1)*0.3)
		}

		// ── etcd_disk_backend_commit_duration_seconds (H; per instance) ──────
		for range 3 {
			c.st.Observe("etcd_disk_backend_commit_duration_seconds", instLbls,
				etcdBackendBounds, state.LEBare, 0.002+0.001*float64(i+1)*0.5)
		}

		// ── etcd_mvcc_db_total_size_in_bytes (G; per instance) — ~100MB ───────
		c.st.Set("etcd_mvcc_db_total_size_in_bytes", instLbls, 104857600.0) // 100MB

		// ── etcd_mvcc_db_total_size_in_use_in_bytes (G; per instance) — ~80MB
		c.st.Set("etcd_mvcc_db_total_size_in_use_in_bytes", instLbls, 83886080.0) // 80MB

		// ── etcd_network_client_grpc_received_bytes_total (C; per instance) ──
		c.st.Add("etcd_network_client_grpc_received_bytes_total", instLbls, 4096*scale)

		// ── etcd_network_client_grpc_sent_bytes_total (C; per instance) ───────
		c.st.Add("etcd_network_client_grpc_sent_bytes_total", instLbls, 8192*scale)

		// ── Peer metrics — emit for each peer (other instances) ───────────────
		for j, peer := range c.instances {
			if j == i {
				continue // skip self
			}
			// Use a short stable peer ID derived from index.
			peerID := fmt.Sprintf("peer-%d", j)
			peerLbls := withExtra(map[string]string{"instance": inst, "To": peerID})

			c.st.Add("etcd_network_peer_received_bytes_total", peerLbls, 2048*scale)
			c.st.Add("etcd_network_peer_sent_bytes_total", peerLbls, 2048*scale)
			c.st.Add("etcd_network_peer_sent_failures_total", peerLbls, 0)

			// ── etcd_network_peer_round_trip_time_seconds (H; instance,To) ────
			c.st.Observe("etcd_network_peer_round_trip_time_seconds", peerLbls,
				etcdRTTBounds, state.LEBare, 0.001+float64(j)*0.0005)

			_ = peer // suppress unused warning
		}

		// ── gRPC server metrics (per instance) ───────────────────────────────
		for _, combo := range grpcCombos {
			comboLbls := withExtra(map[string]string{
				"instance":     inst,
				"grpc_type":    combo.grpcType,
				"grpc_service": combo.grpcService,
				"grpc_method":  combo.grpcMethod,
				"grpc_code":    combo.grpcCode,
			})
			c.st.Add("grpc_server_handled_total", comboLbls, 10*scale)
			c.st.Add("grpc_server_started_total", comboLbls, 10*scale)

			// ── grpc_server_handling_seconds (H; grpc_type,grpc_service,grpc_method)
			histoLbls := withExtra(map[string]string{
				"instance":     inst,
				"grpc_type":    combo.grpcType,
				"grpc_service": combo.grpcService,
				"grpc_method":  combo.grpcMethod,
			})
			for range 5 {
				c.st.Observe("grpc_server_handling_seconds", histoLbls,
					grpcHandlingBounds, state.LEBare, 0.001+0.002*float64(i)*0.4)
			}
		}

		// ── process_resident_memory_bytes (G; per instance) — ~100MB ─────────
		c.st.Set("process_resident_memory_bytes", instLbls, 104857600.0)
	}

	return w.Metrics.Write(ctx, c.st.Collect(now))
}
