// SPDX-License-Identifier: AGPL-3.0-only

package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// KubectlRunner is a function that shells out to kubectl with the given args and returns stdout.
// The default implementation is execKubectl; tests inject a fake runner.
type KubectlRunner func(ctx context.Context, args ...string) ([]byte, error)

// execKubectl is the production kubectl runner: it runs `kubectl <args...>` and returns stdout.
// stderr is wrapped into the error on non-zero exit.
func execKubectl(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, ee.Stderr)
		}
		return nil, fmt.Errorf("kubectl %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// K8sCollector collects cluster state by shelling out to kubectl. Run is injectable for tests;
// when nil, Collect uses execKubectl.
type K8sCollector struct {
	// Run is the kubectl runner. Leave nil to use the real kubectl binary.
	Run KubectlRunner
}

// Name implements Collector.
func (c *K8sCollector) Name() string { return "k8s" }

// runner returns the configured runner or the default execKubectl.
func (c *K8sCollector) runner() KubectlRunner {
	if c.Run != nil {
		return c.Run
	}
	return execKubectl
}

// Collect shells out to kubectl, parses the responses into one Cluster, and appends it to
// inv.Clusters. It records the resource kinds it read into inv.Envelope.ResourceKinds (append) and
// per-section counts into inv.Envelope.Counts (+=). It never assigns inv.Envelope as a whole struct
// (frozen ownership rule).
func (c *K8sCollector) Collect(ctx context.Context, inv *Inventory, opts CaptureOpts) error {
	// Guard: the Counts map may be nil when tests construct a bare Inventory{} directly.
	if inv.Envelope.Counts == nil {
		inv.Envelope.Counts = map[string]int{}
	}

	run := c.runner()
	cl := Cluster{}

	// -------------------------------------------------------------------------
	// Nodes → NodeGroups + provider/region detection
	// -------------------------------------------------------------------------
	nodesRaw, err := run(ctx, "get", "nodes", "-o", "json")
	if err != nil {
		return fmt.Errorf("k8s: get nodes: %w", err)
	}
	inv.Envelope.ResourceKinds = append(inv.Envelope.ResourceKinds, "nodes")

	nodeList, err := parseNodeList(nodesRaw)
	if err != nil {
		return fmt.Errorf("k8s: parse nodes: %w", err)
	}

	cl.NodeGroups, cl.Provider, cl.Region = groupNodes(nodeList)
	inv.Envelope.Counts["nodes"] += len(nodeList)

	// -------------------------------------------------------------------------
	// Server version (best-effort; ignore errors — some RBAC setups deny it)
	// -------------------------------------------------------------------------
	if vraw, verr := run(ctx, "version", "-o", "json"); verr == nil {
		cl.K8sVersion = parseK8sVersion(vraw)
	}

	// -------------------------------------------------------------------------
	// Namespaces
	// -------------------------------------------------------------------------
	nsRaw, err := run(ctx, "get", "namespaces", "-o", "json")
	if err != nil {
		return fmt.Errorf("k8s: get namespaces: %w", err)
	}
	inv.Envelope.ResourceKinds = append(inv.Envelope.ResourceKinds, "namespaces")

	cl.Namespaces, err = parseNamespaces(nsRaw, opts)
	if err != nil {
		return fmt.Errorf("k8s: parse namespaces: %w", err)
	}
	inv.Envelope.Counts["namespaces"] += len(cl.Namespaces)

	// -------------------------------------------------------------------------
	// Workloads: Deployments, StatefulSets, DaemonSets
	// -------------------------------------------------------------------------
	for _, kind := range []string{"deployments", "statefulsets", "daemonsets"} {
		args := workloadGetArgs(kind, opts)
		raw, rerr := run(ctx, args...)
		if rerr != nil {
			return fmt.Errorf("k8s: get %s: %w", kind, rerr)
		}
		inv.Envelope.ResourceKinds = append(inv.Envelope.ResourceKinds, kind)

		ws, werr := parseWorkloads(raw, kind)
		if werr != nil {
			return fmt.Errorf("k8s: parse %s: %w", kind, werr)
		}
		ws = filterByNamespace(ws, opts)
		cl.Workloads = append(cl.Workloads, ws...)
		inv.Envelope.Counts[kind] += len(ws)
	}

	// -------------------------------------------------------------------------
	// Addon detection (from workloads already fetched — no secret reads)
	// -------------------------------------------------------------------------
	cl.Addons = detectAddons(cl.Workloads, cl.Namespaces)
	inv.Envelope.Counts["addons"] += len(cl.Addons)

	// -------------------------------------------------------------------------
	// Monitoring detection
	// -------------------------------------------------------------------------
	cl.Monitoring = detectMonitoring(cl.Workloads, cl.Namespaces)

	// -------------------------------------------------------------------------
	// Services
	// -------------------------------------------------------------------------
	svcArgs := serviceGetArgs(opts)
	svcRaw, err := run(ctx, svcArgs...)
	if err != nil {
		return fmt.Errorf("k8s: get services: %w", err)
	}
	inv.Envelope.ResourceKinds = append(inv.Envelope.ResourceKinds, "services")

	cl.Services, err = parseServices(svcRaw)
	if err != nil {
		return fmt.Errorf("k8s: parse services: %w", err)
	}
	cl.Services = filterServicesByNamespace(cl.Services, opts)
	inv.Envelope.Counts["services"] += len(cl.Services)

	// -------------------------------------------------------------------------
	// Ingresses
	// -------------------------------------------------------------------------
	ingArgs := ingressGetArgs(opts)
	ingRaw, err := run(ctx, ingArgs...)
	if err != nil {
		return fmt.Errorf("k8s: get ingresses: %w", err)
	}
	inv.Envelope.ResourceKinds = append(inv.Envelope.ResourceKinds, "ingresses")

	cl.Ingresses, err = parseIngresses(ingRaw)
	if err != nil {
		return fmt.Errorf("k8s: parse ingresses: %w", err)
	}
	cl.Ingresses = filterIngressesByNamespace(cl.Ingresses, opts)
	inv.Envelope.Counts["ingresses"] += len(cl.Ingresses)

	// -------------------------------------------------------------------------
	// Cluster identity. Runs last: the collector lookup is driven by the workloads and namespaces
	// already collected.
	// -------------------------------------------------------------------------
	cl.Name, cl.NameSource = resolveClusterName(ctx, run, &cl)

	inv.Clusters = append(inv.Clusters, cl)
	return nil
}

// resolveClusterName determines the cluster identity and reports which source produced it.
//
// Precedence is deliberately observable-truth-first. Cluster name is the primary join key across
// every k8s construct, so a blueprint forged with the wrong one emits telemetry that can never join
// to the real cluster's dashboards — and does so silently, because the blueprint still loads and
// validates. The collector's own cluster name is the value stamped on every metric, log and trace
// the cluster ships, so it is the only source that is guaranteed to join.
//
// Nothing here falls back silently: every branch returns the source alongside the name, and only
// NameSourceCollector satisfies AuthoritativeNameSource.
func resolveClusterName(ctx context.Context, run KubectlRunner, cl *Cluster) (name, source string) {
	info := collectorReleaseInfo(ctx, run, cl)
	if info.ChartVersion != "" {
		cl.Monitoring.ChartVersion = info.ChartVersion
	}
	if info.ClusterName != "" {
		return info.ClusterName, NameSourceCollector
	}
	if raw, err := run(ctx, "config", "current-context"); err == nil {
		if n, ok := clusterNameFromEKSARN(string(raw)); ok {
			return n, NameSourceEKSARN
		}
		if n := slugifyClusterName(string(raw)); n != "" {
			return n, NameSourceContext
		}
	}
	return "captured-cluster", NameSourceDefault
}

// releaseInfoClusterKey is the ConfigMap data key holding the cluster name the k8s-monitoring
// collector applies as a label to all telemetry it collects.
const releaseInfoClusterKey = "cluster"

// releaseInfoMetricKey is the only other release-info data key read by the capture. It contains
// the chart's self-reporting Prometheus text, including the version-bearing build-info line.
const releaseInfoMetricKey = "self-reporting-metric.prom"

// releaseInfoChartMetric is the exact metric emitted by the k8s-monitoring chart's release-info
// text file. The name is sourced from the chart template and is not reconstructed from a capture.
const releaseInfoChartMetric = "grafana_kubernetes_monitoring_build_info"

// collectorClusterName reads the cluster name out of the in-cluster metrics collector's release-info
// ConfigMap, returning "" when it is not discoverable (no collector, no read permission, or a
// release layout this does not recognise) so the caller can fall back and say that it did.
//
// The lookup is a targeted `get` of specific named ConfigMaps derived from the collector workloads
// already captured — never a namespace- or cluster-wide list, which would pull every ConfigMap's
// data through the process and defeat the zero-secret default. Only the cluster-name key and the
// known self-reporting metric key are read out of the response.
//
// The default RBAC in deploy/skcapture/rbac.yaml grants no ConfigMap access at all, so an in-cluster
// run under that role takes the fallback path by design.
func collectorClusterName(ctx context.Context, run KubectlRunner, cl *Cluster) string {
	return collectorReleaseInfo(ctx, run, cl).ClusterName
}

// collectorReleaseInfo reads the two known, non-secret values in the collector's release-info
// ConfigMap. The cluster name is used verbatim because it must be byte-identical to the cluster
// label on real telemetry. The chart version is parsed only from the chart's exact build-info
// metric; arbitrary ConfigMap data and arbitrary metric lines are ignored.
type collectorReleaseInfoResult struct {
	ClusterName  string
	ChartVersion string
}

func collectorReleaseInfo(ctx context.Context, run KubectlRunner, cl *Cluster) collectorReleaseInfoResult {
	if !cl.Monitoring.Alloy && !cl.Monitoring.K8sMonitoring {
		return collectorReleaseInfoResult{}
	}

	// A chart can leave the cluster key unset, so retain a chart-only value as a fallback. It is
	// deliberately discarded as soon as a later candidate supplies a name: the name and version
	// must come from the same ConfigMap, never from two candidates in the probe list.
	var chartWithoutName string
	for _, cand := range releaseInfoCandidates(cl) {
		nameRaw, err := run(ctx, "get", "configmap", "-n", cand.namespace, cand.name,
			"-o", "jsonpath={.data."+releaseInfoClusterKey+"}")
		if err != nil {
			continue // not present, or not readable under this role — both are expected
		}

		name := strings.TrimSpace(string(nameRaw))
		metricRaw, merr := run(ctx, "get", "configmap", "-n", cand.namespace, cand.name,
			"-o", "jsonpath={.data['"+releaseInfoMetricKey+"']}")
		chart := ""
		if merr == nil {
			chart = chartVersionFromReleaseInfo(metricRaw)
		}
		if name != "" {
			// The collector name is used verbatim: it must be byte-identical to the cluster label
			// on the real telemetry, so it is never slugified. The chart version is paired with
			// this same candidate and never inherited from another release-info ConfigMap.
			return collectorReleaseInfoResult{ClusterName: name, ChartVersion: chart}
		}
		if chartWithoutName == "" {
			chartWithoutName = chart
		}
	}
	return collectorReleaseInfoResult{ChartVersion: chartWithoutName}
}

// chartVersionFromReleaseInfo extracts the version label from the exact chart build-info family
// stored in self-reporting-metric.prom. It returns empty for malformed, unrelated, or unlabelled
// lines so a missing release value remains visible to the caller.
func chartVersionFromReleaseInfo(raw []byte) string {
	prefix := releaseInfoChartMetric + "{"
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		end := strings.IndexByte(line, '}')
		if end < len(prefix) {
			continue
		}
		for _, label := range strings.Split(line[len(prefix):end], ",") {
			key, value, ok := strings.Cut(label, "=")
			if !ok || strings.TrimSpace(key) != "version" {
				continue
			}
			value, err := strconv.Unquote(strings.TrimSpace(value))
			if err == nil && value != "" {
				return value
			}
		}
	}
	return ""
}

