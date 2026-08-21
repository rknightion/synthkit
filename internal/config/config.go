// SPDX-License-Identifier: AGPL-3.0-only

// Package config loads synthkit's runtime configuration: an optional .env file
// (inline `# comments` stripped — invariant I27) overridden by real process env.
// DRY_RUN defaults to TRUE: pushing live is always an explicit opt-in.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved runtime configuration.
type Config struct {
	// Sinks (one CAP token covers metrics/logs/traces; RUM has its own pair).
	PromRWURL     string // GC_PROM_RW
	PromUser      string // GC_PROM_USER
	OTLPEndpoint  string // GC_OTLP_ENDPOINT
	OTLPUser      string // GC_OTLP_USER
	LokiURL       string // GC_LOKI
	LokiUser      string // GC_LOKI_USER
	Token         string // GC_TOKEN
	FaroCollector string // GC_FARO_COLLECTOR (optional — RUM)
	FaroAppKey    string // GC_FARO_APP_KEY  (optional — RUM)

	// Fleet Management registration (optional). FMURL empty ⇒ fleet_management collectors
	// emit metrics only; no FM API registration. FMStackID is the FM basic-auth username
	// (the stack ID, NOT GC_PROM_USER); FMToken is a CAP with fleet-management:write (NOT GC_TOKEN).
	FMURL     string // GC_FM_URL
	FMStackID string // GC_FM_STACK_ID
	FMToken   string // GC_FM_TOKEN

	// Synthetic Monitoring provisioning (optional control-plane lane). The version-matched
	// sm-provision binary consumes the values; the main process uses presence plus a
	// non-reversible target fingerprint to bind private registration state.
	SMURL   string // GC_SM_URL
	SMToken string // GC_SM_TOKEN

	// Sigil AI-Observability ingest (optional). SigilEndpoint empty ⇒ the aiagent sigil
	// generation lane no-ops (traces/metrics still emit via the OTLP/prom endpoints). Auth is
	// HTTP Basic base64(SigilTenantID:SigilToken) — SigilTenantID is the stack/tenant id (NOT
	// GC_PROM_USER); SigilToken is a CAP with the sigil ingest scope (NOT GC_TOKEN).
	SigilEndpoint string // GC_SIGIL_ENDPOINT (base, e.g. https://sigil-prod-gb-south-1.grafana.net)
	SigilTenantID string // GC_SIGIL_TENANT_ID
	SigilToken    string // GC_SIGIL_TOKEN

	DryRun           bool          // DRY_RUN (default true)
	MasterTick       time.Duration // TICK_DEFAULT (default 5s)
	TickTimeout      time.Duration // TICK_TIMEOUT seconds (0/unset = disabled) — optional per-blueprint per-tick backstop
	SeriesCap        int           // SERIES_CAP global sink backstop (0 = unlimited)
	BlueprintsDir    string        // BLUEPRINTS (default ./blueprints)
	BlueprintNames   []string      // BLUEPRINT_NAMES (empty = none; exact comma-separated names; "*" = all)
	BlueprintDataDir string        // BLUEPRINT_DATA_DIR — persisted staging root for custom/git blueprints (default ./data/blueprints)
	HTTPAddr         string        // JSON_HTTP_ADDR — control plane + Infinity JSON host over HTTP (default 127.0.0.1:8088)
	HostBind         string        // SYNTHKIT_BIND — effective host-side Compose publish address
	SnapshotPath     string        // CONFIG_SNAPSHOT_PATH — control-plane state (default ./control-state.json)
	ControlToken     string        // CONTROL_TOKEN — HTTP Basic password (user: control) for sensitive reads and mutations (empty = auth disabled)
	ControlExposure  string        // CONTROL_EXPOSURE_ACK — trusted-network | tls-proxy for non-loopback exposure

	// External/custom blueprint sources (git + local).
	GitPollInterval int    // GIT_POLL_INTERVAL — seconds between "update available" polls (0 = off)
	GitTokenDefault string // GIT_TOKEN — default HTTPS PAT for private git blueprint repos (fallback when a source's token_env_var is empty)

	// Self-observability (OTLP → a SEPARATE self-obs stack; internal/selfobs). Own credential
	// triplet, NEVER GC_TOKEN; default-off. It is independent of synthetic DRY_RUN because it
	// describes this process, including verification-only and dry-run executions.
	SelfObsEnabled   bool   // SELFOBS_ENABLED
	SelfOTLPEndpoint string // GC_SELF_OTLP_ENDPOINT (base …/otlp; /v1/{signal} appended)
	SelfOTLPUser     string // GC_SELF_OTLP_USER (HTTP Basic user = self-obs stack id)
	SelfOTLPPassword string // GC_SELF_OTLP_PASSWORD (metrics+logs+traces:write; NOT GC_TOKEN)
	SelfObsTags      string // SELFOBS_TAGS (CSV of k=v resource attributes)
	SelfGrafanaURL   string // GC_SELF_GRAFANA_URL (staff Grafana base URL for deep-links; non-secret)

	SelfObsMetricInterval time.Duration // SELFOBS_METRIC_INTERVAL — self-obs metric flush cadence (default 15s; metrics only)

	// Continuous profiling (Pyroscope → the same SEPARATE stack; internal/profiling). Own triplet,
	// NEVER GC_TOKEN. There is NO separate master switch: process profiles are just another self-obs
	// signal, so they share SELFOBS_ENABLED; the lane is a
	// no-op when its GC_PYROSCOPE_* creds are absent.
	PyroscopeURL           string // GC_PYROSCOPE_URL (Profiles ingest ServerAddress)
	PyroscopeUser          string // GC_PYROSCOPE_USER (Profiles instance id)
	PyroscopePassword      string // GC_PYROSCOPE_PASSWORD (profiles:write; NOT GC_TOKEN)
	PyroscopeTags          string // PYROSCOPE_TAGS (CSV of k=v tags)
	PyroscopeMutexFraction int    // PYROSCOPE_MUTEX_FRACTION (runtime.SetMutexProfileFraction; 0=off)
	PyroscopeBlockRate     int    // PYROSCOPE_BLOCK_RATE (runtime.SetBlockProfileRate ns; 0=off)

	// Synthetic profiles sink (Pyroscope → the configured TARGET stack — same stack as the other
	// synthetic data). DISTINCT from the self-obs Pyroscope* triplet above (the generator's own
	// process profiling). Auth REUSES the shared synthetic GC_TOKEN (no separate profiles token);
	// only the Pyroscope endpoint + instance id differ from the metrics/logs/traces destination.
	// There is NO global on/off flag: like every other synthetic sink the lane is wired whenever
	// its credentials are present, and blueprints decide which workloads/constructs emit profiles.
	ProfilesURL  string // GC_PROFILES_URL (the TARGET stack's Pyroscope ingest endpoint)
	ProfilesUser string // GC_PROFILES_USER (Pyroscope instance id — differs from GC_PROM_USER)

	// Decoupled delivery queue — per-sink async send layer (internal/sink/queue).
	SendShards        int           // SEND_SHARDS (default 8) — parallel shard workers per sink
	SendBatchMax      int           // SEND_BATCH_MAX (default 5000) — max series per flush batch
	SendDeadline      time.Duration // SEND_BATCH_DEADLINE (default 5s) — max age before partial batch flushes
	SendCapacity      int           // SEND_QUEUE_CAPACITY (default 500000) — ring-buffer depth (series slots; memory consumed only when filled under backpressure)
	SendDrainDeadline time.Duration // SEND_DRAIN_DEADLINE (default 30s) — graceful-shutdown drain budget
}

