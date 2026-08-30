// SPDX-License-Identifier: AGPL-3.0-only

package matrix

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rknightion/synthkit/internal/inventory"
)

// Promotion: turning one permutation's capture candidate into a corpus document (SKT-0013.01).
//
// A capture observes one disposable cluster for one window. Writing its observed values
// straight into the corpus would claim the corpus knows a value space it saw exactly one
// sample of, and the comparator would then treat a second deployment's perfectly valid value
// as drift. The corpus already has an encoding for "the key is real evidence, the value space
// is not enumerable from this capture": values_elided.
//
// The rule below is the OTel-native counterpart of the capture-instance identity rule in
// elide.go, and it is deliberately the STRICTER of the two directions: an attribute's values
// are retained only when the value set is fixed by the producing software's own contract —
// a semantic-conventions enum, or a receiver's declared attribute enum — and elided otherwise.
// Everything a deployment chooses (namespaces, node and pod names, image tags, interface and
// device names, CPU indices) is elided even though the capture saw it, because seeing one
// cluster's choice says nothing about the next one's.
//
// Retaining a value is therefore a positive claim that the enum is closed, and each entry
// below carries the reason it qualifies. Classic-histogram `le` is the one deliberately
// separate case: it is not a deployment identity or an enum, but the observed bucket boundary
// itself. Keeping its values is what makes the corpus state the bounds actually seen at egress.
var contractFixedAttributeValues = map[string]string{
	// OpenTelemetry semantic conventions: os.type is a closed enum of operating-system
	// families, and no deployment can invent a new member of it.
	"os.type": "OpenTelemetry semantic conventions closed enum",
	// hostmetrics and kubeletstats both declare direction as a closed two-value enum per
	// scraper: receive/transmit for network, read/write for disk.
	"direction": "receiver-declared closed enum (receive/transmit, read/write)",
	// Scraper-declared enums: CPU time states, memory usage states, and the TCP connection
	// states the network scraper counts by.
	"state": "receiver-declared closed enum per scraper (cpu states, memory states, TCP states)",
	// hostmetrics network.connections declares the protocol it counts connections for.
	"protocol": "receiver-declared closed enum",
	// k8sclusterreceiver emits one datapoint per Kubernetes container waiting/terminated
	// reason, so the observed set IS the enum rather than a sample of it.
	"k8s.container.status.reason": "Kubernetes container state reason enum, emitted exhaustively by k8sclusterreceiver",
}

// ContractFixedAttribute reports whether an attribute's observed values are evidence about a
// closed value space, and the reason that qualifies it.
func ContractFixedAttribute(key string) (string, bool) {
	reason, ok := contractFixedAttributeValues[key]
	return reason, ok
}

// canonicalPodLogSource is the corpus's established identifier for the pod-log stream, already
// used by the Alloy k3d document. A capture names the stream after whichever workload happened
// to write a line inside the window, which is capture-instance identity at the source level:
// folding to the canonical identifier keeps two producers' pod-log evidence comparable instead
// of splitting it by which pod was noisy.
const canonicalPodLogSource = "k8s_pod_logs"

// PromoteOptions selects what a candidate contributes to one corpus document. A document is
// one area, so a capture spanning several producer domains promotes once per area.
type PromoteOptions struct {
	// Metrics selects every captured metric family. Exclusion rules are applied after this
	// selection so a refresh can state which lab-owned or scrape-health namespace is out of
	// contract without maintaining a hand-curated inclusion list.
	Metrics bool
	// MetricPrefixes selects metric families by name prefix. Empty selects no metric.
	MetricPrefixes []string
	// ExcludeMetricPrefixes and ExcludeMetricNames state reproducible exclusions from the
	// selected metric set. Prefixes cover namespaces owned by the lab workload deck; exact
	// names avoid accidentally excluding target metrics that merely share a short prefix.
	ExcludeMetricPrefixes []string
	ExcludeMetricNames    []string
	// FoldPodLogs folds every captured log stream into the canonical pod-log source. Use it
	// only for a capture whose every stream IS a pod log and whose recorded source is a
	// workload name the classifier could not resolve.
	FoldPodLogs bool
	// Logs promotes the captured log entries AS CLASSIFIED, one per family, with every stream
	// label value elided. This is the right mode once the capture classifies its own streams:
	// folding would then destroy the distinction between the pod-log, cluster-events and
	// manifests lanes that the classifier just established.
	Logs bool
}

