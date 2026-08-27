// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildGCXLiveReadbackScopesAreasAndElidesDeploymentIdentity(t *testing.T) {
	t.Parallel()
	series := []map[string]string{
		{
			"__name__": "kube_node_info", "cluster": "deployment-cluster", "provider_id": "aws:///region-zone/i-instance",
			"job": "integrations/kubernetes/kube-state-metrics", "kubelet_version": "v1.35.2-eks-build", "source": "kubernetes",
		},
		{
			"__name__": "kube_node_info", "cluster": "deployment-cluster", "provider_id": "aws:///region-zone/i-other",
			"job": "deployment-specific-job", "kubelet_version": "v1.35.2-eks-build",
		},
		{
			"__name__": "kube_node_labels", "cluster": "deployment-cluster", "node": "node-name",
			"label_node_kubernetes_io_instance_type": "m6g.large", "label_topology_kubernetes_io_region": "region",
			"label_topology_kubernetes_io_zone": "region-zone", "label_karpenter_sh_nodepool": "deployment-pool",
		},
		{
			"__name__": "kube_pod_info", "cluster": "deployment-cluster", "pod": "deployment-pod", "uid": "deployment-uid",
			"created_by_kind": "DaemonSet", "namespace": "deployment-namespace",
		},
		{
			"__name__": "awscni_ipamd_action_inprogress", "cluster": "deployment-cluster",
			"fn": "nodeIPPoolReconcile", "instance": "node-address",
		},
		{
			"__name__": "kubeproxy_sync_proxy_rules_iptables_total", "cluster": "deployment-cluster",
			"ip_family": "IPv4", "table": "nat", "instance": "node-address",
		},
		{
			"__name__": "aws_ec2_cpu_credit_usage_sum", "aws_account_id": "account-id",
			"dimension_InstanceId": "i-instance", "namespace": "AWS/EC2", "region": "region",
			"tag_kubernetes_io_cluster_deployment_name": "owned",
		},
		{"__name__": "aws_bedrock_invocations_sum", "model_id": "deployment-model"},
		{"__name__": "aws_appflow_flow_executions_sum", "flow_name": "deployment-flow"},
		{"__name__": "kube_node_info", "cluster": "self-managed-aws", "provider_id": "aws:///region-zone/i-self", "kubelet_version": "v1.35.2"},
		{"__name__": "kube_node_labels", "cluster": "self-managed-aws", "label_node_kubernetes_io_instance_type": "self-managed.type"},
		{"__name__": "kube_node_info", "cluster": "other-cluster", "provider_id": "local://node", "kubelet_version": "v1.35.2"},
		{"__name__": "kube_node_labels", "cluster": "other-cluster", "label_node_kubernetes_io_instance_type": "other.type"},
	}

	declared := DeclaredInstrumentTypes(map[string][]string{
		"aws_ec2_cpu_credit_usage_sum": {"gauge"},
		"kube_node_info":               {"gauge"},
	})
	documents, err := BuildGCXLiveReadback(series, declared, "2026-08-25", "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if got := documentAreas(documents); !reflect.DeepEqual(got, []string{"cw", "k8s", "k8s-addons"}) {
		t.Fatalf("areas=%v, want cw/k8s/k8s-addons", got)
	}

	k8s := documentForArea(t, documents, "k8s")
	if k8s.Source.Substrate != "eks" || !reflect.DeepEqual(k8s.Authority.Substrates, []string{"eks"}) {
		t.Fatalf("k8s substrate/authority=%q/%v, want eks singleton", k8s.Source.Substrate, k8s.Authority.Substrates)
	}
	if k8s.Source.CapturedOn != "2026-08-25" || k8s.Source.CollectorVersion != "1.1.1" {
		t.Fatalf("k8s source=%+v", k8s.Source)
	}
	assertAttribute(t, metricForName(t, k8s, "kube_node_info"), "provider_id", nil, true)
	// One deployment-specific observation makes the complete value set open-ended;
	// retaining only the trusted value would falsely claim the discarded value never existed.
	assertAttribute(t, metricForName(t, k8s, "kube_node_info"), "job", nil, true)
	assertAttribute(t, metricForName(t, k8s, "kube_node_labels"), "label_node_kubernetes_io_instance_type", []string{"m6g.large"}, false)
	assertAttribute(t, metricForName(t, k8s, "kube_node_labels"), "label_topology_kubernetes_io_region", []string{"region"}, false)
	assertAttribute(t, metricForName(t, k8s, "kube_node_labels"), "label_karpenter_sh_nodepool", nil, true)
	assertAttribute(t, metricForName(t, k8s, "kube_pod_info"), "created_by_kind", []string{"DaemonSet"}, false)
	assertAttribute(t, metricForName(t, k8s, "kube_pod_info"), "pod", nil, true)
	assertAttribute(t, metricForName(t, k8s, "kube_pod_info"), "namespace", nil, true)
	assertAttribute(t, metricForName(t, k8s, "kubeproxy_sync_proxy_rules_iptables_total"), "table", []string{"nat"}, false)

	addons := documentForArea(t, documents, "k8s-addons")
	assertAttribute(t, metricForName(t, addons, "awscni_ipamd_action_inprogress"), "fn", []string{"nodeIPPoolReconcile"}, false)

	cw := documentForArea(t, documents, "cw")
	if got := metricNames(cw); !reflect.DeepEqual(got, []string{"aws_ec2_cpu_credit_usage_sum"}) {
		t.Fatalf("cw metrics=%v; AI/LLM families must be excluded", got)
	}
	assertAttribute(t, metricForName(t, cw, "aws_ec2_cpu_credit_usage_sum"), "aws_account_id", nil, true)
	assertAttribute(t, metricForName(t, cw, "aws_ec2_cpu_credit_usage_sum"), "dimension_InstanceId", nil, true)
	assertAttribute(t, metricForName(t, cw, "aws_ec2_cpu_credit_usage_sum"), "namespace", []string{"AWS/EC2"}, false)
	assertAttribute(t, metricForName(t, cw, "aws_ec2_cpu_credit_usage_sum"), "region", []string{"region"}, false)
	// The metadata API answered for the CloudWatch family and not for the kubeproxy one,
	// so the second keeps the sentinel rather than taking a type from its name.
	if got := metricForName(t, cw, "aws_ec2_cpu_credit_usage_sum").InstrumentTypes; !reflect.DeepEqual(got, []string{InstrumentGauge}) {
		t.Fatalf("cw instrument types=%v, want [gauge]", got)
	}
	if got := metricForName(t, k8s, "kube_node_info").InstrumentTypes; !reflect.DeepEqual(got, []string{InstrumentGauge}) {
		t.Fatalf("kube_node_info instrument types=%v, want [gauge]", got)
	}
	if got := metricForName(t, k8s, "kubeproxy_sync_proxy_rules_iptables_total").InstrumentTypes; !reflect.DeepEqual(got, []string{InstrumentUnknown}) {
		t.Fatalf("kubeproxy instrument types=%v, want [unknown]", got)
	}
	for _, attribute := range metricForName(t, cw, "aws_ec2_cpu_credit_usage_sum").Labels {
		if strings.HasPrefix(attribute.Key, "tag_") {
			t.Fatalf("identity-bearing CloudWatch tag key entered corpus: %q", attribute.Key)
		}
	}
}

