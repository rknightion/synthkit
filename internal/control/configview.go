// SPDX-License-Identifier: AGPL-3.0-only

package control

// ConfigView is the REDACTED runtime config served at GET /control/config. Secret fields are NEVER
// included as values — only a Configured bool. Built at the composition root from config.Redacted();
// control owns the wire shape, config owns the secret classification.
type ConfigView struct {
	Groups []ConfigGroup `json:"groups"`
}

// ConfigGroup is one labelled section of fields (Sinks, FM, Cadence, …).
type ConfigGroup struct {
	Title  string        `json:"title"`
	Fields []ConfigField `json:"fields"`
}

// ConfigField is one config value. Exactly one of Value / Configured is meaningful: Secret=false →
// Value is the literal; Secret=true → Value is "" and Configured reports presence.
type ConfigField struct {
	Key        string `json:"key"`        // env var name, e.g. "GC_PROM_RW"
	Value      string `json:"value"`      // literal value for non-secrets; "" for secrets
	Secret     bool   `json:"secret"`     // true ⇒ value redacted
	Configured bool   `json:"configured"` // for secrets: whether a value is set
}
