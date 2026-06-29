// SPDX-License-Identifier: AGPL-3.0-only

package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

	cl.NodeGroups, cl.Provider, cl.Region = synthesizeNodeGroups(nodeList)
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
	// Cluster name: derive from kubectl config current-context (best-effort).
	// EKS contexts are usually ARNs (arn:aws:eks:region:acct:cluster/NAME) — recover the trailing
	// cluster name and slugify it so it is valid as both a cluster name and a blueprint-name stem.
	// -------------------------------------------------------------------------
	if ctxRaw, cerr := run(ctx, "config", "current-context"); cerr == nil {
		cl.Name = clusterNameFromContext(string(ctxRaw))
	}
	if cl.Name == "" {
		cl.Name = "captured-cluster"
	}

	inv.Clusters = append(inv.Clusters, cl)
	return nil
}

// clusterNameFromContext recovers a clean cluster slug from a kubectl context string. For an EKS ARN
// it takes the segment after the final "/" or ":" (the real cluster name); it then keeps only
// lowercase alphanumerics and "-" (mapping "_", ".", " " to "-"). Returns "" when nothing usable
// remains (the caller falls back to a default).
func clusterNameFromContext(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.LastIndexAny(s, "/:"); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
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

// nodeGroupKey is used as a grouping key.
type nodeGroupKey struct {
	instanceType string
	provisioner  string
	os           string
}

func synthesizeNodeGroups(nodes []k8sNode) (groups []NodeGroup, provider string, region string) {
	type groupAccum struct {
		names []string // collected nodegroup label values
		count int
	}
	accum := map[nodeGroupKey]*groupAccum{}
	keyOrder := []nodeGroupKey{}

	for _, n := range nodes {
		lbl := n.Metadata.Labels

		// Provider detection from label families.
		if provider == "" {
			switch {
			case hasLabelPrefix(lbl, "eks.amazonaws.com/"):
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

		// Provisioner.
		prov := "managed"
		if hasLabelPrefix(lbl, "karpenter.sh/") {
			prov = "karpenter"
		}

		instanceType := lbl["node.kubernetes.io/instance-type"]
		os := lbl["kubernetes.io/os"]
		if os == "" {
			os = "linux"
		}

		key := nodeGroupKey{instanceType: instanceType, provisioner: prov, os: os}
		if _, ok := accum[key]; !ok {
			accum[key] = &groupAccum{}
			keyOrder = append(keyOrder, key)
		}
		accum[key].count++

		// Collect EKS nodegroup names for this key.
		if ng := lbl["eks.amazonaws.com/nodegroup"]; ng != "" {
			accum[key].names = append(accum[key].names, ng)
		}
	}

	for _, key := range keyOrder {
		acc := accum[key]
		// Prefer the EKS nodegroup label value as the name; fall back to synthesized key.
		name := ""
		if len(acc.names) > 0 {
			name = acc.names[0] // all nodes in the group share the same label
		}
		if name == "" {
			name = key.instanceType + "-" + key.provisioner
		}
		groups = append(groups, NodeGroup{
			Name:         name,
			InstanceType: key.instanceType,
			Count:        acc.count,
			Provisioner:  key.provisioner,
			OS:           key.os,
		})
	}

	if provider == "" {
		provider = "unknown"
	}
	return groups, provider, region
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
			Annotations: item.Metadata.Annotations,
		})
	}
	return out, nil
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

// addonKindTable maps recognised Helm release names / component names to synthkit construct kinds.
// Unrecognised names produce an Addon with empty Kind (coverage-gap signal).
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
}

// wellKnownDeployments maps deployment name prefixes to component names.
var wellKnownDeploymentNames = map[string]string{
	"karpenter":                     "karpenter",
	"cert-manager":                  "cert-manager",
	"argocd-server":                 "argo-cd",
	"argocd-application-controller": "argo-cd",
	"aws-load-balancer-controller":  "aws-load-balancer-controller",
	"external-dns":                  "external-dns",
	"coredns":                       "coredns",
	"aws-node":                      "aws-node",
	"ebs-csi-controller":            "aws-ebs-csi-driver",
	"envoy-gateway":                 "envoy-gateway",
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
		if component, ok := wellKnownDeploymentNames[w.Name]; ok {
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

// =============================================================================
// Monitoring detection
// =============================================================================

func detectMonitoring(workloads []Workload, namespaces []string) Monitoring {
	var m Monitoring

	for _, ns := range namespaces {
		if ns == "k8s-monitoring" {
			m.K8sMonitoring = true
		}
	}

	for _, w := range workloads {
		name := strings.ToLower(w.Name)
		if strings.HasPrefix(name, "alloy") {
			m.Alloy = true
			// Best-effort: extract version from the image tag.
			for _, img := range w.Images {
				if strings.Contains(strings.ToLower(img), "alloy") {
					if idx := strings.LastIndex(img, ":"); idx >= 0 {
						m.AlloyVersion = img[idx+1:]
					}
					break
				}
			}
			// If this alloy workload is in the k8s-monitoring namespace, mark K8sMonitoring too.
			if w.Namespace == "k8s-monitoring" {
				m.K8sMonitoring = true
			}
		}
	}

	return m
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
