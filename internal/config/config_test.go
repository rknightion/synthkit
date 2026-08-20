// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseEnvFileStripsInlineComments(t *testing.T) {
	p := writeEnv(t, `
# full-line comment
GC_TOKEN=abc123   # inline comment must be stripped
GC_PROM_RW="https://prom.example/api/push#anchor"  # quoted hash kept
DRY_RUN=false
EMPTY=
`)
	kv, err := parseEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if kv["GC_TOKEN"] != "abc123" {
		t.Fatalf("inline comment not stripped: %q", kv["GC_TOKEN"])
	}
	if kv["GC_PROM_RW"] != "https://prom.example/api/push#anchor" {
		t.Fatalf("quoted hash mangled: %q", kv["GC_PROM_RW"])
	}
	if kv["DRY_RUN"] != "false" {
		t.Fatalf("DRY_RUN: %q", kv["DRY_RUN"])
	}
	if v, ok := kv["EMPTY"]; !ok || v != "" {
		t.Fatalf("empty value lost: %q ok=%v", v, ok)
	}
}

func TestLoadDefaultsAndOverrides(t *testing.T) {
	p := writeEnv(t, "GC_TOKEN=filetoken\nSERIES_CAP=5000\n")
	t.Setenv("GC_TOKEN", "envtoken") // process env wins over file
	t.Setenv("TICK_DEFAULT", "10s")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "envtoken" {
		t.Fatalf("env override lost: %q", cfg.Token)
	}
	if cfg.SeriesCap != 5000 {
		t.Fatalf("series cap: %d", cfg.SeriesCap)
	}
	if cfg.MasterTick != 10*time.Second {
		t.Fatalf("tick: %v", cfg.MasterTick)
	}
	if !cfg.DryRun {
		t.Fatalf("DRY_RUN must DEFAULT TO TRUE (live push is opt-in)")
	}
	if cfg.BlueprintsDir != "./blueprints" {
		t.Fatalf("blueprints dir default: %q", cfg.BlueprintsDir)
	}
	if len(cfg.BlueprintNames) != 0 {
		t.Fatalf("blueprint names default: %v", cfg.BlueprintNames)
	}
}

