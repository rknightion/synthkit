// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"github.com/rknightion/synthkit/internal/inventory"
	"github.com/rknightion/synthkit/internal/runner"
)

// withSynthProvenance stamps the producer provenance the signal-fidelity comparator needs onto a
// dry-run inventory export.
//
// SelectorLabels carries the blueprint selector the composition root stamps on every
// blueprint-scoped series, stream and span (ARCHITECTURE I17). It is synthkit's own routing key,
// not a vendor name synthkit invented, and no capture of collector egress can ever carry it — so
// the comparator removes it from the synth view rather than reporting it as a label reality is
// missing. The key is read from runner.BlueprintLabel and never written as a literal here, so
// renaming the selector cannot silently reopen that finding class.
//
// Substrate is deliberately left unset. The export the fidelity gate consumes is produced with
// BLUEPRINT_NAMES='*', so it is the union of every blueprint plus the substrate-scoped catalog,
// and no single substrate describes it. Declaring one would make the comparator drop every corpus
// document captured on another substrate, taking real, substrate-independent findings with it.
func withSynthProvenance(schema inventory.Schema) inventory.Schema {
	schema.Provenance = &inventory.Provenance{SelectorLabels: []string{runner.BlueprintLabel}}
	return schema
}

func withMetricSuppressions(schema inventory.Schema, suppressions []runner.MetricSuppression) inventory.Schema {
	for _, suppression := range suppressions {
		schema.AddAllowListSuppression(suppression.Name, inventory.Producer{
			Name: suppression.Producer, AllowListVersion: suppression.AllowListVersion,
			AllowListVariant: suppression.AllowListVariant,
		})
	}
	return schema
}
