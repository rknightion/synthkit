// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const gcxLiveReadbackSource = "gcx_live_readback"

var cloudWatchMetricPrefixes = []string{
	"aws_amazonmwaa_",
	"aws_aoss_",
	"aws_applicationelb_",
	"aws_docdb_",
	"aws_ebs_",
	"aws_ec2_",
	"aws_eks_",
	"aws_elasticache_",
	"aws_firehose_",
	"aws_glue_",
	"aws_mwaa_",
	"aws_natgateway_",
	"aws_neptune_",
	"aws_networkelb_",
	"aws_privatelinkendpoints_",
	"aws_privatelinkservices_",
	"aws_rds_",
	"aws_s3_",
}

var retainedLiveLabelValues = map[string]struct{}{
	"api":                                    {},
	"created_by_kind":                        {},
	"error":                                  {},
	"fn":                                     {},
	"host_network":                           {},
	"ip_family":                              {},
	"label_beta_kubernetes_io_arch":          {},
	"label_beta_kubernetes_io_instance_type": {},
	"label_beta_kubernetes_io_os":            {},
	"label_failure_domain_beta_kubernetes_io_region": {},
	"label_failure_domain_beta_kubernetes_io_zone":   {},
	"label_kubernetes_io_arch":                       {},
	"label_kubernetes_io_os":                         {},
	"label_node_kubernetes_io_instance_type":         {},
	"label_topology_kubernetes_io_region":            {},
	"label_topology_kubernetes_io_zone":              {},
	"owner_is_controller":                            {},
	"owner_kind":                                     {},
	"reason":                                         {},
	"region":                                         {},
	"source":                                         {},
	"stat":                                           {},
	"status":                                         {},
	"table":                                          {},
	"traffic_policy":                                 {},
}

var trustedLiveJobValues = map[string]struct{}{
	"cloud/aws/amazonmwaa":                       {},
	"cloud/aws/aoss":                             {},
	"cloud/aws/applicationelb":                   {},
	"cloud/aws/docdb":                            {},
	"cloud/aws/ebs":                              {},
	"cloud/aws/ec2":                              {},
	"cloud/aws/eks":                              {},
	"cloud/aws/elasticache":                      {},
	"cloud/aws/firehose":                         {},
	"cloud/aws/glue":                             {},
	"cloud/aws/mwaa":                             {},
	"cloud/aws/natgateway":                       {},
	"cloud/aws/neptune":                          {},
	"cloud/aws/networkelb":                       {},
	"cloud/aws/privatelinkendpoints":             {},
	"cloud/aws/privatelinkservices":              {},
	"cloud/aws/rds":                              {},
	"cloud/aws/s3":                               {},
	"integrations/aws-vpc-cni":                   {},
	"integrations/kubernetes/kube-proxy":         {},
	"integrations/kubernetes/kube-state-metrics": {},
}

// prometheusMetadataInstruments maps the metric types a Prometheus-compatible metadata API
// reports onto the instrument vocabulary. A type outside this set, and the API's own "unknown",
// are absent evidence rather than an instrument claim.
var prometheusMetadataInstruments = map[string]string{
	"counter": InstrumentCounter,
	"gauge":   InstrumentGauge,
	// HISTOGRAM and GAUGEHISTOGRAM both expose the bucket contract recorded as a histogram.
	"histogram":      InstrumentHistogram,
	"gaugehistogram": InstrumentHistogram,
	"summary":        "summary",
	"info":           "info",
	"stateset":       "stateset",
}

// DeclaredInstrumentTypes reduces the metric-name-keyed metadata a Prometheus-compatible API
// returns to the distinct instrument types it declares for each name. Names the API reports no
// usable type for are omitted, so the caller records the unknown sentinel for them.
func DeclaredInstrumentTypes(metadata map[string][]string) map[string][]string {
	declared := make(map[string][]string, len(metadata))
	for name, types := range metadata {
		for _, reported := range types {
			instrument, ok := prometheusMetadataInstruments[strings.ToLower(strings.TrimSpace(reported))]
			if !ok {
				continue
			}
			existing := declared[name]
			if !slices.Contains(existing, instrument) {
				declared[name] = append(existing, instrument)
			}
		}
		sort.Strings(declared[name])
	}
	return declared
}