func TestLoadParsesBlueprintNameAllowlist(t *testing.T) {
	p := writeEnv(t, "BLUEPRINT_NAMES= otlp-native, other ,otlp-native\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(cfg.BlueprintNames, ","), "otlp-native,other"; got != want {
		t.Fatalf("BlueprintNames = %q, want %q", got, want)
	}

	empty, err := Load(writeEnv(t, "BLUEPRINT_NAMES= , , \n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.BlueprintNames) != 0 {
		t.Fatalf("empty BlueprintNames = %v, want no selection", empty.BlueprintNames)
	}
}

func TestLoadRejectsInvalidDryRun(t *testing.T) {
	p := writeEnv(t, "DRY_RUN=maybe\n")

	_, err := Load(p)
	if err == nil {
		t.Fatal("DRY_RUN=maybe must be rejected")
	}
	if !strings.Contains(err.Error(), "DRY_RUN") || !strings.Contains(err.Error(), "expected true or false") {
		t.Fatalf("invalid DRY_RUN error must explain the accepted values, got: %v", err)
	}
}

func TestLoadTreatsEmptyDryRunAsSafeDefault(t *testing.T) {
	cfg, err := Load(writeEnv(t, "DRY_RUN=\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DryRun {
		t.Fatal("an empty DRY_RUN must retain the safe dry-run default")
	}
}

func TestLoadAcceptsCaseInsensitiveDryRun(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{value: "TRUE", want: true},
		{value: "fAlSe", want: false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			cfg, err := Load(writeEnv(t, "DRY_RUN="+tc.value+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DryRun != tc.want {
				t.Fatalf("DRY_RUN=%s: want %t, got %t", tc.value, tc.want, cfg.DryRun)
			}
		})
	}
}

// TestExternalBlueprintSourceDefaults pins the three new external-blueprint-source config fields.
func TestExternalBlueprintSourceDefaults(t *testing.T) {
	// Absent vars → all defaults.
	def, err := Load(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	if def.BlueprintDataDir != "./data/blueprints" {
		t.Fatalf("BlueprintDataDir default: want \"./data/blueprints\", got %q", def.BlueprintDataDir)
	}
	if def.GitPollInterval != 0 {
		t.Fatalf("GitPollInterval default: want 0 (poll off), got %d", def.GitPollInterval)
	}
	if def.GitTokenDefault != "" {
		t.Fatalf("GitTokenDefault default: want \"\", got %q", def.GitTokenDefault)
	}

	// Explicit env overrides win.
	t.Setenv("BLUEPRINT_DATA_DIR", "/mnt/data/bps")
	t.Setenv("GIT_POLL_INTERVAL", "60")
	t.Setenv("GIT_TOKEN", "ghp_testtoken")
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BlueprintDataDir != "/mnt/data/bps" {
		t.Fatalf("BlueprintDataDir override: want \"/mnt/data/bps\", got %q", cfg.BlueprintDataDir)
	}
	if cfg.GitPollInterval != 60 {
		t.Fatalf("GitPollInterval override: want 60, got %d", cfg.GitPollInterval)
	}
	if cfg.GitTokenDefault != "ghp_testtoken" {
		t.Fatalf("GitTokenDefault override: want \"ghp_testtoken\", got %q", cfg.GitTokenDefault)
	}
}

// TestSelfObsAndProfilingConfig pins the self-observability + profiling triplets: default-off, own
// credential keys (never GC_TOKEN), and numeric profiling knobs parsed from strings.
func TestSelfObsAndProfilingConfig(t *testing.T) {
	// Defaults: both off, all creds empty.
	def, err := Load(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	if def.SelfObsEnabled {
		t.Fatalf("self-obs (incl. process profiling) must default OFF: selfobs=%v", def.SelfObsEnabled)
	}

	p := writeEnv(t, strings.Join([]string{
		"SELFOBS_ENABLED=true",
		"GC_SELF_OTLP_ENDPOINT=https://otlp.example/otlp",
		"GC_SELF_OTLP_USER=42",
		"GC_SELF_OTLP_PASSWORD=selftok",
		"SELFOBS_TAGS=env=prod",
		"GC_PYROSCOPE_URL=https://profiles.example",
		"GC_PYROSCOPE_USER=99",
		"GC_PYROSCOPE_PASSWORD=proftok",
		"PYROSCOPE_MUTEX_FRACTION=5",
		"PYROSCOPE_BLOCK_RATE=7",
	}, "\n")+"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SelfObsEnabled || cfg.SelfOTLPEndpoint != "https://otlp.example/otlp" ||
		cfg.SelfOTLPUser != "42" || cfg.SelfOTLPPassword != "selftok" || cfg.SelfObsTags != "env=prod" {
		t.Fatalf("self-obs config mis-parsed: %+v", cfg)
	}
	// Process profiling has no master flag of its own — it follows SELFOBS_ENABLED; only the
	// GC_PYROSCOPE_* triplet is profiling-specific.
	if cfg.PyroscopeURL != "https://profiles.example" ||
		cfg.PyroscopeUser != "99" || cfg.PyroscopePassword != "proftok" {
		t.Fatalf("profiling config mis-parsed: %+v", cfg)
	}
	if cfg.PyroscopeMutexFraction != 5 || cfg.PyroscopeBlockRate != 7 {
		t.Fatalf("profiling knobs: mutex=%d block=%d", cfg.PyroscopeMutexFraction, cfg.PyroscopeBlockRate)
	}
	// Self-obs/profiling creds are independent of the synthetic GC_TOKEN: ValidateLive (synthetic
	// path) is unaffected by their presence/absence — it still only guards the synthetic sinks.
	if cfg.Token != "" {
		t.Fatalf("GC_TOKEN must not be populated from self-obs keys: %q", cfg.Token)
	}
}

func TestControlTokenAndLoopbackDefault(t *testing.T) {
	// Absent env vars → HTTPAddr loopback default, ControlToken empty, TickTimeout 20s.
	p := writeEnv(t, "")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8088" {
		t.Fatalf("HTTPAddr default must be loopback, got %q", cfg.HTTPAddr)
	}
	if cfg.ControlToken != "" {
		t.Fatalf("ControlToken must default to empty, got %q", cfg.ControlToken)
	}
	if cfg.TickTimeout != 0 {
		t.Fatalf("TickTimeout must default to 0 (disabled — per-sink HTTP timeouts bound pushes), got %v", cfg.TickTimeout)
	}

	// Explicit env overrides win.
	t.Setenv("JSON_HTTP_ADDR", "0.0.0.0:9999")
	t.Setenv("CONTROL_TOKEN", "mytoken")
	t.Setenv("TICK_TIMEOUT", "30")
	cfg2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.HTTPAddr != "0.0.0.0:9999" {
		t.Fatalf("HTTPAddr override lost: %q", cfg2.HTTPAddr)
	}
	if cfg2.ControlToken != "mytoken" {
		t.Fatalf("ControlToken override lost: %q", cfg2.ControlToken)
	}
	if cfg2.TickTimeout != 30*time.Second {
		t.Fatalf("TickTimeout override lost: %v", cfg2.TickTimeout)
	}
}

func TestControlExposureConfig(t *testing.T) {
	t.Setenv("SYNTHKIT_BIND", "10.0.0.8")
	t.Setenv("CONTROL_EXPOSURE_ACK", "trusted-network")
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostBind != "10.0.0.8" || cfg.ControlExposure != "trusted-network" {
		t.Fatalf("control exposure config = bind %q ack %q", cfg.HostBind, cfg.ControlExposure)
	}
}

// TestFleetConfigRead pins the GC_FM_* triplet read by the config loader. These feed the
// runner's fleet.Config (Fleet Management registration is now wired — see env_alignment_test.go).
func TestFleetConfigRead(t *testing.T) {
	t.Setenv("GC_FM_URL", "https://fleet-management-prod-006.grafana.net")
	t.Setenv("GC_FM_STACK_ID", "123456")
	t.Setenv("GC_FM_TOKEN", "tok")
	c, err := Load("") // loader is func Load(envPath string) (*Config, error)
	if err != nil {
		t.Fatal(err)
	}
	if c.FMURL != "https://fleet-management-prod-006.grafana.net" || c.FMStackID != "123456" || c.FMToken != "tok" {
		t.Fatalf("fm config not read: FMURL=%q FMStackID=%q FMToken=%q", c.FMURL, c.FMStackID, c.FMToken)
	}
}

func TestSynthProfilesEnabled(t *testing.T) {
	// Auth REUSES the shared synthetic GC_TOKEN (c.Token) — only URL + USER are profiles-specific.
	// There is NO global enable flag: credential presence alone wires the lane.
	c := &Config{ProfilesURL: "https://profiles-prod-1.grafana.net",
		ProfilesUser: "1", Token: "tok"}
	if !c.SynthProfilesEnabled() {
		t.Fatal("enabled when URL + user + GC_TOKEN set")
	}
	if (&Config{}).SynthProfilesEnabled() {
		t.Fatal("disabled without URL/user/token")
	}
	// Needs the shared GC_TOKEN — URL+user without a token must be disabled.
	if (&Config{ProfilesURL: "x", ProfilesUser: "1"}).SynthProfilesEnabled() {
		t.Fatal("disabled without GC_TOKEN")
	}
	// Footgun guard: SynthProfilesEnabled must NOT read the self-obs Pyroscope* fields.
	if (&Config{PyroscopeURL: "x", PyroscopeUser: "u", PyroscopePassword: "p"}).SynthProfilesEnabled() {
		t.Fatal("must not read the GC_PYROSCOPE_* self-obs triplet")
	}
}

func TestLoadMissingFileIsFine(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatalf("missing .env must not error: %v", err)
	}
}

func TestLiveModeRequiresCreds(t *testing.T) {
	p := writeEnv(t, "DRY_RUN=false\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateLive(); err == nil {
		t.Fatalf("live mode without GC_TOKEN must fail validation")
	}
}

func validLiveConfig() *Config {
	return &Config{
		PromRWURL:    "https://prom.example/api/prom/push",
		PromUser:     "123456",
		OTLPEndpoint: "https://otlp.example/otlp",
		OTLPUser:     "234567",
		LokiURL:      "https://logs.example/loki/api/v1/push",
		LokiUser:     "345678",
		Token:        "test-token",
	}
}

func TestValidateLiveRejectsMalformedPrometheusEndpoint(t *testing.T) {
	cfg := validLiveConfig()
	cfg.PromRWURL = "://malformed-prom-secret"

	err := cfg.ValidateLive()
	if err == nil {
		t.Fatal("malformed Prometheus endpoint must be rejected")
	}
	if strings.Contains(err.Error(), cfg.PromRWURL) {
		t.Fatalf("validation error exposed configured endpoint: %v", err)
	}
}

func TestValidateLiveRejectsInvalidMandatoryEndpointShape(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		set   func(*Config, string)
	}{
		{name: "prometheus requires HTTPS", key: "GC_PROM_RW", value: "http://prom.example/api/prom/push", set: func(c *Config, v string) { c.PromRWURL = v }},
		{name: "prometheus requires hostname", key: "GC_PROM_RW", value: "https://:443/api/prom/push", set: func(c *Config, v string) { c.PromRWURL = v }},
		{name: "prometheus requires push path", key: "GC_PROM_RW", value: "https://prom.example/loki/api/v1/push", set: func(c *Config, v string) { c.PromRWURL = v }},
		{name: "prometheus rejects encoded path", key: "GC_PROM_RW", value: "https://prom.example/api%2Fprom/push", set: func(c *Config, v string) { c.PromRWURL = v }},
		{name: "loki requires HTTPS", key: "GC_LOKI", value: "http://logs.example/loki/api/v1/push", set: func(c *Config, v string) { c.LokiURL = v }},
		{name: "loki requires push path", key: "GC_LOKI", value: "https://logs.example/api/prom/push", set: func(c *Config, v string) { c.LokiURL = v }},
		{name: "OTLP requires HTTPS", key: "GC_OTLP_ENDPOINT", value: "http://otlp.example/otlp", set: func(c *Config, v string) { c.OTLPEndpoint = v }},
		{name: "OTLP requires base path", key: "GC_OTLP_ENDPOINT", value: "https://otlp.example/v1/traces", set: func(c *Config, v string) { c.OTLPEndpoint = v }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validLiveConfig()
			tc.set(cfg, tc.value)

			err := cfg.ValidateLive()
			if err == nil {
				t.Fatal("invalid endpoint must be rejected")
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error = %v, want redacted field name %s", err, tc.key)
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Fatalf("validation error exposed configured endpoint: %v", err)
			}
		})
	}
}

func TestValidateLiveRejectsInvalidMandatoryIdentifierShape(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		set   func(*Config, string)
	}{
		{name: "Prometheus identifier is decimal", key: "GC_PROM_USER", value: "prom-user-secret", set: func(c *Config, v string) { c.PromUser = v }},
		{name: "Loki identifier is positive", key: "GC_LOKI_USER", value: "0", set: func(c *Config, v string) { c.LokiUser = v }},
		{name: "OTLP identifier is unsigned", key: "GC_OTLP_USER", value: "-234567", set: func(c *Config, v string) { c.OTLPUser = v }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validLiveConfig()
			tc.set(cfg, tc.value)

			err := cfg.ValidateLive()
			if err == nil {
				t.Fatal("invalid identifier must be rejected")
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error = %v, want redacted field name %s", err, tc.key)
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Fatalf("validation error exposed configured identifier: %v", err)
			}
		})
	}
}