// releaseInfoCandidate is one namespace/name pair to probe for the collector release-info ConfigMap.
type releaseInfoCandidate struct{ namespace, name string }

// maxReleaseInfoCandidates bounds the probe so a large cluster cannot turn identity resolution into
// a long sequence of kubectl calls.
const maxReleaseInfoCandidates = 8

// releaseInfoCandidates derives probe targets from the collector workloads that were actually
// captured. The k8s-monitoring chart names its collectors "<fullname>-alloy-<role>" and its
// release-info ConfigMap "<fullname>-release-info", so the chart fullname is recoverable from any
// collector workload name. Deriving from observed names keeps this working for a renamed release
// instead of hard-coding one installation's names.
func releaseInfoCandidates(cl *Cluster) []releaseInfoCandidate {
	var out []releaseInfoCandidate
	seen := map[releaseInfoCandidate]bool{}
	add := func(namespace, name string) {
		if namespace == "" || name == "" || len(out) >= maxReleaseInfoCandidates {
			return
		}
		c := releaseInfoCandidate{namespace: namespace, name: name}
		if seen[c] {
			return
		}
		seen[c] = true
		out = append(out, c)
	}
	for _, w := range cl.Workloads {
		if i := strings.Index(w.Name, "-alloy"); i > 0 {
			add(w.Namespace, w.Name[:i]+"-release-info")
		}
		if rel := w.Annotations["meta.helm.sh/release-name"]; rel != "" && strings.Contains(w.Name, "alloy") {
			add(w.Namespace, rel+"-release-info")
		}
	}
	return out
}

