// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Determinism helpers: same seed → same identity, every run (ARCHITECTURE I12). All
// fixture identity funnels through Sum so cross-signal joins are stable without any
// shared mutable state.

// Sum returns the sha256 hex of a seed plus path parts joined with ':'.
func Sum(seed string, parts ...string) string {
	h := sha256.Sum256([]byte(seed + ":" + strings.Join(parts, ":")))
	return hex.EncodeToString(h[:])
}

// HexID returns the first n hex chars (n ≤ 64) of the seeded hash.
func HexID(seed string, n int, parts ...string) string {
	s := Sum(seed, parts...)
	if n > len(s) {
		n = len(s)
	}
	return s[:n]
}

// EC2InstanceID returns a deterministic EC2 instance id: "i-" + 17 hex chars.
func EC2InstanceID(seed string, parts ...string) string {
	return "i-" + HexID(seed, 17, parts...)
}

// NATGatewayID returns a deterministic NAT-gateway id: "nat-" + 17 hex chars.
func NATGatewayID(seed string, parts ...string) string {
	return "nat-" + HexID(seed, 17, parts...)
}

// VolumeID returns a deterministic EBS volume id: "vol-" + 17 hex chars.
func VolumeID(seed string, parts ...string) string {
	return "vol-" + HexID(seed, 17, parts...)
}