func TestMergeCorpusDocumentFileUsesCanonicalCumulativeUnion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cw", "eks-live-readback.json")
	first, err := BuildGCXLiveReadback([]map[string]string{{"__name__": "aws_ec2_cpu_credit_usage_sum", "region": "a"}}, nil, "2026-08-24", "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildGCXLiveReadback([]map[string]string{{"__name__": "aws_ec2_cpu_credit_usage_sum", "region": "b"}}, DeclaredInstrumentTypes(map[string][]string{"aws_ec2_cpu_credit_usage_sum": {"gauge"}}), "2026-08-25", "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := MergeCorpusDocumentFile(path, documentForArea(t, first, "cw")); err != nil {
		t.Fatal(err)
	}
	if err := MergeCorpusDocumentFile(path, documentForArea(t, second, "cw")); err != nil {
		t.Fatal(err)
	}
	documents, err := LoadCorpusDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	merged := documentForArea(t, documents, "cw")
	// The frozen cross-capture rule elides a value set as soon as a later run differs;
	// it does not accumulate both values as an asserted finite enumeration.
	assertAttribute(t, metricForName(t, merged, "aws_ec2_cpu_credit_usage_sum"), "region", nil, true)
	if merged.CaptureVolume.Runs != 2 || merged.Source.CapturedOn != "2026-08-25" {
		t.Fatalf("merged provenance=%+v volume=%+v", merged.Source, merged.CaptureVolume)
	}
	if !reflect.DeepEqual(merged.CaptureVolume.ObservedContractCounts, []int{1}) {
		t.Fatalf("merged observed contract counts=%v, want sorted distinct [1]", merged.CaptureVolume.ObservedContractCounts)
	}
}

func documentAreas(documents []CorpusDocument) []string {
	out := make([]string, len(documents))
	for i := range documents {
		out[i] = documents[i].Area
	}
	return out
}