// BuildGCXLiveReadback converts Prometheus /series label sets returned by gcx into
// frozen substrate-scoped corpus documents. Deployment identities remain key-presence
// evidence only; only the small allowlist of stable enum/topology values is retained.
//
// gcxInstrumentTypeSource records the one mechanism this producer reads instrument types from,
// and why an entry can still carry the unknown sentinel. The API answers for the CloudWatch
// cloud-scraper ingest path and returns nothing at all for the Prometheus remote-write path,
// and its answer is a time-windowed snapshot rather than a catalogue — two reads twenty minutes
// apart differed by 34 names dropped and 21 added. A family the API does not answer for is
// therefore UNRESOLVED in this document, not untyped.
const gcxInstrumentTypeSource = "Stack Prometheus metadata API, read once per run and looked up by exact metric name through a fixed type table; a miss keeps the unknown sentinel. The API answers for the CloudWatch cloud-scraper ingest path, returns nothing for the Prometheus remote-write path, and its answer is a time-windowed snapshot rather than a catalogue, so an unknown here means unresolved rather than untyped."

// declaredInstruments carries the instrument types the same stack's metadata API reports,
// keyed by exact metric name. A metric the API reports no type for keeps the unknown
// sentinel: a type is never derived from the metric name.
func BuildGCXLiveReadback(series []map[string]string, declaredInstruments map[string][]string, capturedOn, collectorVersion string) ([]CorpusDocument, error) {
	if err := validateCapturedOn(capturedOn); err != nil {
		return nil, err
	}
	if strings.TrimSpace(collectorVersion) == "" {
		return nil, errors.New("collector version must not be empty")
	}

	inventories := map[string]Schema{
		"cw":         New(),
		"k8s":        New(),
		"k8s-addons": New(),
	}
	eksClusters := observedEKSClusters(series)
	// A classic histogram arrives as three component series. The synth and e2e inventories both
	// record the family, so this producer must too or every histogram family the read-back covers
	// contradicts on its name, instrument, label keys and bucket bounds at once. The proof gate
	// is what keeps CloudWatch's `<metric>_sum` GAUGE — a stat, not a histogram component —
	// under its own name, so a family synthkit really does not emit stays a coverage gap.
	histograms := ProveClassicHistogramsFromSeries(series)
	for _, labels := range series {
		name := labels["__name__"]
		var histogram *Histogram
		if family, folded := histograms.Family(name); folded {
			histogram = ClassicHistogramEvidence(labels)
			name = family
		}
		area, ok := liveMetricArea(name, labels)
		if !ok {
			continue
		}
		if (area == "k8s" || area == "k8s-addons") && !belongsToObservedEKS(labels, eksClusters) {
			continue
		}
		metricLabels := make(map[string]string, len(labels)-1)
		for key, value := range labels {
			if key != "__name__" && !identityBearingLiveLabelKey(key) {
				metricLabels[key] = value
			}
		}
		schema := inventories[area]
		instruments := declaredInstruments[name]
		if len(instruments) == 0 {
			instruments = []string{InstrumentUnknown}
		}
		for _, instrument := range instruments {
			schema.AddMetric(name, "", instrument, metricLabels, histogram)
		}
		inventories[area] = schema
	}

	documents := make([]CorpusDocument, 0, len(inventories))
	for area, schema := range inventories {
		if len(schema.Metrics) == 0 {
			continue
		}
		elideLiveDeploymentValues(&schema, area)
		document := CorpusDocument{
			CorpusVersion: CorpusVersion,
			Area:          area,
			Source: CorpusSource{
				Kind:      gcxLiveReadbackSource,
				Substrate: "eks",
				Collector: "grafana/gcx",
				// The CLI reads the stack back; it is not the collector under audit, so its
				// version is provenance and never merge identity.
				CollectorRole:        CollectorRoleReader,
				CollectorVersion:     collectorVersion,
				CapturedOn:           capturedOn,
				InstrumentTypeSource: gcxInstrumentTypeSource,
			},
			Authority: CorpusAuthority{Substrates: []string{"eks"}},
			CaptureVolume: CaptureVolume{
				Runs:                   1,
				ObservedContractCounts: []int{len(schema.Metrics)},
			},
			Inventory: schema,
		}
		normalizeCorpusDocument(&document)
		if err := validateCorpusDocument(document); err != nil {
			return nil, fmt.Errorf("build %s live read-back document: %w", area, err)
		}
		documents = append(documents, document)
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].Area < documents[j].Area })
	return documents, nil
}

func observedEKSClusters(series []map[string]string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, labels := range series {
		if labels["__name__"] != "kube_node_info" || !isEKSNodeInfo(labels) {
			continue
		}
		for _, key := range []string{"cluster", "k8s_cluster_name"} {
			if labels[key] != "" {
				out[labels[key]] = struct{}{}
			}
		}
	}
	return out
}

func belongsToObservedEKS(labels map[string]string, eksClusters map[string]struct{}) bool {
	if labels["__name__"] == "kube_node_info" {
		return isEKSNodeInfo(labels)
	}
	for _, key := range []string{"cluster", "k8s_cluster_name"} {
		if _, ok := eksClusters[labels[key]]; ok && labels[key] != "" {
			return true
		}
	}
	return false
}