// Load reads envPath (missing file is fine), overlays process env, applies defaults.
func Load(envPath string) (*Config, error) {
	kv, err := parseEnvFile(envPath)
	if err != nil {
		return nil, err
	}
	get := func(key, def string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		if v, ok := kv[key]; ok {
			return v
		}
		return def
	}
	// getInt reads an optional integer env var (empty/unset ⇒ 0). Every env read in this file goes
	// through get/getInt with a STRING-LITERAL key, so the env-alignment test (env_alignment_test.go)
	// can extract the full consumed surface by regex and assert .env/.env.example stay aligned.
	getInt := func(key string) (int, error) {
		v := get(key, "")
		if v == "" {
			return 0, nil
		}
		n, perr := strconv.Atoi(v)
		if perr != nil {
			return 0, fmt.Errorf("config: bad %s %q: %w", key, v, perr)
		}
		return n, nil
	}
	cfg := &Config{
		PromRWURL:        get("GC_PROM_RW", ""),
		PromUser:         get("GC_PROM_USER", ""),
		OTLPEndpoint:     get("GC_OTLP_ENDPOINT", ""),
		OTLPUser:         get("GC_OTLP_USER", ""),
		LokiURL:          get("GC_LOKI", ""),
		LokiUser:         get("GC_LOKI_USER", ""),
		Token:            get("GC_TOKEN", ""),
		FaroCollector:    get("GC_FARO_COLLECTOR", ""),
		FaroAppKey:       get("GC_FARO_APP_KEY", ""),
		FMURL:            get("GC_FM_URL", ""),
		FMStackID:        get("GC_FM_STACK_ID", ""),
		FMToken:          get("GC_FM_TOKEN", ""),
		SMURL:            get("GC_SM_URL", ""),
		SMToken:          get("GC_SM_TOKEN", ""),
		SigilEndpoint:    get("GC_SIGIL_ENDPOINT", ""),
		SigilTenantID:    get("GC_SIGIL_TENANT_ID", ""),
		SigilToken:       get("GC_SIGIL_TOKEN", ""),
		BlueprintsDir:    get("BLUEPRINTS", "./blueprints"),
		BlueprintNames:   parseBlueprintNames(get("BLUEPRINT_NAMES", "")),
		BlueprintDataDir: get("BLUEPRINT_DATA_DIR", "./data/blueprints"),
		HTTPAddr:         get("JSON_HTTP_ADDR", "127.0.0.1:8088"),
		HostBind:         get("SYNTHKIT_BIND", ""),
		SnapshotPath:     get("CONFIG_SNAPSHOT_PATH", "./control-state.json"),
		ControlToken:     get("CONTROL_TOKEN", ""),
		ControlExposure:  get("CONTROL_EXPOSURE_ACK", ""),
		GitTokenDefault:  get("GIT_TOKEN", ""),

		SelfObsEnabled:   strings.EqualFold(get("SELFOBS_ENABLED", "false"), "true"),
		SelfOTLPEndpoint: get("GC_SELF_OTLP_ENDPOINT", ""),
		SelfOTLPUser:     get("GC_SELF_OTLP_USER", ""),
		SelfOTLPPassword: get("GC_SELF_OTLP_PASSWORD", ""),
		SelfObsTags:      get("SELFOBS_TAGS", ""),
		SelfGrafanaURL:   get("GC_SELF_GRAFANA_URL", ""),

		PyroscopeURL:      get("GC_PYROSCOPE_URL", ""),
		PyroscopeUser:     get("GC_PYROSCOPE_USER", ""),
		PyroscopePassword: get("GC_PYROSCOPE_PASSWORD", ""),
		PyroscopeTags:     get("PYROSCOPE_TAGS", ""),

		ProfilesURL:  get("GC_PROFILES_URL", ""),
		ProfilesUser: get("GC_PROFILES_USER", ""),
	}
	if cfg.PyroscopeMutexFraction, err = getInt("PYROSCOPE_MUTEX_FRACTION"); err != nil {
		return nil, err
	}
	if cfg.PyroscopeBlockRate, err = getInt("PYROSCOPE_BLOCK_RATE"); err != nil {
		return nil, err
	}
	if cfg.GitPollInterval, err = getInt("GIT_POLL_INTERVAL"); err != nil {
		return nil, err
	}
	dry := get("DRY_RUN", "true")
	if dry == "" {
		dry = "true"
	}
	switch {
	case strings.EqualFold(dry, "true"):
		cfg.DryRun = true
	case strings.EqualFold(dry, "false"):
		cfg.DryRun = false
	default:
		return nil, fmt.Errorf("config: bad DRY_RUN %q: expected true or false", dry)
	}
	tick := get("TICK_DEFAULT", "5s")
	d, derr := time.ParseDuration(tick)
	if derr != nil {
		return nil, fmt.Errorf("config: bad TICK_DEFAULT %q: %w", tick, derr)
	}
	cfg.MasterTick = d
	soInt := get("SELFOBS_METRIC_INTERVAL", "15s")
	si, sierr := time.ParseDuration(soInt)
	if sierr != nil {
		return nil, fmt.Errorf("config: bad SELFOBS_METRIC_INTERVAL %q: %w", soInt, sierr)
	}
	cfg.SelfObsMetricInterval = si
	if cfg.SeriesCap, err = getInt("SERIES_CAP"); err != nil {
		return nil, err
	}
	tickTimeoutSec, ttErr := getInt("TICK_TIMEOUT")
	if ttErr != nil {
		return nil, ttErr
	}
	// 0/unset = disabled: the per-sink HTTP client timeouts (15s) already bound individual hung
	// pushes, and goroutine-per-blueprint + the dropped-tick metric handle isolation/visibility. A
	// positive value adds an optional coarse whole-blueprint-tick backstop (rarely needed).
	cfg.TickTimeout = time.Duration(tickTimeoutSec) * time.Second

	// Decoupled delivery queue (internal/sink/queue). Defaults: 8 shards, 5000 series/batch,
	// 5s batch deadline, 200000-slot ring buffer (burst absorption for many simultaneous clusters;
	// backpressure is the safety valve beyond it), 30s graceful-drain budget.
	if cfg.SendShards, err = getInt("SEND_SHARDS"); err != nil {
		return nil, err
	}
	if cfg.SendShards == 0 {
		cfg.SendShards = 8
	}
	if cfg.SendBatchMax, err = getInt("SEND_BATCH_MAX"); err != nil {
		return nil, err
	}
	if cfg.SendBatchMax == 0 {
		cfg.SendBatchMax = 5000
	}
	// Treat a present-but-EMPTY value as "use default" (an operator may blank an optional var in
	// .env), matching getInt's empty⇒0⇒default behaviour above — otherwise ParseDuration("") errors.
	sendDeadlineStr := get("SEND_BATCH_DEADLINE", "5s")
	if sendDeadlineStr == "" {
		sendDeadlineStr = "5s"
	}
	sd, sderr := time.ParseDuration(sendDeadlineStr)
	if sderr != nil {
		return nil, fmt.Errorf("config: bad SEND_BATCH_DEADLINE %q: %w", sendDeadlineStr, sderr)
	}
	cfg.SendDeadline = sd
	if cfg.SendCapacity, err = getInt("SEND_QUEUE_CAPACITY"); err != nil {
		return nil, err
	}
	if cfg.SendCapacity == 0 {
		cfg.SendCapacity = 500000
	}
	sendDrainStr := get("SEND_DRAIN_DEADLINE", "30s")
	if sendDrainStr == "" {
		sendDrainStr = "30s"
	}
	sdd, sdderr := time.ParseDuration(sendDrainStr)
	if sdderr != nil {
		return nil, fmt.Errorf("config: bad SEND_DRAIN_DEADLINE %q: %w", sendDrainStr, sdderr)
	}
	cfg.SendDrainDeadline = sdd

	return cfg, nil
}

