export interface FailureSetting { enabled: boolean; intensity: number; scope: string }
export interface State {
  volume_multiplier: number;
  disabled_blueprints: string[];
  failures: Record<string, FailureSetting>;
  active_scenarios: string[];
  scaling: Record<string, number>;
  disabled_constructs: string[];
  disabled_kinds: string[];
  span_metrics_blueprints: string[];
  runtime_incidents: RuntimeIncident[];
  blueprint_sources: SourceView[];
}

// ── GET /control/incidents (control.RuntimeIncident) ──────────────────────────
// One operator-created scheduled incident (control-plane analogue of a blueprint
// `incidents:` entry), persisted into State.runtime_incidents. The POST /control/incidents
// body is this shape (ID is server-minted on create; the DELETE key). Source:
// internal/control/control.go (RuntimeIncident). At uses shape's schedule grammar.
export interface RuntimeIncident {
  id: string;          // server-minted; stable key for DELETE
  blueprint: string;   // owning blueprint (incidents are blueprint-scoped)
  mode: string;        // failure mode
  target: string;      // "" = blueprint-wide | target name | "<axis>:*"
  at: string;
  for: string;
  intensity: number;
}

// ── GET /control/inventory (control.InventoryReport) ──────────────────────────
// Live emission + cardinality inventory: per-blueprint rollups + per-construct
// detail + grand totals. Source: internal/control/inventory_types.go.
export interface ConstructInventory {
  kind: string;
  name: string;
  distinct_series?: number;
  capped?: boolean;
  metric_names?: string[];
  metric_label_keys?: string[];
  log_sources?: string[];
  log_label_keys?: string[];
  span_services?: string[];
  span_names?: string[];
  span_attr_keys?: string[];
}
export interface BlueprintInventory {
  blueprint: string;
  distinct_series: number;   // summed across constructs (metrics)
  metric_names: number;      // distinct union
  label_keys: number;        // distinct union
  constructs: ConstructInventory[];
}
export interface InventoryTotals {
  distinct_series: number;
  constructs: number;
  blueprints: number;
}
export interface InventoryReport {
  blueprints: BlueprintInventory[];
  totals: InventoryTotals;
}

// ── GET /control/status (control.StatusReport) ────────────────────────────────
// Sink readiness + per-blueprint emission rollup + persist health + dry-run flag.
// Source: internal/control/http.go (StatusReport), internal/pushstatus (SinkStat,
// BlueprintEmission), internal/control/control.go (PersistHealth).
export interface SinkStat {
  sink: string;
  last_success_ms: number;
  last_error_ms: number;
  last_error: string;
  pushes: number;
  failures: number;
  last_items: number;
  last_status: number;
  total_items: number;
  rate_per_min: number;
  spark: number[];
  dry_run: boolean;
}
export interface BlueprintEmission {
  blueprint: string;
  total_items: number;
  rate_per_min: number;       // rolling items/min (0 when <2 samples)
  spark: number[];            // recent per-push item counts, oldest→newest
}
export interface PersistHealth {
  last_ok_ms: number;
  last_error_ms: number;
  last_error: string;
}
// FM lifecycle health aggregate — mirrors internal/fleetstatus.FleetStat (json tags verbatim).
export interface FleetStat {
  registered: number;    // collectors currently registered
  heartbeats: number;    // total heartbeat attempts
  failures: number;      // failed ops (register/heartbeat/unregister)
  last_ok_ms: number;    // last successful op (epoch ms)
  last_error_ms: number; // last failed op (epoch ms)
  last_error: string;
  dry_run: boolean;
}
export interface StatusReport {
  sinks: SinkStat[];
  by_blueprint?: Record<string, BlueprintEmission>;
  fleet?: FleetStat;          // fleetstatus.FleetStat — FM lifecycle health
  persist: PersistHealth;
  dry_run: boolean;
}

// ── GET /control/diagnostics (control.Diagnostic) ─────────────────────────────
// Load-time problems (blueprints skipped on load, dropped config/incident entries),
// errors first. Source: internal/control/diagnostics.go.
export interface Diagnostic {
  level: string;     // "error" (blueprint skipped) | "warning" (entry degraded)
  source: string;    // blueprint name or source file path
  category: string;  // "load" | "resolve" | "incident"
  message: string;
  time: string;      // RFC3339 (UTC)
}

// ── GET /control/schema (control.Schema) ──────────────────────────────────────
// The rich, blueprint-derived knob catalogue (operator-audience). Source:
// internal/control/control.go (Schema + Descriptor/ModeInfo/TargetInfo/ScenarioInfo/
// ConstructInfo/BlueprintMetaInfo). The default (no schema source) path serves the bare
// Descriptor[] catalogue instead, and the ?audience=customer variant trims fields — the
// views request the operator schema, so this types the full shape.
export interface Descriptor {
  key: string;
  type: string;
  help: string;
  default: unknown;
  min: number;
  max: number;
}
export interface ModeInfo {
  name: string;
  axis: string;
  help: string;
}
export interface ScalableInfo {
  dimension: string;
  min: number;
  max: number;
  default: number;
  current: number;
}
export interface TargetInfo {
  blueprint: string;
  name: string;
  axis: string;
  scalable: ScalableInfo | null;   // non-null ⇒ live-scalable
}
export interface EffectInfo {
  mode: string;
  target: string;
  intensity: number;
}
export interface ScenarioInfo {
  blueprint: string;
  name: string;
  title: string;
  summary: string;
  effects: EffectInfo[];
  active: boolean;
}
export interface ConstructInfo {
  blueprint: string;
  kind: string;
  name: string;
  enabled: boolean;
}
// MetaFields is flattened (embedded) onto BlueprintMetaInfo/EnvMetaInfo — all omitempty.
export interface MetaFields {
  description?: string;
  tags?: string[];
  owner?: string;
  links?: Record<string, string>;
  category?: string;
}
export interface EnvMetaInfo extends MetaFields {
  name: string;
}
export interface BlueprintMetaInfo extends MetaFields {
  name: string;
  environments?: EnvMetaInfo[];
}
export interface Schema {
  volume_multiplier: Descriptor;
  blueprints: string[];
  blueprint_meta?: BlueprintMetaInfo[];   // optional, parallel to blueprints
  modes: ModeInfo[];
  targets: TargetInfo[];
  scenarios: ScenarioInfo[];
  constructs: ConstructInfo[];
  kinds: string[];
}

