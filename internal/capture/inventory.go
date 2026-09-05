// SPDX-License-Identifier: AGPL-3.0-only

// Package capture is the leaf library shared by skcapture (the customer-side inspector)
// and skforge (our-side converter). It defines the inventory wire contract, the pluggable
// Collector interface, and at-rest encryption. It imports NOTHING from synthkit internals
// (no blueprint/core/runner/construct/workload) — that isolation is the trust boundary,
// enforced by TestCaptureTrustBoundary.
package capture

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion is bumped on any breaking change to the Inventory wire shape. skforge refuses
// an inventory whose Envelope.SchemaVersion it does not recognise.
const SchemaVersion = 1

// Inventory is the complete captured picture of a customer environment. JSON is the wire format
// (deterministic, unambiguous); skforge re-pretty-prints it for human review.
type Inventory struct {
	Envelope Envelope  `json:"envelope"`
	Clusters []Cluster `json:"clusters"`
}

// Envelope makes the file auditable and version-gated.
type Envelope struct {
	SchemaVersion int            `json:"schema_version"`
	CapturedAtMS  int64          `json:"captured_at_ms"`
	ToolVersion   string         `json:"tool_version"`
	Flags         []string       `json:"flags"`          // the flag set skcapture ran with
	ResourceKinds []string       `json:"resource_kinds"` // kubectl resource kinds actually read
	Counts        map[string]int `json:"counts"`         // per-section counts (nodes, workloads, ...)
}

// Cluster identity sources, most authoritative first. Recorded verbatim in Cluster.NameSource so
// an operator can see where the identity came from instead of having to infer it.
const (
	// ProviderUndetermined is returned when the node labels do not identify one of the
	// supported cloud providers. It is deliberately distinct from a provider guess so
	// downstream tooling can require an operator decision.
	ProviderUndetermined = "undetermined"

	// NameSourceCollector is the cluster name the in-cluster metrics collector stamps on every
	// signal it ships. It is read from the collector's release-info ConfigMap and is the only
	// source that is guaranteed to join to the cluster's real telemetry.
	NameSourceCollector = "collector-release-info"
	// NameSourceEKSARN is the cluster name recovered from an EKS ARN kubeconfig context. It is the
	// real EKS cluster name but not necessarily the name the telemetry carries.
	NameSourceEKSARN = "eks-arn-context"
	// NameSourceContext is a slug of whatever the kubeconfig context happens to be called. It
	// describes the operator's kubeconfig, not the cluster, and is not authoritative.
	NameSourceContext = "kubeconfig-context"
	// NameSourceDefault is the placeholder used when no identity could be discovered at all.
	NameSourceDefault = "default"
)

// AuthoritativeNameSource reports whether a Cluster.NameSource value is the identity the cluster's
// own telemetry carries. Callers that present a captured cluster name must qualify it when this is
// false: everything other than the collector identity is an inference from the operator's
// kubeconfig and may not match the cluster label on the real signals.
func AuthoritativeNameSource(source string) bool { return source == NameSourceCollector }

// Cluster is one inspected cluster.
type Cluster struct {
	Name       string      `json:"name"`        // cluster identity; see NameSource for provenance
	NameSource string      `json:"name_source"` // collector-release-info|eks-arn-context|kubeconfig-context|default
	Provider   string      `json:"provider"`    // eks|gke|aks|undetermined (from node labels)
	Region     string      `json:"region"`      // from topology.kubernetes.io/region
	K8sVersion string      `json:"k8s_version"` // server gitVersion, trimmed to major.minor
	NodeGroups []NodeGroup `json:"node_groups"`
	Namespaces []string    `json:"namespaces"`
	Workloads  []Workload  `json:"workloads"`
	Services   []Service   `json:"services"`
	Ingresses  []Ingress   `json:"ingresses"`
	Addons     []Addon     `json:"addons"`
	Monitoring Monitoring  `json:"monitoring"`
}

