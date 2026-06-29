// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"reflect"
	"strings"
	"testing"
)

// fullConfig — every string field set to a UNIQUE, recognisable sentinel so the reflection-driven
// leak test can detect any secret value that escapes into the view. Secret sentinels start "SEC-".
func fullConfig() *Config {
	return &Config{
		PromRWURL: "https://prom", PromUser: "111", OTLPEndpoint: "https://otlp", OTLPUser: "222",
		LokiURL: "https://loki", LokiUser: "333", Token: "SEC-GC", FaroCollector: "https://faro",
		FaroAppKey: "SEC-FARO", FMURL: "https://fm", FMStackID: "444", FMToken: "SEC-FM",
		DryRun: false, MasterTick: 5_000_000_000, TickTimeout: 0, SeriesCap: 0,
		BlueprintsDir: "./blueprints", BlueprintDataDir: "./data/blueprints", HTTPAddr: "127.0.0.1:8088",
		SnapshotPath: "./control-state.json", ControlToken: "SEC-CTL",
		GitPollInterval: 0, GitTokenDefault: "SEC-GIT",
		SelfObsEnabled: true, SelfOTLPEndpoint: "https://self", SelfOTLPUser: "555",
		SelfOTLPPassword: "SEC-SELF", SelfObsTags: "a=b", SelfGrafanaURL: "https://staff.example.net",
		PyroscopeURL: "https://pyro", PyroscopeUser: "666",
		PyroscopePassword: "SEC-PYRO", PyroscopeTags: "c=d", PyroscopeMutexFraction: 0, PyroscopeBlockRate: 0,
		ProfilesURL: "https://prof", ProfilesUser: "777",
	}
}

// emittedValues collects every Value string the view would serialise.
func emittedValues(v RedactedConfig) []string {
	var out []string
	for _, g := range v.Groups {
		for _, f := range g.Fields {
			out = append(out, f.Value)
		}
	}
	return out
}

// REFLECTION-DRIVEN leak guard: for EVERY Config field whose Go name contains Token/Password/Key, its
// value must NOT appear anywhere in the emitted view. Future-proof — a new secret field added to
// Config (and accidentally exposed via safe()) fails here automatically.
func TestRedactedNeverLeaksAnySecretValue(t *testing.T) {
	cfg := fullConfig()
	emitted := strings.Join(emittedValues(cfg.Redacted()), "\x00")
	rv := reflect.ValueOf(*cfg)
	rt := rv.Type()
	checked := 0
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if !(strings.Contains(name, "Token") || strings.Contains(name, "Password") || strings.Contains(name, "Key")) {
			continue
		}
		val, _ := rv.Field(i).Interface().(string)
		if val == "" {
			t.Fatalf("test bug: secret field %s must be non-empty in fullConfig()", name)
		}
		checked++
		if strings.Contains(emitted, val) {
			t.Fatalf("redacted view LEAKED secret field %s value %q", name, val)
		}
	}
	if checked < 7 {
		t.Fatalf("expected >=7 secret-looking fields checked, got %d (did Config change?)", checked)
	}
}

// The 7 known credentials must be PRESENT as Secret:true with Configured reflecting presence — i.e.
// shown as set/unset, not silently dropped (that's the feature).
func TestRedactedExposesSecretsAsConfigured(t *testing.T) {
	view := fullConfig().Redacted()
	byKey := map[string]RedactedField{}
	for _, g := range view.Groups {
		for _, f := range g.Fields {
			byKey[f.Key] = f
		}
	}
	for _, k := range []string{"GC_TOKEN", "GC_FARO_APP_KEY", "GC_FM_TOKEN", "CONTROL_TOKEN", "GC_SELF_OTLP_PASSWORD", "GC_PYROSCOPE_PASSWORD", "GIT_TOKEN"} {
		f, ok := byKey[k]
		if !ok {
			t.Fatalf("secret %s missing from view", k)
		}
		if !f.Secret || f.Value != "" || !f.Configured {
			t.Fatalf("secret %s wrong: %+v (want Secret:true Value:\"\" Configured:true)", k, f)
		}
	}
}

func TestRedactedShowsSafeValues(t *testing.T) {
	view := fullConfig().Redacted()
	want := map[string]string{"GC_PROM_RW": "https://prom", "GC_PROM_USER": "111", "JSON_HTTP_ADDR": "127.0.0.1:8088", "GC_SELF_GRAFANA_URL": "https://staff.example.net"}
	got := map[string]string{}
	for _, g := range view.Groups {
		for _, f := range g.Fields {
			if !f.Secret {
				got[f.Key] = f.Value
			}
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("safe field %s = %q, want %q", k, got[k], v)
		}
	}
}
