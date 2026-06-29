// SPDX-License-Identifier: AGPL-3.0-only

// Package control: audience.go — the SINGLE place the customer/operator knob line is
// drawn (mirrors the predecessor's customerGroups). Customer self-serve sees only the master
// load knob and the curated incident scenarios; everything else is operator-only.
package control

// Audience selects which knobs a schema projection exposes.
type Audience string

const (
	AudienceOperator Audience = "operator" // full schema (default)
	AudienceCustomer Audience = "customer" // volume_multiplier + scenarios ONLY
)

// ParseAudience maps a query-string value to an Audience; anything unrecognised
// (including "") defaults to operator so a typo never widens, only narrows.
func ParseAudience(s string) Audience {
	if s == string(AudienceCustomer) {
		return AudienceCustomer
	}
	return AudienceOperator
}

// CustomerSchema projects a full operator schema to the customer-safe subset: the master
// VolumeMultiplier descriptor and the curated Scenarios (with their live Active state).
// Operator-only collections are zeroed. This is intentionally additive-safe: any new
// Schema field defaults to operator-only until explicitly allowed here.
func CustomerSchema(full Schema) Schema {
	return Schema{
		VolumeMultiplier: full.VolumeMultiplier,
		Scenarios:        full.Scenarios,
	}
}