// ── GET /control/config (control.ConfigView) ──────────────────────────────────
// Redacted runtime config: secret field values are NEVER included — only a `configured`
// bool. 404 when no config view is wired. Source: internal/control/configview.go.
export interface ConfigField {
  key: string;          // env var name, e.g. "GC_PROM_RW"
  value: string;        // literal for non-secrets; "" for secrets
  secret: boolean;      // true ⇒ value redacted
  configured: boolean;  // for secrets: whether a value is set
}
export interface ConfigGroup {
  title: string;
  fields: ConfigField[];
}
export interface ConfigView {
  groups: ConfigGroup[];
}

// ── GET /control/health (healthPayload from cmd/synthkit/main.go) ─────────────
// Process metrics from Go runtime (processMetrics struct in main.go).
export interface ProcessStats {
  goroutines: number;   // runtime.NumGoroutine()
  heap_bytes: number;   // ms.HeapAlloc
  gc_count: number;     // ms.NumGC
}
// Per-construct tick health (healthstatus.ConstructHealth).
export interface ConstructHealth {
  blueprint: string;
  kind: string;
  name: string;
  ticks: number;
  errors: number;
  last_outcome: string;   // "ok" | "error"
  last_error: string;
  last_tick_ms: number;
  last_dur_ms: number;
  p95_dur_ms: number;
  spark: number[];        // recent dur in ms, oldest→newest
}
// Per-blueprint cycle health (healthstatus.BlueprintHealth).
export interface BlueprintHealth {
  blueprint: string;
  cycles: number;
  dropped_ticks: number;
  last_cycle_ms: number;
  cycle_spark: number[];  // recent cycle durations in ms, oldest→newest
}
// Full /control/health payload (healthPayload in main.go).
export interface HealthReport {
  constructs: ConstructHealth[];
  blueprints: BlueprintHealth[];
  process: ProcessStats;
}

// ── GET /control/incidents (control.IncidentInfo[]) ───────────────────────────
// Declared (blueprint `incidents:`) + runtime (operator-created) incidents merged, with
// authoritative active_now computed in Go. 404 when no incident source is wired. NOTE: the
// route returns a bare array of these, not a wrapper object. Source: internal/control/control.go.
export interface IncidentInfo {
  source: string;         // "declared" | "runtime"
  id: string;             // runtime only ("" for declared)
  blueprint: string;
  mode: string;
  target: string;
  at: string;             // "" for declared (parse schedule_spec UI-side)
  for: string;            // "" for declared
  schedule_spec: string;
  intensity: number;
  active_now: boolean;
}

// ── GET /control/blueprint-schema (blueprintschema.Doc) ───────────────────────
// The live-derived blueprint authoring schema (sections → fields). Fetched lazily
// on mount by the Schema view — NOT part of the polled store snapshot. 404s when
// the schema generator is not wired. Source: internal/blueprintschema/schema.go.
export interface BpSchemaField {
  key: string;
  type: string;
  doc?: string;
  optional?: boolean;
  repeated?: boolean;
  enum?: string[];
  fields?: BpSchemaField[];
}
export interface BpSchemaSection {
  title: string;
  path?: string;
  doc?: string;
  kind?: string;
  group?: string;
  fields: BpSchemaField[];
}
export interface BpSchemaDoc {
  sections: BpSchemaSection[];
}

// ── GET /control/blueprints/{pending,staged,sources} (control blueprint DTOs) ──
// External/custom blueprint management. Each route 404s when blueprint admin is not wired.
// These DTOs have NO raw-token field by design (SourceView carries only the env-var NAME).
// Source: internal/control/blueprints.go.
export interface PendingChanges {
  added: string[];     // namespaced names staged but not in boot manifest
  removed: string[];   // in boot manifest but no longer staged
  changed: string[];   // git sources whose latest SHA != applied SHA
  restart: boolean;    // any of the above non-empty
}
export interface StagedBlueprint {
  name: string;
  provenance: string;
  source_id: string;
}
export interface SourceView {
  id: string;             // stable slug, also the on-disk dir name
  name: string;           // human label
  namespace: string;      // prefix applied to its blueprints
  url: string;            // https://… (no SSH)
  ref: string;            // e.g. "refs/heads/main"
  subpath: string;        // dir within repo holding *.yaml ("" = root)
  token_env_var: string;  // NAME of env var holding the token ("" = public)
  last_sha: string;       // last fetched commit SHA (status only)
  last_fetch_ms: number;  // unix ms of last successful fetch (status only)
  last_err: string;       // last fetch error ("" = ok)
}
// ValidationResult — POST /control/blueprints/validate outcome (not polled; typed for the
// view's mutation path). Source: internal/control/blueprints.go.
export interface ValidationResult {
  ok: boolean;
  name: string;          // bare name from YAML (pre-namespace)
  cardinality: number;   // projected distinct series (-1 if estimate unavailable)
  estimated: boolean;    // true = multiplier fallback, not exact projection
  diagnostics: string[]; // load errors / warnings
}