// clusterNameFromEKSARN recovers the real EKS cluster name from an EKS ARN kubeconfig context
// (arn:aws:eks:<region>:<account>:cluster/<name>). The second-best source: it is the cluster's AWS
// identity, which is not necessarily the name its collector stamps on telemetry. Reports false for
// anything that is not an EKS ARN so the caller can label the source correctly.
func clusterNameFromEKSARN(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "arn:") || !strings.Contains(s, ":cluster/") {
		return "", false
	}
	i := strings.LastIndex(s, "/")
	if i < 0 || i == len(s)-1 {
		return "", false
	}
	return s[i+1:], true
}

// slugifyClusterName reduces an arbitrary kubeconfig context name to something usable as both a
// cluster name and a blueprint-name stem: lowercase alphanumerics and "-", with "_", "." and " "
// mapped to "-". Returns "" when nothing usable remains.
func slugifyClusterName(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == '.' || r == ' ':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// =============================================================================
// JSON shim structs — hold only the fields we need from the kubectl -o json output
// =============================================================================

type k8sNodeList struct {
	Items []k8sNode `json:"items"`
}

type k8sNode struct {
	Metadata k8sObjectMeta `json:"metadata"`
}

type k8sObjectMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type k8sNamespaceList struct {
	Items []struct {
		Metadata k8sObjectMeta `json:"metadata"`
	} `json:"items"`
}

type k8sWorkloadList struct {
	Items []k8sWorkloadItem `json:"items"`
}

type k8sWorkloadItem struct {
	Kind     string          `json:"kind"`
	Metadata k8sObjectMeta   `json:"metadata"`
	Spec     k8sWorkloadSpec `json:"spec"`
}

type k8sWorkloadSpec struct {
	Replicas *int32         `json:"replicas"`
	Template k8sPodTemplate `json:"template"`
}

type k8sPodTemplate struct {
	Metadata k8sObjectMeta `json:"metadata"`
	Spec     k8sPodSpec    `json:"spec"`
}

type k8sPodSpec struct {
	Containers []k8sContainer `json:"containers"`
}

type k8sContainer struct {
	Image          string             `json:"image"`
	Ports          []k8sContainerPort `json:"ports"`
	LivenessProbe  *k8sProbe          `json:"livenessProbe"`
	ReadinessProbe *k8sProbe          `json:"readinessProbe"`
	StartupProbe   *k8sProbe          `json:"startupProbe"`
}

type k8sContainerPort struct {
	ContainerPort int32  `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

type k8sProbe struct {
	HTTPGet *k8sHTTPGetAction `json:"httpGet"`
}

type k8sHTTPGetAction struct {
	Path string `json:"path"`
}

type k8sServiceList struct {
	Items []k8sServiceItem `json:"items"`
}

type k8sServiceItem struct {
	Metadata k8sObjectMeta  `json:"metadata"`
	Spec     k8sServiceSpec `json:"spec"`
}

type k8sServiceSpec struct {
	Type         string            `json:"type"`
	ExternalName string            `json:"externalName"`
	Selector     map[string]string `json:"selector"`
	Ports        []k8sServicePort  `json:"ports"`
}

type k8sServicePort struct {
	Port int32 `json:"port"`
}

type k8sIngressList struct {
	Items []k8sIngressItem `json:"items"`
}

type k8sIngressItem struct {
	Metadata k8sObjectMeta  `json:"metadata"`
	Spec     k8sIngressSpec `json:"spec"`
}

type k8sIngressSpec struct {
	Rules []k8sIngressRule `json:"rules"`
}

type k8sIngressRule struct {
	Host string       `json:"host"`
	HTTP *k8sHTTPRule `json:"http"`
}

type k8sHTTPRule struct {
	Paths []k8sHTTPPath `json:"paths"`
}

type k8sHTTPPath struct {
	Backend k8sIngressBackend `json:"backend"`
}

type k8sIngressBackend struct {
	Service *k8sIngressServiceBackend `json:"service"` // networking.k8s.io/v1
	// legacy v1beta1 backend
	ServiceName string `json:"serviceName"`
}

type k8sIngressServiceBackend struct {
	Name string `json:"name"`
}

type k8sVersionOutput struct {
	ServerVersion struct {
		GitVersion string `json:"gitVersion"`
	} `json:"serverVersion"`
}

// =============================================================================
// Parse helpers
// =============================================================================

func parseNodeList(raw []byte) ([]k8sNode, error) {
	var list k8sNodeList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Node labels that declare which pool a node belongs to and therefore how it was provisioned.
// Both are set by the component that owns the node; neither is inferable from anything else.
const (
	// labelEKSNodeGroup is present on every node in an EKS managed nodegroup.
	labelEKSNodeGroup = "eks.amazonaws.com/nodegroup"
	// labelKarpenterNodePool is present on every Karpenter-provisioned node and names its NodePool.
	labelKarpenterNodePool = "karpenter.sh/nodepool"
)

// nodeGroupKey identifies one real node pool. Nodes are grouped by the pool identity they declare,
// never by instance type: a NodePool routinely runs several instance types, and grouping by type
// shreds one pool into several and merges unrelated pools that happen to share a type.
type nodeGroupKey struct {
	name        string
	provisioner string
	os          string
}

// nodePoolIdentity reads how a node is provisioned and which pool it belongs to, straight off the
// node's own labels.
//
// The EKS nodegroup label wins over the Karpenter one: a node in a managed nodegroup is managed by
// definition, and such nodes commonly carry unrelated "karpenter.sh/" labels — "karpenter.sh/controller"
// marks the nodes the Karpenter controller itself is scheduled onto, which is why a "karpenter.sh/"
// prefix test attributes a managed nodegroup to Karpenter. Only the specific NodePool label means
// Karpenter provisioned this node.
//
// A node declaring neither is reported as provisioner "unknown" rather than assumed managed.
func nodePoolIdentity(lbl map[string]string) (name, provisioner string) {
	if ng := lbl[labelEKSNodeGroup]; ng != "" {
		return ng, "managed"
	}
	if np := lbl[labelKarpenterNodePool]; np != "" {
		return np, "karpenter"
	}
	return "", "unknown"
}

// groupNodes collapses the node list into one NodeGroup per real pool, and detects the cloud
// provider and region from node label families.
func groupNodes(nodes []k8sNode) (groups []NodeGroup, provider string, region string) {
	type groupAccum struct {
		count         int
		typeCounts    map[string]int
		typeFirstSeen map[string]int
	}
	accum := map[nodeGroupKey]*groupAccum{}
	keyOrder := []nodeGroupKey{}

	for i, n := range nodes {
		lbl := n.Metadata.Labels

		// Provider detection from label families.
		if provider == "" {
			switch {
			case hasLabelPrefix(lbl, "eks.amazonaws.com/"):
				provider = "eks"
			case hasLabelPrefix(lbl, "karpenter.k8s.aws/"):
				// Karpenter's AWS provider labels identify EKS nodes even when the
				// managed-nodegroup label family is absent.
				provider = "eks"
			case hasLabelPrefix(lbl, "cloud.google.com/gke-"):
				provider = "gke"
			case hasLabelPrefix(lbl, "kubernetes.azure.com/"):
				provider = "aks"
			}
		}

		// Region (first non-empty win).
		if region == "" {
			region = lbl["topology.kubernetes.io/region"]
		}

		poolName, prov := nodePoolIdentity(lbl)
		instanceType := lbl["node.kubernetes.io/instance-type"]
		os := lbl["kubernetes.io/os"]
		if os == "" {
			os = "linux"
		}
		if poolName == "" {
			// No pool identity to read. Group by shape so the node count is still right, and make it
			// obvious from the name that this is not a declared pool.
			poolName = strings.Trim(instanceType+"-"+prov, "-")
		}

		key := nodeGroupKey{name: poolName, provisioner: prov, os: os}
		acc, ok := accum[key]
		if !ok {
			acc = &groupAccum{typeCounts: map[string]int{}, typeFirstSeen: map[string]int{}}
			accum[key] = acc
			keyOrder = append(keyOrder, key)
		}
		acc.count++
		if instanceType != "" {
			if _, seen := acc.typeCounts[instanceType]; !seen {
				acc.typeFirstSeen[instanceType] = i
			}
			acc.typeCounts[instanceType]++
		}
	}

	for _, key := range keyOrder {
		acc := accum[key]
		types := sortedInstanceTypes(acc.typeCounts)
		g := NodeGroup{
			Name:         key.name,
			InstanceType: dominantInstanceType(acc.typeCounts, acc.typeFirstSeen),
			Count:        acc.count,
			Provisioner:  key.provisioner,
			OS:           key.os,
		}
		// Record the full set only when the pool genuinely spans more than one type; a single-type
		// pool is fully described by InstanceType.
		if len(types) > 1 {
			g.InstanceTypes = types
		}
		groups = append(groups, g)
	}

	if provider == "" {
		provider = ProviderUndetermined
	}
	return groups, provider, region
}

// dominantInstanceType returns the most common instance type in a pool, breaking ties on the order
// the types were first observed so the result is stable for a given node list.
func dominantInstanceType(counts map[string]int, firstSeen map[string]int) string {
	best, bestCount, bestSeen := "", 0, 0
	for t, n := range counts {
		if n > bestCount || (n == bestCount && firstSeen[t] < bestSeen) {
			best, bestCount, bestSeen = t, n, firstSeen[t]
		}
	}
	return best
}

// sortedInstanceTypes returns the observed instance types in a deterministic order.
func sortedInstanceTypes(counts map[string]int) []string {
	out := make([]string, 0, len(counts))
	for t := range counts {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func hasLabelPrefix(labels map[string]string, prefix string) bool {
	for k := range labels {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func parseK8sVersion(raw []byte) string {
	var v k8sVersionOutput
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	gv := v.ServerVersion.GitVersion
	// Trim to "major.minor": strip leading "v" then keep first two dot-parts.
	gv = strings.TrimPrefix(gv, "v")
	parts := strings.SplitN(gv, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return gv
}

func parseNamespaces(raw []byte, opts CaptureOpts) ([]string, error) {
	var list k8sNamespaceList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	allow := stringSet(opts.Namespaces)
	deny := stringSet(opts.ExcludeNamespaces)
	var out []string
	for _, item := range list.Items {
		ns := item.Metadata.Name
		if len(allow) > 0 && !allow[ns] {
			continue
		}
		if deny[ns] {
			continue
		}
		out = append(out, ns)
	}
	return out, nil
}

func workloadGetArgs(kind string, opts CaptureOpts) []string {
	if len(opts.Namespaces) == 0 {
		return []string{"get", kind, "-A", "-o", "json"}
	}
	// Per-namespace queries will be handled by the caller; here just do all-namespaces.
	return []string{"get", kind, "-A", "-o", "json"}
}

func serviceGetArgs(opts CaptureOpts) []string {
	if len(opts.Namespaces) == 0 {
		return []string{"get", "services", "-A", "-o", "json"}
	}
	return []string{"get", "services", "-A", "-o", "json"}
}

func ingressGetArgs(opts CaptureOpts) []string {
	if len(opts.Namespaces) == 0 {
		return []string{"get", "ingresses", "-A", "-o", "json"}
	}
	return []string{"get", "ingresses", "-A", "-o", "json"}
}

func parseWorkloads(raw []byte, kind string) ([]Workload, error) {
	var list k8sWorkloadList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}

	// Derive the canonical Kind string from the plural resource name.
	kindStr := pluralToKind(kind)

	var out []Workload
	for _, item := range list.Items {
		k := item.Kind
		if k == "" {
			k = kindStr
		}

		replicas := 1
		if k != "DaemonSet" && item.Spec.Replicas != nil {
			replicas = int(*item.Spec.Replicas)
		}

		var images []string
		var ports []int32
		var probePaths []string
		seen := map[int32]bool{}
		seenPaths := map[string]bool{}

		for _, ctr := range item.Spec.Template.Spec.Containers {
			if ctr.Image != "" {
				images = append(images, ctr.Image)
			}
			for _, p := range ctr.Ports {
				if p.ContainerPort > 0 && !seen[p.ContainerPort] {
					ports = append(ports, p.ContainerPort)
					seen[p.ContainerPort] = true
				}
			}
			for _, probe := range []*k8sProbe{ctr.LivenessProbe, ctr.ReadinessProbe, ctr.StartupProbe} {
				if probe != nil && probe.HTTPGet != nil && probe.HTTPGet.Path != "" {
					path := probe.HTTPGet.Path
					if !seenPaths[path] {
						probePaths = append(probePaths, path)
						seenPaths[path] = true
					}
				}
			}
		}

		out = append(out, Workload{
			Name:        item.Metadata.Name,
			Namespace:   item.Metadata.Namespace,
			Kind:        k,
			Replicas:    replicas,
			Images:      images,
			Ports:       ports,
			ProbePaths:  probePaths,
			Labels:      item.Spec.Template.Metadata.Labels,
			Annotations: filterAnnotations(item.Metadata.Annotations),
		})
	}
	return out, nil
}

// allowedAnnotationKeys is the complete set of workload annotations a capture carries. It is an
// allowlist, not a denylist, because annotations are an open key space that routinely holds object
// specs: "kubectl.kubernetes.io/last-applied-configuration" embeds the whole serialised object,
// container environment values included, so any cluster managed with `kubectl apply` would ship
// credentials in a capture taken with the zero-secret defaults. A denylist naming that one key fails
// the next annotation that embeds a spec; only an allowlist of keys a consumer actually reads holds.
//
// Every entry is a literal key with a bounded, non-spec value. Add a key here only when something
// reads it.
var allowedAnnotationKeys = map[string]struct{}{
	// Helm release identity. detectAddons reads release-name to map a workload to a construct kind;
	// release-namespace disambiguates two releases sharing a name.
	"meta.helm.sh/release-name":      {},
	"meta.helm.sh/release-namespace": {},

	// Argo CD ownership. The GitOps equivalent of the Helm release identity: on an Argo-managed
	// cluster this is the only annotation that says which application owns a workload.
	"argocd.argoproj.io/tracking-id": {},

	// Deployment revision — an integer, and the only rollout-generation signal on the object.
	"deployment.kubernetes.io/revision": {},

	// Scrape hints. These declare that a workload exposes metrics and on which port and path, which
	// is what decides whether it is modelled as an observed service. Both the Grafana
	// annotation-autodiscovery keys and the classic Prometheus ones are bounded scalars.
	"k8s.grafana.com/scrape":             {},
	"k8s.grafana.com/metrics.portNumber": {},
	"k8s.grafana.com/metrics.portName":   {},
	"k8s.grafana.com/metrics.path":       {},
	"k8s.grafana.com/metrics.scheme":     {},
	"prometheus.io/scrape":               {},
	"prometheus.io/port":                 {},
	"prometheus.io/path":                 {},
	"prometheus.io/scheme":               {},
}

// filterAnnotations reduces an object's annotations to the allowlisted keys. Returns nil when none
// survive, so an object with no interesting annotations serialises as null rather than {}.
func filterAnnotations(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	var out map[string]string
	for k, v := range in {
		if _, ok := allowedAnnotationKeys[k]; !ok {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(allowedAnnotationKeys))
		}
		out[k] = v
	}
	return out
}

func pluralToKind(plural string) string {
	switch plural {
	case "deployments":
		return "Deployment"
	case "statefulsets":
		return "StatefulSet"
	case "daemonsets":
		return "DaemonSet"
	default:
		return plural
	}
}

func filterByNamespace(ws []Workload, opts CaptureOpts) []Workload {
	if len(opts.Namespaces) == 0 && len(opts.ExcludeNamespaces) == 0 {
		return ws
	}
	allow := stringSet(opts.Namespaces)
	deny := stringSet(opts.ExcludeNamespaces)
	var out []Workload
	for _, w := range ws {
		if len(allow) > 0 && !allow[w.Namespace] {
			continue
		}
		if deny[w.Namespace] {
			continue
		}
		out = append(out, w)
	}
	return out
}

func parseServices(raw []byte) ([]Service, error) {
	var list k8sServiceList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	var out []Service
	for _, item := range list.Items {
		var ports []int32
		for _, p := range item.Spec.Ports {
			if p.Port > 0 {
				ports = append(ports, p.Port)
			}
		}
		out = append(out, Service{
			Name:         item.Metadata.Name,
			Namespace:    item.Metadata.Namespace,
			Type:         item.Spec.Type,
			ExternalName: item.Spec.ExternalName,
			Selector:     item.Spec.Selector,
			Ports:        ports,
		})
	}
	return out, nil
}

func filterServicesByNamespace(svcs []Service, opts CaptureOpts) []Service {
	if len(opts.Namespaces) == 0 && len(opts.ExcludeNamespaces) == 0 {
		return svcs
	}
	allow := stringSet(opts.Namespaces)
	deny := stringSet(opts.ExcludeNamespaces)
	var out []Service
	for _, s := range svcs {
		if len(allow) > 0 && !allow[s.Namespace] {
			continue
		}
		if deny[s.Namespace] {
			continue
		}
		out = append(out, s)
	}
	return out
}

func parseIngresses(raw []byte) ([]Ingress, error) {
	var list k8sIngressList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	var out []Ingress
	for _, item := range list.Items {
		var hosts []string
		backends := map[string]bool{}
		for _, rule := range item.Spec.Rules {
			if rule.Host != "" {
				hosts = append(hosts, rule.Host)
			}
			if rule.HTTP != nil {
				for _, path := range rule.HTTP.Paths {
					// networking.k8s.io/v1 backend
					if path.Backend.Service != nil && path.Backend.Service.Name != "" {
						backends[path.Backend.Service.Name] = true
					}
					// legacy v1beta1
					if path.Backend.ServiceName != "" {
						backends[path.Backend.ServiceName] = true
					}
				}
			}
		}
		var backendList []string
		for b := range backends {
			backendList = append(backendList, b)
		}
		out = append(out, Ingress{
			Name:      item.Metadata.Name,
			Namespace: item.Metadata.Namespace,
			Hosts:     hosts,
			Backends:  backendList,
		})
	}
	return out, nil
}

func filterIngressesByNamespace(ings []Ingress, opts CaptureOpts) []Ingress {
	if len(opts.Namespaces) == 0 && len(opts.ExcludeNamespaces) == 0 {
		return ings
	}
	allow := stringSet(opts.Namespaces)
	deny := stringSet(opts.ExcludeNamespaces)
	var out []Ingress
	for _, i := range ings {
		if len(allow) > 0 && !allow[i.Namespace] {
			continue
		}
		if deny[i.Namespace] {
			continue
		}
		out = append(out, i)
	}
	return out
}

// =============================================================================
// Addon detection — NO secret reads.
// Uses meta.helm.sh/release-name annotation + app.kubernetes.io/managed-by=Helm label on the
// workload objects already fetched, plus well-known namespace/deployment names as fallback.
// =============================================================================

// addonKindTable maps recognised Helm release names / component names to standalone synthkit
// construct kinds. Recognised products without a standalone addon kind produce an Addon with empty
// Kind for forge to classify against the broader construct catalog.
var addonKindTable = map[string]string{
	"karpenter":                    "karpenter",
	"cert-manager":                 "cert_manager",
	"argo-cd":                      "argocd",
	"argocd":                       "argocd",
	"coredns":                      "core_dns",
	"aws-load-balancer-controller": "load_balancer_controller",
	"external-dns":                 "external_dns",
	"aws-ebs-csi-driver":           "ebs_csi",
	"vpc-cni":                      "vpc_cni",
	"aws-node":                     "vpc_cni",
	"envoy-gateway":                "envoy_gateway",
}

// wellKnownNamespaces maps a namespace name to the component it implies.
var wellKnownNamespaces = map[string]string{
	"karpenter":            "karpenter",
	"cert-manager":         "cert-manager",
	"argocd":               "argo-cd",
	"external-dns":         "external-dns",
	"envoy-gateway-system": "envoy-gateway",
	// Platform products with no standalone synthkit addon construct still need to be
	// represented as detected names so forge can state the correct coverage verdict.
	"crossplane-system": "crossplane",
	"external-secrets":  "external-secrets",
	"arc-systems":       "actions-runner-controller",
	"github2otel":       "github2otel",
	"opencost":          "opencost",
}

// wellKnownDeployments maps exact deployment/statefulset/daemonset names to component names.
// The lookup also accepts a hyphen-delimited suffix because Helm and controller-managed names
// may append a release or role discriminator while retaining the product's stable prefix.
var wellKnownDeploymentNames = map[string]string{
	"karpenter":                       "karpenter",
	"cert-manager":                    "cert-manager",
	"argocd-server":                   "argo-cd",
	"argocd-application-controller":   "argo-cd",
	"aws-load-balancer-controller":    "aws-load-balancer-controller",
	"external-dns":                    "external-dns",
	"coredns":                         "coredns",
	"aws-node":                        "aws-node",
	"ebs-csi-controller":              "aws-ebs-csi-driver",
	"envoy-gateway":                   "envoy-gateway",
	"crossplane":                      "crossplane",
	"crossplane-rbac-manager":         "crossplane",
	"external-secrets":                "external-secrets",
	"external-secrets-webhook":        "external-secrets",
	"gha-rs-controller":               "actions-runner-controller",
	"gha-runner-scale-set-controller": "actions-runner-controller",
	"gha-runner-scale-set":            "actions-runner-controller",
	"github2otel":                     "github2otel",
	"opencost":                        "opencost",
}

func detectAddons(workloads []Workload, namespaces []string) []Addon {
	seen := map[string]bool{} // deduplicate by detected name
	var addons []Addon

	emit := func(detected, evidence string) {
		if seen[detected] {
			return
		}
		seen[detected] = true
		kind := addonKindTable[detected]
		addons = append(addons, Addon{
			Kind:     kind,
			Detected: detected,
			Evidence: evidence,
		})
	}

	// 1. Helm release-name annotation on the workload object (most reliable, NO secret reads).
	for _, w := range workloads {
		if rel := w.Annotations["meta.helm.sh/release-name"]; rel != "" {
			// The release name is the most direct operator signal; map it through the kind table
			// (an unrecognised release still emits an Addon with empty Kind = coverage gap).
			emit(rel, "helm-annotation")
		}
		// Name-based fallback for components not installed via Helm (e.g. EKS managed add-ons).
		if component, ok := wellKnownDeploymentComponent(w.Name); ok {
			emit(component, "deployment")
		}
	}

	// 2. Well-known namespaces as fallback.
	for _, ns := range namespaces {
		if component, ok := wellKnownNamespaces[ns]; ok {
			emit(component, "namespace")
		}
	}

	return addons
}

func wellKnownDeploymentComponent(name string) (string, bool) {
	if component, ok := wellKnownDeploymentNames[name]; ok {
		return component, true
	}
	for prefix, component := range wellKnownDeploymentNames {
		if strings.HasPrefix(name, prefix+"-") {
			return component, true
		}
	}
	return "", false
}

// =============================================================================
// Monitoring detection
// =============================================================================

// detectMonitoring records whether an in-cluster collector is present.
//
// Detection is on the container image, not the workload name. The k8s-monitoring chart names its
// collectors "<release>-alloy-<role>", so a workload-name prefix test misses every default install
// and leaves the capture claiming a cluster has no collector while five of them are running.
func detectMonitoring(workloads []Workload, namespaces []string) Monitoring {
	var m Monitoring

	for _, ns := range namespaces {
		if ns == "k8s-monitoring" {
			m.K8sMonitoring = true
		}
	}

	for _, w := range workloads {
		// The chart's own resources carry its name whatever namespace it was installed into; the
		// namespace is operator choice and is frequently not "k8s-monitoring".
		if strings.Contains(strings.ToLower(w.Name), "k8s-monitoring") {
			m.K8sMonitoring = true
		}
		for _, img := range w.Images {
			version, ok := alloyImageVersion(img)
			if !ok {
				continue
			}
			m.Alloy = true
			if m.AlloyVersion == "" && version != "" {
				m.AlloyVersion = version
			}
			if w.Namespace == "k8s-monitoring" {
				m.K8sMonitoring = true
			}
			break
		}
	}

	return m
}

// alloyImageVersion reports whether a container image reference is Grafana Alloy itself, and its
// tag when it carries one. It compares the repository's final path segment exactly, so a private
// registry mirror still matches while "alloy-operator" — which is not a collector — does not.
// A digest-pinned reference matches with an empty version rather than reporting the digest as one.
func alloyImageVersion(image string) (version string, ok bool) {
	ref := image
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	// Strip any digest FIRST. A tag-and-digest reference (alloy:v1.19.0@sha256:...) otherwise
	// splits on the ':' and carries the digest into the version.
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	name := ref
	if i := strings.Index(ref, ":"); i >= 0 {
		name, version = ref[:i], ref[i+1:]
	}
	if strings.ToLower(name) != "alloy" {
		return "", false
	}
	// A digest-only reference is recognised with no human-form version.
	return version, true
}

// =============================================================================
// Misc helpers
// =============================================================================

func stringSet(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		out[s] = true
	}
	return out
}