// parseBlueprintNames turns the optional comma-separated selection into a stable runtime selector.
// Empty entries are ignored: absent/blank selects none, exact names select those identities, and
// the source manager interprets "*" as the explicit all-catalog opt-in.
func parseBlueprintNames(raw string) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// ValidateLive checks the credentials a live (non-dry-run) push needs.
func (c *Config) ValidateLive() error {
	if c.DryRun {
		return nil
	}
	return c.ValidateMandatory()
}

// ValidateMandatory checks the static endpoint and credential shape required by the mandatory
// synthetic-data lanes. Unlike ValidateLive, it runs regardless of DryRun so an explicit preflight
// can validate a future live configuration while ordinary dry-run startup remains credential-free.
func (c *Config) ValidateMandatory() error {
	var missing []string
	for _, field := range []struct {
		key   string
		value string
	}{
		{key: "GC_TOKEN", value: c.Token},
		{key: "GC_PROM_RW", value: c.PromRWURL},
		{key: "GC_PROM_USER", value: c.PromUser},
		{key: "GC_OTLP_ENDPOINT", value: c.OTLPEndpoint},
		{key: "GC_OTLP_USER", value: c.OTLPUser},
		{key: "GC_LOKI", value: c.LokiURL},
		{key: "GC_LOKI_USER", value: c.LokiUser},
	} {
		if field.value == "" {
			missing = append(missing, field.key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: missing mandatory live settings: %s", strings.Join(missing, ", "))
	}
	if err := validateHTTPSURL("GC_PROM_RW", c.PromRWURL, "/api/prom/push"); err != nil {
		return err
	}
	if err := validateHTTPSURL("GC_LOKI", c.LokiURL, "/loki/api/v1/push"); err != nil {
		return err
	}
	if err := validateHTTPSURL("GC_OTLP_ENDPOINT", c.OTLPEndpoint, "/otlp"); err != nil {
		return err
	}
	for _, field := range []struct {
		key   string
		value string
	}{
		{key: "GC_PROM_USER", value: c.PromUser},
		{key: "GC_LOKI_USER", value: c.LokiUser},
		{key: "GC_OTLP_USER", value: c.OTLPUser},
	} {
		if err := validatePositiveDecimal(field.key, field.value); err != nil {
			return err
		}
	}
	return nil
}

func validateHTTPSURL(key, raw, wantPath string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != wantPath ||
		u.RawPath != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("config: %s must be an HTTPS URL with path %s", key, wantPath)
	}
	return nil
}

func validatePositiveDecimal(key, raw string) error {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return fmt.Errorf("config: %s must be a positive decimal identifier", key)
	}
	return nil
}