// NodeGroup is one real node pool: an EKS managed nodegroup or a Karpenter NodePool, grouped by the
// pool identity the nodes themselves declare. Nodes carrying neither label family are grouped by
// (instance_type, os) with provisioner "unknown" rather than being attributed to a pool that does
// not exist.
type NodeGroup struct {
	Name string `json:"name"` // eks.amazonaws.com/nodegroup or karpenter.sh/nodepool value
	// InstanceType is the dominant node.kubernetes.io/instance-type in the pool. A pool may run
	// several types; InstanceTypes carries the full observed set.
	InstanceType  string   `json:"instance_type"`
	InstanceTypes []string `json:"instance_types,omitempty"` // every observed type, sorted, when the pool spans more than one
	Count         int      `json:"count"`
	Provisioner   string   `json:"provisioner"` // managed|karpenter|unknown
	OS            string   `json:"os"`          // linux|windows
}

// Workload is one Deployment/StatefulSet/DaemonSet.
type Workload struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Kind       string            `json:"kind"` // Deployment|StatefulSet|DaemonSet
	Replicas   int               `json:"replicas"`
	Images     []string          `json:"images"`
	Ports      []int32           `json:"ports"`
	ProbePaths []string          `json:"probe_paths"`
	Labels     map[string]string `json:"labels"` // pod-template labels
	// Annotations carries only the allowlisted workload-object annotation keys (see
	// allowedAnnotationKeys). Annotations are never copied wholesale: several well-known keys embed
	// the object's full spec, container environment values included.
	Annotations map[string]string `json:"annotations"`
}

// Service carries call-graph hints; ExternalName surfaces db/cache endpoints.
type Service struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Type         string            `json:"type"` // ClusterIP|NodePort|LoadBalancer|ExternalName
	ExternalName string            `json:"external_name"`
	Selector     map[string]string `json:"selector"`
	Ports        []int32           `json:"ports"`
}

// Ingress carries north-south edges.
type Ingress struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Hosts     []string `json:"hosts"`
	Backends  []string `json:"backends"` // referenced service names
}

// Addon is a detected operator/platform component. Kind is the synthkit construct kind when the
// detector recognises a standalone addon; empty Kind means the name needs forge classification as
// either a missing construct or a surface represented by another registered construct.
type Addon struct {
	Kind     string `json:"kind"`     // synthkit construct kind, or "" if no standalone addon kind
	Detected string `json:"detected"` // raw component name detected
	Evidence string `json:"evidence"` // how it was detected (helm-annotation|namespace|deployment)
}

// Monitoring records whether an in-cluster observability stack is present.
type Monitoring struct {
	K8sMonitoring bool   `json:"k8s_monitoring"`
	Alloy         bool   `json:"alloy"`
	AlloyVersion  string `json:"alloy_version"`
	ChartVersion  string `json:"chart_version"`
}

// Marshal serialises an inventory to its JSON wire form (indented for post-decrypt readability).
func Marshal(inv *Inventory) ([]byte, error) {
	return json.MarshalIndent(inv, "", "  ")
}

// Unmarshal parses the wire form and rejects an unrecognised schema version.
func Unmarshal(data []byte) (*Inventory, error) {
	var inv Inventory
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("capture: parse inventory: %w", err)
	}
	if inv.Envelope.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("capture: unsupported schema version %d (this build expects %d)",
			inv.Envelope.SchemaVersion, SchemaVersion)
	}
	return &inv, nil
}

// NewInventory returns an inventory with the Envelope.Counts map initialised so collectors can
// `+=` count keys without a nil-map panic. ENVELOPE OWNERSHIP RULE (frozen contract): collectors
// write ONLY Envelope.ResourceKinds (append) and Envelope.Counts (+= per key); cmd/skcapture sets
// the scalar envelope fields (SchemaVersion, CapturedAtMS, ToolVersion, Flags) by FIELD assignment
// AFTER all collectors run — never a whole-struct assignment that would clobber ResourceKinds/Counts.
func NewInventory() *Inventory {
	return &Inventory{Envelope: Envelope{Counts: map[string]int{}}}
}