func isEKSNodeInfo(labels map[string]string) bool {
	return strings.HasPrefix(labels["provider_id"], "aws://") &&
		strings.Contains(labels["kubelet_version"], "-eks-")
}

func liveMetricArea(name string, labels map[string]string) (string, bool) {
	switch {
	case name == "kube_node_info", name == "kube_node_labels", name == "kube_pod_info", name == "kube_pod_labels":
		return "k8s", true
	case strings.HasPrefix(name, "kubeproxy_"):
		return "k8s", true
	case name == "kubernetes_build_info" && labels["job"] == "integrations/kubernetes/kube-proxy":
		return "k8s", true
	case strings.HasPrefix(name, "awscni_"):
		return "k8s-addons", true
	}
	for _, prefix := range cloudWatchMetricPrefixes {
		if strings.HasPrefix(name, prefix) {
			return "cw", true
		}
	}
	return "", false
}

func identityBearingLiveLabelKey(key string) bool {
	// CloudWatch resource-tag discovery turns tag names into Prometheus label keys.
	// Those keys can contain a live cluster, account, tenant, or workload identity;
	// the frozen schema can elide values but not keys, so they cannot enter a public corpus.
	return strings.HasPrefix(key, "tag_")
}

func elideLiveDeploymentValues(schema *Schema, area string) {
	for metricIndex := range schema.Metrics {
		for attributeIndex := range schema.Metrics[metricIndex].Labels {
			attribute := &schema.Metrics[metricIndex].Labels[attributeIndex]
			_, retain := retainedLiveLabelValues[attribute.Key]
			if attribute.Key == "job" {
				retain = allTrustedLiveValues(attribute.Values, trustedLiveJobValues)
			}
			if area == "cw" && attribute.Key == "namespace" {
				retain = allOfficialCloudWatchNamespaces(attribute.Values)
			}
			if !retain {
				attribute.Values = []string{}
				attribute.ValuesElided = true
			}
		}
	}
	schema.Normalize()
	normalizeElidedValues(schema)
}

func allTrustedLiveValues(values []string, trusted map[string]struct{}) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if _, ok := trusted[value]; !ok {
			return false
		}
	}
	return true
}

func allOfficialCloudWatchNamespaces(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !strings.HasPrefix(value, "AWS/") || strings.ContainsAny(strings.TrimPrefix(value, "AWS/"), "/ ") {
			return false
		}
	}
	return true
}

// MergeCorpusDocumentFile atomically writes candidate or cumulative-merges it into
// an existing frozen corpus document. Absence in candidate never removes evidence.
func MergeCorpusDocumentFile(path string, candidate CorpusDocument) error {
	if err := validateCorpusDocument(candidate); err != nil {
		return fmt.Errorf("candidate corpus document: %w", err)
	}
	normalizeCorpusDocument(&candidate)
	document := candidate
	if _, err := os.Stat(path); err == nil {
		existing, loadErr := loadCorpusFile(path)
		if loadErr != nil {
			return loadErr
		}
		merged, mergeErr := CanonicalMerge(existing, candidate)
		if mergeErr != nil {
			return mergeErr
		}
		document = merged
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat corpus document %q: %w", path, err)
	}
	// A document written before this producer folded classic histograms carries the three
	// component series as three metric entries, and the cumulative merge never removes evidence,
	// so a refresh alone would keep them forever. Folding the written document brings that
	// history into the one family shape every producer now records.
	document.Inventory = FoldClassicHistogramMetrics(document.Inventory)
	dropSupersededInstrumentSentinel(&document.Inventory)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create corpus directory for %q: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".gcx-readback-*.json")
	if err != nil {
		return fmt.Errorf("create temporary corpus document for %q: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		temporary.Close()
		return fmt.Errorf("encode corpus document %q: %w", path, err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set corpus document permissions %q: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close corpus document %q: %w", path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace corpus document %q: %w", path, err)
	}
	return nil
}

// dropSupersededInstrumentSentinel removes the unknown sentinel from a metric that also
// carries an observed instrument type. The cumulative merge unions instrument types, so a
// refresh that finally observes a type would otherwise leave the earlier "we could not see
// one" marker beside it. The sentinel records absent evidence, and once evidence exists it
// records nothing. A metric whose only entry is the sentinel keeps it.
func dropSupersededInstrumentSentinel(schema *Schema) {
	for i := range schema.Metrics {
		types := schema.Metrics[i].InstrumentTypes
		if len(types) < 2 || !slices.Contains(types, InstrumentUnknown) {
			continue
		}
		observed := make([]string, 0, len(types)-1)
		for _, instrument := range types {
			if instrument != InstrumentUnknown {
				observed = append(observed, instrument)
			}
		}
		schema.Metrics[i].InstrumentTypes = observed
	}
}
