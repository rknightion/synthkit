// SPDX-License-Identifier: AGPL-3.0-only

package inventory

// producerMismatchFindings reports the disjoint identities that the comparison
// cannot pair. An overlapping producer still compares through the unchanged Diff.
// Existing family unions cannot be split into job shapes after value elision.
func producerMismatchFindings(synth, reality Schema) []Finding {
	byName := make(map[string]Metric, len(synth.Metrics))
	for _, metric := range synth.Metrics {
		byName[metric.Name] = metric
	}
	var findings []Finding
	for _, observed := range reality.Metrics {
		modeled, ok := byName[observed.Name]
		if !ok || len(modeled.Producers) == 0 || len(observed.Producers) == 0 {
			continue
		}
		if sameProducerNames(modeled.Producers, observed.Producers) {
			continue
		}
		findings = append(findings, Finding{
			Kind: KindProducerMismatch, Disposition: DispositionCoverageGap,
			Signal: observed.Name, Field: "producers",
			SynthValues: producerNames(modeled.Producers), RealityValues: producerNames(observed.Producers),
		})
	}
	return findings
}

func producerNames(producers []Producer) []string {
	names := make([]string, 0, len(producers))
	for _, producer := range producers {
		names = append(names, producer.Name)
	}
	return sortedUniqueStrings(names)
}

func sameProducerNames(left, right []Producer) bool {
	return equalStrings(producerNames(left), producerNames(right))
}

// noComparableProducerFindings accounts for each unmatched family/producer once
// over the explicitly attributed evidence in scope. A family absent from every
// document remains outside corpus coverage. Legacy unattributed documents retain
// their original comparisons, but cannot assert a reviewed producer match.
func noComparableProducerFindings(synth Schema, documents []CorpusDocument) []ScopedFinding {
	var findings []ScopedFinding
	for _, metric := range synth.Metrics {
		var first *CorpusDocument
		var observed []Producer
		for i := range documents {
			doc := &documents[i]
			if synth.Provenance != nil && synth.Provenance.Substrate != "" && !containsString(doc.Authority.Substrates, synth.Provenance.Substrate) {
				continue
			}
			for _, real := range doc.Inventory.Metrics {
				if real.Name != metric.Name || len(real.Producers) == 0 {
					continue
				}
				if first == nil {
					first = doc
				}
				observed = append(observed, real.Producers...)
			}
		}
		if first == nil {
			continue
		}
		producers := metric.Producers
		if len(producers) == 0 {
			producers = []Producer{{Name: "(unrecorded)"}}
		}
		for _, name := range producerNames(producers) {
			if producersIntersect([]Producer{{Name: name}}, observed) {
				continue
			}
			findings = append(findings, ScopedFinding{
				Area: first.Area, Source: first.Source, Substrate: first.Source.Substrate,
				Finding: Finding{Kind: KindNoComparableProducer, Disposition: DispositionNoComparableProducer,
					Signal: metric.Name, Field: "producers", SynthValues: []string{name}, RealityValues: producerNames(observed)},
			})
		}
	}
	return findings
}