// NodeUID returns a deterministic Kubernetes node UID in canonical UUID form
// (8-4-4-4-12 hex) — the `uid` label value real KSM/OpenCost carry on node-scoped series.
func NodeUID(seed string, parts ...string) string {
	h := HexID(seed, 32, parts...)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// PrivateIP returns a deterministic VPC private address in the 10.0.0.0/16 block — the realistic
// EKS shape (every node gets a unique VPC IP). The host bits are drawn from raw hash ENTROPY (the
// first two bytes of the seeded sha256, hex-DECODED), not from the ASCII codes of the hex string —
// the latter only spans ~160 distinct addresses (s[0]/s[1] are hex digits '0'-'f'), which
// birthday-collides at realistic node counts and produced two nodes sharing a hostname → identical
// kube_node_info (cluster,node) key → the k8s-monitoring app's provider_id-rewriting query 422s →
// blank cluster row. The third octet x in [0,256) and host octet y in [4,254) (second octet pinned
// to 0) → 256×250 ≈ 64k addresses, ample room for any real cluster; deriveNodesWithFloor still
// de-dups residual birthday collisions deterministically. Pure function of inputs (I12).
func PrivateIP(seed string, parts ...string) string {
	s := Sum(seed, parts...)
	b0, _ := strconv.ParseUint(s[0:2], 16, 8) // raw entropy byte 0 → third octet [0,256)
	b1, _ := strconv.ParseUint(s[2:4], 16, 8) // raw entropy byte 1 → host octet
	x := int(b0)
	y := 4 + int(b1)%250
	return fmt.Sprintf("10.0.%d.%d", x, y)
}

// NodeHostname returns the AWS-style private DNS node name for an ip in a region,
// e.g. "ip-10-0-1-23.us-east-1.compute.internal" ("ec2.internal" for us-east-1 is NOT
// modelled — the synthetic estate uses the uniform regional form).
func NodeHostname(ip, region string) string {
	return "ip-" + strings.ReplaceAll(ip, ".", "-") + "." + region + ".compute.internal"
}

// PodName returns a deterministic k8s pod name for a workload replica in the
// ReplicaSet style: <workload>-<10 hex>-<5 hex>. The SAME name must be used by the
// k8s substrate and the workload's resource attributes (service→pod join).
func PodName(seed, workload string, replica int) string {
	rs := HexID(seed, 10, "rs", workload)
	suffix := HexID(seed, 5, "pod", workload, fmt.Sprintf("%d", replica))
	return fmt.Sprintf("%s-%s-%s", workload, rs, suffix)
}

// StatefulSetPodName returns the deterministic StatefulSet pod name: <workload>-<ordinal>
// (e.g. "argocd-application-controller-0"). StatefulSet pods are ordinal-stable — no hash —
// so PodNames[0] is always the "-0" pod (replica-0 semantics hold for the service→pod join).
func StatefulSetPodName(workload string, ordinal int) string {
	return fmt.Sprintf("%s-%d", workload, ordinal)
}

// DaemonSetPodName returns the deterministic DaemonSet pod name for one node: <workload>-<5 hex>
// (e.g. "aws-node-2g5t7"). A DaemonSet runs exactly one pod per node, so the suffix is keyed by
// node hostname (stable per node, distinct across nodes; survives node-count changes).
func DaemonSetPodName(seed, workload, node string) string {
	return fmt.Sprintf("%s-%s", workload, HexID(seed, 5, "ds", workload, node))
}

// IsDaemonSet reports whether a controller string is the daemonset kind.
func IsDaemonSet(controller string) bool { return controller == "daemonset" }

// nodeAssignment is the deterministic 0..numNodes-1 placement hash — copied VERBATIM
// from internal/construct/k8scluster/helpers.go:139 so the pod-IP/instance formula has
// ONE home. (The param is named `deploy` there but callers pass the pod name for PodIP.)
func nodeAssignment(ns, deploy string, ri, numNodes int) int {
	if numNodes <= 0 {
		return 0
	}
	h := 0
	for _, c := range ns + deploy {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	return ((h + ri) & 0x7fffffff) % numNodes
}

// PodIP reproduces kube_pod_info.pod_ip (ksm.go:~783) so a construct's `instance`
// (=PodIP:port) joins the pod's kube_pod_* series. nodeIdx is the pod's node index;
// the host octet uses a fixed modulus 150 exactly as k8scluster does today.
func PodIP(nodeIdx int, ns, pod string, ri int) string {
	return fmt.Sprintf("10.1.%d.%d", 30+nodeIdx, 100+nodeAssignment(ns, pod, ri, 150))
}

// WorkloadPodNames returns the controller-aware pod-name slice for a workload given the live node
// set. Deployment → one ReplicaSet-form name per replica; StatefulSet → one ordinal name per
// replica (<name>-<ordinal>); DaemonSet → one per-node name (<name>-<5hex(node)>), len == len(nodes)
// (a DaemonSet's Replicas is ignored — it runs one pod per node). It is the SINGLE shared minter the
// resolver and the live-scaling path both call, so non-Deployment naming never reverts to the
// ReplicaSet form on live re-derivation. nodes may be nil for Deployment/StatefulSet (they don't
// need the node set); a DaemonSet with no nodes yields an empty slice.
func WorkloadPodNames(seed string, wl Workload, nodes []Node) []string {
	switch wl.Controller {
	case "statefulset":
		out := make([]string, 0, wl.Replicas)
		for ord := 0; ord < wl.Replicas; ord++ {
			out = append(out, StatefulSetPodName(wl.Name, ord))
		}
		return out
	case "daemonset":
		out := make([]string, 0, len(nodes))
		for _, n := range nodes {
			out = append(out, DaemonSetPodName(seed, wl.Name, n.Hostname))
		}
		return out
	default: // "" or "deployment"
		out := make([]string, 0, wl.Replicas)
		for p := 0; p < wl.Replicas; p++ {
			out = append(out, PodName(seed, wl.Name, p))
		}
		return out
	}
}

// ServerID returns the dbo11y 64-hex server_id for a DB instance name (real Alloy
// hashes server_uuid:hostname / system_identifier — we hash the instance name to the
// same 64-hex shape).
func ServerID(seed, instanceName string) string {
	return Sum(seed, "server_id", instanceName)
}

// MySQLDigest returns a deterministic 64-hex performance_schema digest.
func MySQLDigest(seed string, parts ...string) string {
	return Sum(seed, append([]string{"mysql_digest"}, parts...)...)
}

// PostgresQueryID returns a deterministic positive-int64 decimal-string queryid.
func PostgresQueryID(seed string, parts ...string) string {
	s := Sum(seed, append([]string{"pg_queryid"}, parts...)...)
	var v uint64
	for i := range 8 {
		b := s[i*2 : i*2+2]
		var n uint64
		_, _ = fmt.Sscanf(b, "%02x", &n)
		v = v<<8 | n
	}
	return fmt.Sprintf("%d", v>>1) // strip sign bit → positive int64
}
