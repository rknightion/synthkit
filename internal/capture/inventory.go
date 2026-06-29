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

// Cluster is one inspected cluster.
type Cluster struct {
	Name       string      `json:"name"`        // context/cluster name (best-effort)
	Provider   string      `json:"provider"`    // eks|gke|aks|unknown (from node labels)
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

// NodeGroup is synthesized by grouping nodes on (instance_type, provisioner, os).
type NodeGroup struct {
	Name         string `json:"name"`          // eks nodegroup label value, or synthesized key
	InstanceType string `json:"instance_type"` // node.kubernetes.io/instance-type
	Count        int    `json:"count"`
	Provisioner  string `json:"provisioner"` // managed|karpenter|unknown
	OS           string `json:"os"`          // linux|windows
}

// Workload is one Deployment/StatefulSet/DaemonSet.
type Workload struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Kind        string            `json:"kind"` // Deployment|StatefulSet|DaemonSet
	Replicas    int               `json:"replicas"`
	Images      []string          `json:"images"`
	Ports       []int32           `json:"ports"`
	ProbePaths  []string          `json:"probe_paths"`
	Labels      map[string]string `json:"labels"`      // pod-template labels
	Annotations map[string]string `json:"annotations"` // workload-object annotations (e.g. meta.helm.sh/release-name)
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
// detector recognises it; empty Kind means "no construct exists" (a coverage-gap signal).
type Addon struct {
	Kind     string `json:"kind"`     // synthkit construct kind, or "" if unmodelled
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