func documentForArea(t *testing.T, documents []CorpusDocument, area string) CorpusDocument {
	t.Helper()
	for _, document := range documents {
		if document.Area == area {
			return document
		}
	}
	t.Fatalf("missing document for area %q", area)
	return CorpusDocument{}
}

func metricForName(t *testing.T, document CorpusDocument, name string) Metric {
	t.Helper()
	for _, metric := range document.Inventory.Metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("missing metric %q in area %q", name, document.Area)
	return Metric{}
}

func metricNames(document CorpusDocument) []string {
	out := make([]string, len(document.Inventory.Metrics))
	for i := range document.Inventory.Metrics {
		out[i] = document.Inventory.Metrics[i].Name
	}
	return out
}

func assertAttribute(t *testing.T, metric Metric, key string, values []string, elided bool) {
	t.Helper()
	for _, attribute := range metric.Labels {
		if attribute.Key == key {
			valuesMatch := len(attribute.Values) == len(values)
			for i := range values {
				valuesMatch = valuesMatch && attribute.Values[i] == values[i]
			}
			if !valuesMatch || attribute.ValuesElided != elided {
				t.Fatalf("metric %q attribute %q=%v elided=%v, want %v elided=%v", metric.Name, key, attribute.Values, attribute.ValuesElided, values, elided)
			}
			return
		}
	}
	t.Fatalf("metric %q missing attribute %q", metric.Name, key)
}

func TestDeclaredInstrumentTypesKeepsOnlyReportedTypes(t *testing.T) {
	t.Parallel()
	got := DeclaredInstrumentTypes(map[string][]string{
		"a_counter":     {"counter"},
		"a_gauge":       {"gauge"},
		"a_histogram":   {"histogram"},
		"a_gaugehisto":  {"gaugehistogram"},
		"a_summary":     {"summary"},
		"an_info":       {"info"},
		"a_stateset":    {"stateset"},
		"an_untyped":    {"unknown"},
		"a_nonsense":    {"not-a-prometheus-type"},
		"a_disagreeing": {"counter", "gauge", "counter"},
	})
	want := map[string][]string{
		"a_counter":     {InstrumentCounter},
		"a_gauge":       {InstrumentGauge},
		"a_histogram":   {InstrumentHistogram},
		"a_gaugehisto":  {InstrumentHistogram},
		"a_summary":     {"summary"},
		"an_info":       {"info"},
		"a_stateset":    {"stateset"},
		"a_disagreeing": {InstrumentCounter, InstrumentGauge},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("declared=%v, want %v", got, want)
	}
}

func TestMergeCorpusDocumentFileDropsSupersededInstrumentSentinel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cw", "eks-live-readback.json")
	series := []map[string]string{{"__name__": "aws_ec2_cpu_credit_usage_sum", "region": "a"}}
	// The first capture could not observe a type; the refresh can.
	unobserved, err := BuildGCXLiveReadback(series, nil, "2026-08-24", "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	observed, err := BuildGCXLiveReadback(series, DeclaredInstrumentTypes(map[string][]string{
		"aws_ec2_cpu_credit_usage_sum": {"gauge"},
	}), "2026-08-25", "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := MergeCorpusDocumentFile(path, documentForArea(t, unobserved, "cw")); err != nil {
		t.Fatal(err)
	}
	if err := MergeCorpusDocumentFile(path, documentForArea(t, observed, "cw")); err != nil {
		t.Fatal(err)
	}
	documents, err := LoadCorpusDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	merged := documentForArea(t, documents, "cw")
	if got := metricForName(t, merged, "aws_ec2_cpu_credit_usage_sum").InstrumentTypes; !reflect.DeepEqual(got, []string{InstrumentGauge}) {
		t.Fatalf("merged instrument types=%v, want [gauge] with the sentinel dropped", got)
	}
}