// PromoteCandidate reduces a capture candidate to the structural evidence one corpus document
// should carry. It never invents a name: every family, attribute key and retained value in the
// result was decoded at the capture receiver.
func PromoteCandidate(candidate inventory.Schema, options PromoteOptions) (inventory.Schema, error) {
	out := inventory.New()

	for _, metric := range candidate.Metrics {
		if !options.Metrics && !hasAnyPrefix(metric.Name, options.MetricPrefixes) {
			continue
		}
		if hasAnyPrefix(metric.Name, options.ExcludeMetricPrefixes) || contains(options.ExcludeMetricNames, metric.Name) {
			continue
		}
		promoted := metric
		promoted.Labels = make([]inventory.Attribute, 0, len(metric.Labels))
		for _, attribute := range metric.Labels {
			kept := inventory.Attribute{Key: attribute.Key}
			if retainObservedMetricValues(metric, attribute) {
				kept.Values = append([]string{}, attribute.Values...)
				sort.Strings(kept.Values)
			} else {
				kept.Values = []string{}
				kept.ValuesElided = true
			}
			promoted.Labels = append(promoted.Labels, kept)
		}
		out.Metrics = append(out.Metrics, promoted)
	}

	if options.Logs {
		for _, log := range candidate.Logs {
			promoted := inventory.Log{
				Source:                 log.Source,
				Transport:              log.Transport,
				StructuredMetadataKeys: append([]string{}, log.StructuredMetadataKeys...),
			}
			for _, label := range log.StreamLabels {
				// Every stream label of a real log lane names the deployment that produced the
				// line — its cluster, namespace, workload and container. The KEY set is the
				// contract and compares; the values are one lab's, matching the convention the
				// committed pod-log entries already follow.
				promoted.StreamLabels = append(promoted.StreamLabels, inventory.Attribute{
					Key: label.Key, Values: []string{}, ValuesElided: true,
				})
			}
			sort.Slice(promoted.StreamLabels, func(i, j int) bool {
				return promoted.StreamLabels[i].Key < promoted.StreamLabels[j].Key
			})
			sort.Strings(promoted.StructuredMetadataKeys)
			out.Logs = append(out.Logs, promoted)
		}
	}

	if options.FoldPodLogs && len(candidate.Logs) > 0 {
		folded := inventory.Log{Source: canonicalPodLogSource}
		keys := map[string]struct{}{}
		metadata := map[string]struct{}{}
		for _, log := range candidate.Logs {
			folded.Transport = log.Transport
			for _, label := range log.StreamLabels {
				keys[label.Key] = struct{}{}
			}
			for _, key := range log.StructuredMetadataKeys {
				metadata[key] = struct{}{}
			}
		}
		for key := range keys {
			// Every pod-log stream label is deployment identity: the pod, its controller, its
			// image and the service it resolves to. The keys are the contract; the values are
			// one cluster's.
			folded.StreamLabels = append(folded.StreamLabels, inventory.Attribute{
				Key: key, Values: []string{}, ValuesElided: true,
			})
		}
		sort.Slice(folded.StreamLabels, func(i, j int) bool { return folded.StreamLabels[i].Key < folded.StreamLabels[j].Key })
		for key := range metadata {
			folded.StructuredMetadataKeys = append(folded.StructuredMetadataKeys, key)
		}
		sort.Strings(folded.StructuredMetadataKeys)
		out.Logs = append(out.Logs, folded)
	}

	// Refused in EVERY empty shape, not only the missing-prefix one: a well-formed corpus
	// document holding nothing records a capture that did not happen, and the corpus is the
	// repository's only ground truth.
	if len(out.Metrics) == 0 && len(out.Logs) == 0 {
		if len(options.MetricPrefixes) > 0 {
			return inventory.Schema{}, fmt.Errorf("no captured metric family matches the selected prefixes %v; promoting an empty document would record a capture that did not happen", options.MetricPrefixes)
		}
		return inventory.Schema{}, fmt.Errorf("the promotion selected nothing from this candidate; promoting an empty document would record a capture that did not happen")
	}

	out.Normalize()
	return out, nil
}

// retainObservedMetricValues identifies the two value classes a k3d corpus promotion may keep.
// A contract-fixed attribute is a closed enum. `le` is not an enum: it remains only when the
// captured metric is already proved classic, so every retained value is an observed bucket
// boundary (including +Inf), never a bound selected by this producer.
func retainObservedMetricValues(metric inventory.Metric, attribute inventory.Attribute) bool {
	if attribute.ValuesElided {
		return false
	}
	if metric.Histogram != nil && metric.Histogram.Classic && attribute.Key == inventory.BucketBoundLabel {
		return true
	}
	_, contractFixed := ContractFixedAttribute(attribute.Key)
	return contractFixed
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
