// SPDX-License-Identifier: AGPL-3.0-only

package inventory

// mergeProducerObservation preserves attribution at the same boundary that builds
// the compatibility family union. Unattributed observations are retained too, so
// partial evidence can never silently exclude them from a comparison.
func mergeProducerObservation(out *Schema, observation Schema) {
	for _, metric := range observation.Metrics {
		merge := func(dst *[]Metric, scoped bool) {
			for i := range *dst {
				if (*dst)[i].Name == metric.Name && (!scoped || sameProducerNames((*dst)[i].Producers, metric.Producers)) {
					mergeMetric(&(*dst)[i], metric)
					return
				}
			}
			*dst = append(*dst, cloneMetric(metric))
		}
		merge(&out.Metrics, false)
		merge(&out.ProducerMetrics, true)
	}
}

// producerShapeSynth narrows only shapes observed directly for a matching
// producer. Legacy flattened inventories retain their existing comparisons.
func producerShapeSynth(synth, reality Schema) Schema {
	out := cloneSchema(synth)
	observed := map[string][]Producer{}
	for _, metric := range reality.Metrics {
		observed[metric.Name] = metric.Producers
	}
	byFamily := map[string][]Metric{}
	for _, metric := range synth.ProducerMetrics {
		byFamily[metric.Name] = append(byFamily[metric.Name], metric)
	}
	for i, metric := range out.Metrics {
		shapes := byFamily[metric.Name]
		producers := observed[metric.Name]
		if len(shapes) == 0 || len(producers) == 0 {
			continue
		}
		var scoped *Metric
		for _, shape := range shapes {
			if len(shape.Producers) > 0 && !producersIntersect(shape.Producers, producers) {
				continue
			}
			if scoped == nil {
				m := cloneMetric(shape)
				scoped = &m
			} else {
				mergeMetric(scoped, shape)
			}
		}
		if scoped != nil {
			out.Metrics[i] = *scoped
		}
	}
	return out
}