func TestBuildGCXLiveReadbackFoldsClassicHistogramComponents(t *testing.T) {
	t.Parallel()
	node := map[string]string{
		"__name__": "kube_node_info", "cluster": "c", "provider_id": "aws:///r-z/i-1",
		"kubelet_version": "v1.35.2-eks-build",
	}
	series := []map[string]string{
		node,
		{"__name__": "kubeproxy_sync_proxy_rules_duration_seconds_bucket", "cluster": "c", "ip_family": "IPv4", "le": "0.001"},
		{"__name__": "kubeproxy_sync_proxy_rules_duration_seconds_bucket", "cluster": "c", "ip_family": "IPv4", "le": "+Inf"},
		{"__name__": "kubeproxy_sync_proxy_rules_duration_seconds_sum", "cluster": "c", "ip_family": "IPv4"},
		{"__name__": "kubeproxy_sync_proxy_rules_duration_seconds_count", "cluster": "c", "ip_family": "IPv4"},
	}
	documents, err := BuildGCXLiveReadback(series, DeclaredInstrumentTypes(map[string][]string{
		"kubeproxy_sync_proxy_rules_duration_seconds": {"histogram"},
	}), "2026-08-27", "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	k8s := documentForArea(t, documents, "k8s")
	if got := metricNames(k8s); !reflect.DeepEqual(got, []string{
		"kube_node_info", "kubeproxy_sync_proxy_rules_duration_seconds",
	}) {
		t.Fatalf("metric names=%v, want the three components folded into one family", got)
	}
	family := metricForName(t, k8s, "kubeproxy_sync_proxy_rules_duration_seconds")
	if !reflect.DeepEqual(family.InstrumentTypes, []string{InstrumentHistogram}) {
		t.Fatalf("instrument types=%v, want the type the metadata API declares for the family", family.InstrumentTypes)
	}
	if family.Histogram == nil || !family.Histogram.Classic ||
		!reflect.DeepEqual(family.Histogram.BucketBounds, []float64{0.001}) {
		t.Fatalf("histogram=%+v, want the classic representation with the finite observed bound", family.Histogram)
	}
	assertAttribute(t, family, "le", []string{}, true)
}

func TestBuildGCXLiveReadbackKeepsCloudWatchStatSuffixesUnfolded(t *testing.T) {
	t.Parallel()
	series := []map[string]string{
		{"__name__": "aws_rds_cpuutilization_sum", "namespace": "AWS/RDS", "region": "r"},
		{"__name__": "aws_rds_cpuutilization_sample_count", "namespace": "AWS/RDS", "region": "r"},
		{"__name__": "aws_rds_cpuutilization_average", "namespace": "AWS/RDS", "region": "r"},
	}
	documents, err := BuildGCXLiveReadback(series, nil, "2026-08-27", "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	cw := documentForArea(t, documents, "cw")
	want := []string{"aws_rds_cpuutilization_average", "aws_rds_cpuutilization_sample_count", "aws_rds_cpuutilization_sum"}
	if got := metricNames(cw); !reflect.DeepEqual(got, want) {
		t.Fatalf("metric names=%v, want %v; the CloudWatch stat suffixes are not histogram components "+
			"and folding them would hide a family synthkit does not emit", got, want)
	}
}

func TestMergeCorpusDocumentFileFoldsAComponentShapedDocument(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "k8s", "eks-live-readback.json")
	// A document recorded before the producer folded: three component entries, `le` elided.
	legacy := CorpusDocument{
		CorpusVersion: CorpusVersion,
		Area:          "k8s",
		Source: CorpusSource{
			Kind: gcxLiveReadbackSource, Substrate: "eks", Collector: "grafana/gcx",
			CollectorVersion: "1.1.1", CapturedOn: "2026-08-26",
		},
		Authority:     CorpusAuthority{Substrates: []string{"eks"}},
		CaptureVolume: CaptureVolume{Runs: 1, ObservedContractCounts: []int{3}},
		Inventory:     New(),
	}
	legacy.Inventory.AddMetric("kubeproxy_network_programming_duration_seconds_bucket", "", InstrumentUnknown, map[string]string{"cluster": "", "le": ""}, nil)
	legacy.Inventory.AddMetric("kubeproxy_network_programming_duration_seconds_sum", "", InstrumentUnknown, map[string]string{"cluster": ""}, nil)
	legacy.Inventory.AddMetric("kubeproxy_network_programming_duration_seconds_count", "", InstrumentUnknown, map[string]string{"cluster": ""}, nil)
	if err := MergeCorpusDocumentFile(path, legacy); err != nil {
		t.Fatal(err)
	}
	documents, err := LoadCorpusDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	merged := documentForArea(t, documents, "k8s")
	if got := metricNames(merged); !reflect.DeepEqual(got, []string{"kubeproxy_network_programming_duration_seconds"}) {
		t.Fatalf("metric names=%v, want the stale component entries folded into the family; the "+
			"cumulative merge never removes evidence, so a refresh alone would keep them forever", got)
	}
	family := metricForName(t, merged, "kubeproxy_network_programming_duration_seconds")
	if family.Histogram == nil || !family.Histogram.Classic || len(family.Histogram.BucketBounds) != 0 {
		t.Fatalf("histogram=%+v, want classic with no bounds: a recorded document elides the `le` values", family.Histogram)
	}
}