// RUMEnabled reports whether the optional Faro credential pair is present.
func (c *Config) RUMEnabled() bool { return c.FaroCollector != "" && c.FaroAppKey != "" }

// SigilEnabled reports whether the sigil AI-Observability generation-ingest lane is configured.
// Like every synthetic sink there is no on/off flag — the lane is wired whenever its credential
// triplet is present; blueprints decide which aiagent workloads actually emit.
func (c *Config) SigilEnabled() bool {
	return c.SigilEndpoint != "" && c.SigilTenantID != "" && c.SigilToken != ""
}

// SynthProfilesEnabled reports whether the synthetic Pyroscope-profiles sink is configured. Like
// every other synthetic sink there is NO global on/off flag — the lane is wired whenever its
// credentials are present (URL + USER + the shared GC_TOKEN); blueprints decide which
// workloads/constructs actually emit. NOTE: this is the SYNTHETIC data path (URL/USER + the shared
// GC_TOKEN), DISTINCT from the self-obs GC_PYROSCOPE_* triplet. Auth reuses GC_TOKEN (c.Token) — no
// separate profiles token.
func (c *Config) SynthProfilesEnabled() bool {
	return c.ProfilesURL != "" && c.ProfilesUser != "" && c.Token != ""
}

// parseEnvFile reads KEY=VALUE lines. Full-line and inline `#` comments are stripped
// (inline only outside quotes — "…#…" survives); surrounding quotes are removed.
// A missing file returns an empty map.
func parseEnvFile(path string) (map[string]string, error) {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("config: %w", err)
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case strings.HasPrefix(val, `"`):
			if end := strings.Index(val[1:], `"`); end >= 0 {
				val = val[1 : 1+end]
			}
		case strings.HasPrefix(val, `'`):
			if end := strings.Index(val[1:], `'`); end >= 0 {
				val = val[1 : 1+end]
			}
		default:
			// Unquoted: strip inline comment (I27 — the naive parser kept it once).
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			} else if strings.HasPrefix(val, "#") {
				val = ""
			}
		}
		out[key] = val
	}
	return out, nil
}
