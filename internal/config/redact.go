// SPDX-License-Identifier: AGPL-3.0-only

package config

import "strconv"

// RedactedConfig is the secret-safe projection of Config for the control plane. Secret values are
// never included — only a Configured bool. config owns the classification (the secret list lives next
// to the fields); the composition root maps this to control.ConfigView for the wire.
type RedactedConfig struct {
	Groups []RedactedGroup
}

type RedactedGroup struct {
	Title  string
	Fields []RedactedField
}

type RedactedField struct {
	Key        string
	Value      string
	Secret     bool
	Configured bool
}

func safe(key, val string) RedactedField { return RedactedField{Key: key, Value: val} }
func secret(key, val string) RedactedField {
	return RedactedField{Key: key, Secret: true, Configured: val != ""}
}

// boolStr renders a bool as the literal env truth it came from.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(i int) string { return strconv.Itoa(i) }

// Redacted projects the resolved config to its secret-safe view. Every credential (Token/Password/
// Key field) goes through secret(); everything else through safe().
func (c *Config) Redacted() RedactedConfig {
	return RedactedConfig{Groups: []RedactedGroup{
		{Title: "Sinks (primary stack)", Fields: []RedactedField{
			safe("GC_PROM_RW", c.PromRWURL), safe("GC_PROM_USER", c.PromUser),
			safe("GC_OTLP_ENDPOINT", c.OTLPEndpoint), safe("GC_OTLP_USER", c.OTLPUser),
			safe("GC_LOKI", c.LokiURL), safe("GC_LOKI_USER", c.LokiUser),
			secret("GC_TOKEN", c.Token),
		}},
		{Title: "RUM (Faro)", Fields: []RedactedField{
			safe("GC_FARO_COLLECTOR", c.FaroCollector), secret("GC_FARO_APP_KEY", c.FaroAppKey),
		}},
		{Title: "Fleet Management", Fields: []RedactedField{
			safe("GC_FM_URL", c.FMURL), safe("GC_FM_STACK_ID", c.FMStackID), secret("GC_FM_TOKEN", c.FMToken),
		}},
		{Title: "Mode & cadence", Fields: []RedactedField{
			safe("DRY_RUN", boolStr(c.DryRun)), safe("TICK_DEFAULT", c.MasterTick.String()),
			safe("TICK_TIMEOUT", c.TickTimeout.String()), safe("SERIES_CAP", itoa(c.SeriesCap)),
		}},
		{Title: "Paths & bind", Fields: []RedactedField{
			safe("BLUEPRINTS", c.BlueprintsDir), safe("JSON_HTTP_ADDR", c.HTTPAddr),
			safe("CONFIG_SNAPSHOT_PATH", c.SnapshotPath), secret("CONTROL_TOKEN", c.ControlToken),
		}},
		{Title: "External blueprint sources", Fields: []RedactedField{
			safe("BLUEPRINT_DATA_DIR", c.BlueprintDataDir),
			safe("GIT_POLL_INTERVAL", itoa(c.GitPollInterval)),
			secret("GIT_TOKEN", c.GitTokenDefault),
		}},
		{Title: "Self-observability", Fields: []RedactedField{
			safe("SELFOBS_ENABLED", boolStr(c.SelfObsEnabled)), safe("GC_SELF_OTLP_ENDPOINT", c.SelfOTLPEndpoint),
			safe("GC_SELF_OTLP_USER", c.SelfOTLPUser), secret("GC_SELF_OTLP_PASSWORD", c.SelfOTLPPassword),
			safe("SELFOBS_TAGS", c.SelfObsTags), safe("GC_SELF_GRAFANA_URL", c.SelfGrafanaURL),
			safe("SELFOBS_METRIC_INTERVAL", c.SelfObsMetricInterval.String()),
		}},
		{Title: "Profiling (process)", Fields: []RedactedField{
			safe("GC_PYROSCOPE_URL", c.PyroscopeURL),
			safe("GC_PYROSCOPE_USER", c.PyroscopeUser), secret("GC_PYROSCOPE_PASSWORD", c.PyroscopePassword),
			safe("PYROSCOPE_TAGS", c.PyroscopeTags),
		}},
		{Title: "Synthetic profiles sink", Fields: []RedactedField{
			safe("GC_PROFILES_URL", c.ProfilesURL),
			safe("GC_PROFILES_USER", c.ProfilesUser), // auth reuses the shared GC_TOKEN (no separate profiles secret)
		}},
	}}
}