// TestSendDefaults pins the decoupled delivery queue config defaults and env overrides.
func TestSendDefaults(t *testing.T) {
	// Absent vars → all defaults.
	def, err := Load(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	if def.SendShards != 8 {
		t.Fatalf("SendShards default: want 8, got %d", def.SendShards)
	}
	if def.SendBatchMax != 5000 {
		t.Fatalf("SendBatchMax default: want 5000, got %d", def.SendBatchMax)
	}
	if def.SendDeadline != 5*time.Second {
		t.Fatalf("SendDeadline default: want 5s, got %v", def.SendDeadline)
	}
	if def.SendCapacity != 500000 {
		t.Fatalf("SendCapacity default: want 500000, got %d", def.SendCapacity)
	}
	if def.SendDrainDeadline != 30*time.Second {
		t.Fatalf("SendDrainDeadline default: want 30s, got %v", def.SendDrainDeadline)
	}

	// Explicit env overrides win.
	p := writeEnv(t, strings.Join([]string{
		"SEND_SHARDS=16",
		"SEND_BATCH_MAX=500",
		"SEND_BATCH_DEADLINE=2s",
		"SEND_QUEUE_CAPACITY=4000",
		"SEND_DRAIN_DEADLINE=10s",
	}, "\n")+"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SendShards != 16 {
		t.Fatalf("SendShards override: want 16, got %d", cfg.SendShards)
	}
	if cfg.SendBatchMax != 500 {
		t.Fatalf("SendBatchMax override: want 500, got %d", cfg.SendBatchMax)
	}
	if cfg.SendDeadline != 2*time.Second {
		t.Fatalf("SendDeadline override: want 2s, got %v", cfg.SendDeadline)
	}
	if cfg.SendCapacity != 4000 {
		t.Fatalf("SendCapacity override: want 4000, got %d", cfg.SendCapacity)
	}
	if cfg.SendDrainDeadline != 10*time.Second {
		t.Fatalf("SendDrainDeadline override: want 10s, got %v", cfg.SendDrainDeadline)
	}

	// Present-but-EMPTY values (an operator blanking optional vars in .env) must fall back to
	// defaults, not error — ParseDuration("") would otherwise fail the whole load.
	pe := writeEnv(t, "SEND_SHARDS=\nSEND_BATCH_MAX=\nSEND_BATCH_DEADLINE=\nSEND_QUEUE_CAPACITY=\nSEND_DRAIN_DEADLINE=\n")
	empty, err := Load(pe)
	if err != nil {
		t.Fatalf("empty SEND_* values must use defaults, not error: %v", err)
	}
	if empty.SendShards != 8 || empty.SendBatchMax != 5000 || empty.SendDeadline != 5*time.Second ||
		empty.SendCapacity != 500000 || empty.SendDrainDeadline != 30*time.Second {
		t.Fatalf("empty SEND_* did not fall back to defaults: %+v", empty)
	}
}
