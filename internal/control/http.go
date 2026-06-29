// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/fleetstatus"
	"github.com/rknightion/synthkit/internal/pushstatus"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// Handler is the control-plane HTTP handler. It is a concrete type (NewHandler used to
// return http.Handler) so a runtime status source can be attached after construction
// without disturbing existing call sites — *Handler satisfies http.Handler (ServeHTTP)
// and the variadic NewHandler tail stays byte-identical.
type Handler struct {
	mux      http.Handler // corsEcho(mux): the assembled, CORS-wrapped router
	store    *Store
	status   StatusSources
	bpSchema []byte // pre-marshalled blueprint authoring schema (GET /control/blueprint-schema)
	cfg      *ConfigView
	inv      InventorySource
	health   func() any
	diag     *Diagnostics
	onChange func(State) // optional self-obs audit hook; called after every successful mutation
	bpadmin  BlueprintAdmin
}

// ServeHTTP dispatches to the assembled router.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

// SetStatus attaches the runtime status source feeding GET /control/status. Chained so
// main.go can write NewHandler(...).SetStatus(...). Returns h.
func (h *Handler) SetStatus(s StatusSources) *Handler { h.status = s; return h }

// SetBlueprintSchema attaches the pre-marshalled blueprint authoring schema served at
// GET /control/blueprint-schema (the operator UI's "Blueprint schema" reference tab). The
// caller marshals internal/blueprintschema.Build(...) — control stays decoupled from it.
// Chained; returns h. Absent ⇒ the route returns 404.
func (h *Handler) SetBlueprintSchema(b []byte) *Handler { h.bpSchema = b; return h }

// SetConfig attaches the redacted runtime config view served at GET /control/config.
// Built at the composition root from config.Redacted() and mapped field-for-field.
// Chained; returns h. Absent ⇒ the route returns 404.
func (h *Handler) SetConfig(c ConfigView) *Handler { h.cfg = &c; return h }

// SetInventory attaches the live emission inventory source served at GET /control/inventory.
// Implemented by the runner (control.InventorySource). Chained; returns h. Absent ⇒ 404.
func (h *Handler) SetInventory(s InventorySource) *Handler { h.inv = s; return h }

// SetHealth attaches the health payload callback served at GET /control/health. The
// closure assembles healthstatus.Report + live process metrics at the composition root
// so control stays decoupled from healthstatus. Chained; returns h. Absent ⇒ 404.
func (h *Handler) SetHealth(fn func() any) *Handler { h.health = fn; return h }

// SetDiagnostics attaches the load-time diagnostics collector served at GET /control/diagnostics
// (blueprints skipped on load, dropped incident/config entries). Populated at the composition root.
// Chained; returns h. Absent ⇒ the route returns 404.
func (h *Handler) SetDiagnostics(d *Diagnostics) *Handler { h.diag = d; return h }

// SetBlueprintAdmin attaches the external/custom blueprint management capability
// (implemented by *bpsource.Manager). Chained; returns h. Absent ⇒ blueprint-admin
// routes return 404.
func (h *Handler) SetBlueprintAdmin(a BlueprintAdmin) *Handler { h.bpadmin = a; return h }

// SetChangeObserver attaches an audit hook called with the new snapshot after EVERY successful
// control-plane mutation (the same chokepoint as onApply). main.go wires it to selfobs.EmitEvent
// so config changes (volume / failure injection / blueprint+construct enablement) ride the self-obs
// log stream as event="config_change". control stays decoupled from selfobs/OTel — the callback
// takes only control's own State. Chained; returns h. Absent ⇒ no config-change events.
func (h *Handler) SetChangeObserver(fn func(State)) *Handler { h.onChange = fn; return h }

// StatusReport is the JSON body of GET /control/status: sink readiness + FM health + persist
// health. Fleet is a pointer omitted when no FM source is wired (its absence hides the panel row).
type StatusReport struct {
	Sinks       []pushstatus.SinkStat                   `json:"sinks"`
	ByBlueprint map[string]pushstatus.BlueprintEmission `json:"by_blueprint,omitempty"`
	Fleet       *fleetstatus.FleetStat                  `json:"fleet,omitempty"`
	Persist     PersistHealth                           `json:"persist"`
	DryRun      bool                                    `json:"dry_run"`
}

// StatusSources supplies the runtime status the /control/status route renders. Sinks and Fleet are
// read at request time (typically pushstatus.Store.Snapshot / fleetstatus.Store.Snapshot); DryRun
// reflects the run mode. ByBlueprint (pushstatus.Store.SnapshotByBlueprint) rolls per-blueprint
// emission for the Overview grid. A nil func leaves that section absent from the report.
type StatusSources struct {
	Sinks       func() []pushstatus.SinkStat
	ByBlueprint func() map[string]pushstatus.BlueprintEmission
	Fleet       func() fleetstatus.FleetStat
	DryRun      bool
}

// NewHandler serves the control plane. onApply (optional) is called with the new
// snapshot after every successful mutation — the runner adapter wires knobs from it.
// token (optional) is the HTTP Basic password (username controlUser) gating the POST
// mutation routes; empty = auth disabled (GET routes are always open — Infinity datasource
// + operator UI require unauthenticated GET).
// src (optional, variadic so the existing 3-arg call sites keep compiling) supplies the
// blueprint-derived schema: when present and non-nil, GET /control/schema marshals
// src[0].ControlSchema() and the scenario/scaling POSTs validate against it; absent ⇒ the
// static Descriptors() catalogue and scenario/scaling POSTs are unavailable.
//
// Routes: GET /control/schema · GET /control/state · GET /control/ui ·
// GET /control/blueprint · POST /control/load · POST /control/failures ·
// POST /control/blueprints · POST /control/scenarios · POST /control/scaling ·
// POST /control/constructs · POST /control/kinds · POST /control/reset.
// GETs are strictly side-effect-free (I26).
func NewHandler(store *Store, onApply func(State), token string, src ...SchemaSource) *Handler {
	h := &Handler{store: store}
	var schemaSrc SchemaSource
	if len(src) > 0 {
		schemaSrc = src[0]
	}
	var srcProvider BlueprintSourcer
	if len(src) > 0 {
		if sp, ok := src[0].(BlueprintSourcer); ok {
			srcProvider = sp
		}
	}
	var incidentSrc IncidentSource
	if len(src) > 0 {
		if is, ok := src[0].(IncidentSource); ok {
			incidentSrc = is
		}
	}
	apply := func(s State) {
		if onApply != nil {
			onApply(s)
		}
		if h.onChange != nil {
			h.onChange(s)
		}
	}
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return requireToken(token, h)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /control/schema", func(w http.ResponseWriter, r *http.Request) {
		if schemaSrc == nil {
			writeJSON(w, Descriptors())
			return
		}
		full := schemaSrc.ControlSchema()
		if ParseAudience(r.URL.Query().Get("audience")) == AudienceCustomer {
			writeJSON(w, CustomerSchema(full))
			return
		}
		writeJSON(w, full)
	})
	mux.HandleFunc("GET /control/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, store.Snapshot())
	})
	// GET /control/blueprint-schema — the COMPLETE blueprint authoring schema (every key a
	// blueprint may contain), derived from the live Go types. Read off h.bpSchema at request
	// time so it can be attached after construction via SetBlueprintSchema. Side-effect-free.
	mux.HandleFunc("GET /control/blueprint-schema", func(w http.ResponseWriter, r *http.Request) {
		if len(h.bpSchema) == 0 {
			http.Error(w, "blueprint schema not configured", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(h.bpSchema)
	})
	mux.Handle("GET /control/ui", http.RedirectHandler("/control/ui/", http.StatusFound))
	mux.Handle("GET /control/ui/", spaHandler())
	// GET /control/status — sink readiness + persist health (unguarded, side-effect-free,
	// I26). Status is read off h.status at request time so the source can be attached after
	// construction via SetStatus.
	mux.HandleFunc("GET /control/status", func(w http.ResponseWriter, r *http.Request) {
		var sinks []pushstatus.SinkStat
		if h.status.Sinks != nil {
			sinks = h.status.Sinks()
		}
		var fleet *fleetstatus.FleetStat
		if h.status.Fleet != nil {
			f := h.status.Fleet()
			fleet = &f
		}
		var byBp map[string]pushstatus.BlueprintEmission
		if h.status.ByBlueprint != nil {
			byBp = h.status.ByBlueprint()
		}
		writeJSON(w, StatusReport{Sinks: sinks, ByBlueprint: byBp, Fleet: fleet, Persist: h.store.PersistHealth(), DryRun: h.status.DryRun})
	})

	// GET /control/config — redacted runtime config (unguarded, side-effect-free, I26).
	// cfg is read at request time so it can be attached after construction via SetConfig.
	mux.HandleFunc("GET /control/config", func(w http.ResponseWriter, r *http.Request) {
		if h.cfg == nil {
			http.Error(w, "config view not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.cfg)
	})
	// GET /control/inventory — live emission + cardinality inventory (unguarded, side-effect-free, I26).
	mux.HandleFunc("GET /control/inventory", func(w http.ResponseWriter, r *http.Request) {
		if h.inv == nil {
			http.Error(w, "inventory not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.inv.Inventory())
	})
	// GET /control/health — per-construct tick health + process metrics (unguarded, side-effect-free, I26).
	mux.HandleFunc("GET /control/health", func(w http.ResponseWriter, r *http.Request) {
		if h.health == nil {
			http.Error(w, "health not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.health())
	})

	// GET /control/diagnostics — load-time problems (skipped blueprints / dropped config entries),
	// errors first. Unguarded + side-effect-free, like /control/status and /control/health.
	mux.HandleFunc("GET /control/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		if h.diag == nil {
			http.Error(w, "diagnostics not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.diag.Snapshot())
	})

	// POST /control/load — {"volume_multiplier": N}
	mux.HandleFunc("POST /control/load", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			VolumeMultiplier *float64 `json:"volume_multiplier"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		next := store.Snapshot()
		if body.VolumeMultiplier != nil {
			next.VolumeMultiplier = *body.VolumeMultiplier
		}
		if err := validateState(next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := store.Update(func(s *State) { s.VolumeMultiplier = next.VolumeMultiplier })
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/failures — {"<mode>": {enabled, intensity, scope}, ...} (merge).
	// The ad-hoc failure knob is an intentional escape hatch: a mode/target absent from the derived
	// schema is WARNED (logged), never rejected, so operators can exercise modes/scopes ahead of (or
	// outside) the blueprint inventory. Hard bounds (intensity range, cardinality cap) still apply.
	mux.HandleFunc("POST /control/failures", guard(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]FailureSetting
		if !decodeStrict(w, r, &body) {
			return
		}
		next := store.Snapshot()
		for mode, f := range body {
			next.SetFailure(mode, f)
		}
		if err := validateState(next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if schemaSrc != nil {
			schema := schemaSrc.ControlSchema()
			known := map[string]bool{}
			for _, m := range schema.Modes {
				known[m.Name] = true
			}
			targets := map[string]bool{}
			for _, t := range schema.Targets {
				targets[t.Name] = true
			}
			for mode, f := range body {
				if !known[mode] {
					log.Printf("control: WARN failures references unknown mode %q (accepted)", mode)
				}
				if f.Scope != "" && !targets[f.Scope] {
					log.Printf("control: WARN failures mode %q references unknown target/scope %q (accepted)", mode, f.Scope)
				}
			}
		}
		out := store.Update(func(s *State) {
			for mode, f := range body {
				s.SetFailure(mode, f)
			}
		})
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/scenarios — {"active_scenarios": ["blueprint/name", ...]} (replace).
	// Each id must exist in the derived schema (else 400); the runner's Live closures fire the
	// matching scenarios' effects on the next tick. Unavailable when no SchemaSource is wired.
	mux.HandleFunc("POST /control/scenarios", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ActiveScenarios *[]string `json:"active_scenarios"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if body.ActiveScenarios == nil {
			http.Error(w, "missing active_scenarios", http.StatusBadRequest)
			return
		}
		if schemaSrc == nil {
			http.Error(w, "scenarios unavailable: no schema source configured", http.StatusBadRequest)
			return
		}
		if err := validateScenarios(*body.ActiveScenarios, schemaSrc.ControlSchema()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := store.Update(func(s *State) {
			s.ActiveScenarios = append([]string{}, *body.ActiveScenarios...)
		})
		apply(out)
		writeJSON(w, out)
	}))

	// GET /control/incidents — declared + runtime incidents with authoritative active_now.
	// Side-effect-free (I26); unavailable (404) when no IncidentSource is wired.
	mux.HandleFunc("GET /control/incidents", func(w http.ResponseWriter, r *http.Request) {
		if incidentSrc == nil {
			http.Error(w, "incidents unavailable: no incident source configured", http.StatusNotFound)
			return
		}
		writeJSON(w, incidentSrc.ControlIncidents())
	})

	// POST /control/incidents — create one runtime incident (server mints ID; append).
	// Body: {blueprint, mode, target, at, for, intensity}. Validated by the runner
	// (mode/target/blueprint against schema; at/for against shape's grammar).
	mux.HandleFunc("POST /control/incidents", guard(func(w http.ResponseWriter, r *http.Request) {
		if incidentSrc == nil {
			http.Error(w, "incidents unavailable: no incident source configured", http.StatusBadRequest)
			return
		}
		var ri RuntimeIncident
		if !decodeStrict(w, r, &ri) {
			return
		}
		ri.ID = "" // server-minted only; ignore any client-supplied id
		if ri.Intensity <= 0 {
			ri.Intensity = 1.0
		}
		if err := incidentSrc.ValidateRuntimeIncident(ri); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ri.ID = "rt-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		out := store.Update(func(s *State) {
			s.RuntimeIncidents = append(s.RuntimeIncidents, ri)
		})
		apply(out)
		writeJSON(w, out)
	}))

	// DELETE /control/incidents/{id} — remove one runtime incident by id (404 if not found).
	mux.HandleFunc("DELETE /control/incidents/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		found := false
		for _, ri := range store.Snapshot().RuntimeIncidents {
			if ri.ID == id {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "unknown runtime incident "+id, http.StatusNotFound)
			return
		}
		out := store.Update(func(s *State) {
			kept := s.RuntimeIncidents[:0]
			for _, ri := range s.RuntimeIncidents {
				if ri.ID != id {
					kept = append(kept, ri)
				}
			}
			s.RuntimeIncidents = append([]RuntimeIncident{}, kept...)
		})
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/scaling — {"<target>": count, ...} (merge into Scaling).
	// Each target must be live-scalable and within its bounds (else 400). Unavailable when no
	// SchemaSource is wired.
	mux.HandleFunc("POST /control/scaling", guard(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]int
		if !decodeStrict(w, r, &body) {
			return
		}
		if schemaSrc == nil {
			http.Error(w, "scaling unavailable: no schema source configured", http.StatusBadRequest)
			return
		}
		next := store.Snapshot()
		merged := make(map[string]int, len(next.Scaling)+len(body))
		for k, v := range next.Scaling {
			merged[k] = v
		}
		for k, v := range body {
			merged[k] = v
		}
		if err := validateScaling(merged, schemaSrc.ControlSchema()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := store.Update(func(s *State) {
			if s.Scaling == nil {
				s.Scaling = map[string]int{}
			}
			for k, v := range body {
				s.Scaling[k] = v
			}
		})
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/blueprints — {"disabled_blueprints": ["name", ...]} (replace)
	mux.HandleFunc("POST /control/blueprints", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DisabledBlueprints *[]string `json:"disabled_blueprints"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		out := store.Update(func(s *State) {
			if body.DisabledBlueprints != nil {
				s.DisabledBlueprints = *body.DisabledBlueprints
			}
		})
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/spanmetrics — {"span_metrics_blueprints": ["name", ...]} (replace).
	// Opt-IN list (default OFF): a blueprint emits synthkit's own backend spanmetrics/service-graph
	// only when listed; the default defers to Grafana Cloud metrics-generator / beyla.
	mux.HandleFunc("POST /control/spanmetrics", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SpanMetricsBlueprints *[]string `json:"span_metrics_blueprints"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		out := store.Update(func(s *State) {
			if body.SpanMetricsBlueprints != nil {
				s.SpanMetricsBlueprints = *body.SpanMetricsBlueprints
			}
		})
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/constructs — {"disabled_constructs":["bp/kind:name", ...]} (replace).
	mux.HandleFunc("POST /control/constructs", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DisabledConstructs *[]string `json:"disabled_constructs"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if body.DisabledConstructs == nil {
			http.Error(w, "missing disabled_constructs", http.StatusBadRequest)
			return
		}
		if schemaSrc == nil {
			http.Error(w, "constructs unavailable: no schema source configured", http.StatusBadRequest)
			return
		}
		if err := validateConstructs(*body.DisabledConstructs, schemaSrc.ControlSchema()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := store.Update(func(s *State) {
			s.DisabledConstructs = append([]string{}, *body.DisabledConstructs...)
		})
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/kinds — {"disabled_kinds":["cloudflare", ...]} (replace).
	mux.HandleFunc("POST /control/kinds", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DisabledKinds *[]string `json:"disabled_kinds"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if body.DisabledKinds == nil {
			http.Error(w, "missing disabled_kinds", http.StatusBadRequest)
			return
		}
		if schemaSrc == nil {
			http.Error(w, "kinds unavailable: no schema source configured", http.StatusBadRequest)
			return
		}
		if err := validateKinds(*body.DisabledKinds, schemaSrc.ControlSchema()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := store.Update(func(s *State) {
			s.DisabledKinds = append([]string{}, *body.DisabledKinds...)
		})
		apply(out)
		writeJSON(w, out)
	}))

	// GET /control/blueprint?blueprint=NAME — raw YAML (text/plain). Side-effect-free (I26).
	mux.HandleFunc("GET /control/blueprint", func(w http.ResponseWriter, r *http.Request) {
		if srcProvider == nil {
			http.Error(w, "blueprint source unavailable", http.StatusNotFound)
			return
		}
		yaml, ok := srcProvider.BlueprintSource(r.URL.Query().Get("blueprint"))
		if !ok {
			http.Error(w, "unknown blueprint", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(yaml))
	})

	mux.HandleFunc("POST /control/reset", guard(func(w http.ResponseWriter, r *http.Request) {
		out := store.Reset()
		apply(out)
		writeJSON(w, out)
	}))

	// GET /control/blueprints/staged — list blueprints staged for next restart (open, I26).
	mux.HandleFunc("GET /control/blueprints/staged", func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.bpadmin.ListStaged())
	})

	// GET /control/blueprints/sources — list configured git sources (open, I26).
	// SourceView has no raw token field — only TokenEnvVar (the env-var NAME) — safe to serve.
	mux.HandleFunc("GET /control/blueprints/sources", func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.bpadmin.Sources())
	})

	// GET /control/blueprints/pending — staged-vs-manifest diff driving the "restart to apply"
	// banner (open, side-effect-free I26).
	mux.HandleFunc("GET /control/blueprints/pending", func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.bpadmin.Pending())
	})

	// POST /control/blueprints/validate — validate a blueprint YAML in isolation (guarded).
	// Body: {"yaml": "..."}.  Calls Validate on the raw bytes; returns ValidationResult.
	// NOTE: this validates ONE blueprint in isolation — it CANNOT detect cross-blueprint
	// substrate-identity collisions (cluster/db/cache/host names already used by another loaded
	// blueprint). A "valid" result here can still be rejected at restart by blueprint.ValidateSet.
	// The authoritative cross-blueprint check is surfaced in GET /control/diagnostics.
	mux.HandleFunc("POST /control/blueprints/validate", guard(func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		var body struct {
			YAML string `json:"yaml"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		writeJSON(w, h.bpadmin.Validate([]byte(body.YAML)))
	}))

	// POST /control/blueprints/custom — stage a custom blueprint upload (guarded).
	// Body: {"namespace": "...", "name": "...", "yaml": "..."}.
	// Calls StageUpload; error (invalid YAML / bad name) → 400; success → {"status":"staged"}.
	mux.HandleFunc("POST /control/blueprints/custom", guard(func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		var body struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			YAML      string `json:"yaml"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if err := h.bpadmin.StageUpload(body.Namespace, body.Name, []byte(body.YAML)); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "staged"})
	}))

	// DELETE /control/blueprints/custom?name=<ns/name> — remove a staged custom blueprint (guarded).
	// Calls RemoveUpload; error → 400; success → {"status":"removed"}.
	mux.HandleFunc("DELETE /control/blueprints/custom", guard(func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		name := r.URL.Query().Get("name")
		if err := h.bpadmin.RemoveUpload(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "removed"})
	}))

	// POST /control/blueprints/sources — upsert a git blueprint source (guarded).
	// Body is a SourceView (decodeStrict). Token NEVER echoed in response (SourceView has none).
	// Calls UpsertSource; error → 400; success → {"status":"ok"}.
	mux.HandleFunc("POST /control/blueprints/sources", guard(func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		var sv SourceView
		if !decodeStrict(w, r, &sv) {
			return
		}
		if err := h.bpadmin.UpsertSource(sv); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}))

	// DELETE /control/blueprints/sources?id=<id> — remove a git blueprint source (guarded).
	// Calls RemoveSource; error → 400; success → {"status":"removed"}.
	mux.HandleFunc("DELETE /control/blueprints/sources", guard(func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		id := r.URL.Query().Get("id")
		if err := h.bpadmin.RemoveSource(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "removed"})
	}))

	// POST /control/blueprints/sources/fetch?id=<id> — trigger an immediate git fetch (guarded).
	// Calls FetchNow; upstream-git error → 502 (BadGateway); success → {"status":"fetched"}.
	mux.HandleFunc("POST /control/blueprints/sources/fetch", guard(func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		id := r.URL.Query().Get("id")
		if err := h.bpadmin.FetchNow(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]string{"status": "fetched"})
	}))

	h.mux = corsEcho(mux)
	return h
}

// controlUser is the fixed HTTP Basic username for the control plane. The shared secret
// (CONTROL_TOKEN) is the PASSWORD; the username is a constant so the Grafana Infinity
// datasource (basicAuthUser) and the browser's native dialog have one well-known value.
const controlUser = "control"

// requireToken returns a middleware that requires HTTP Basic auth — username controlUser,
// password <token> — when token is non-empty. Uses crypto/subtle.ConstantTimeCompare to
// avoid timing leaks. On failure it emits a Basic WWW-Authenticate challenge so a browser
// pops its native credential dialog (prompt-on-mutation: GETs are open, the first guarded
// POST triggers the prompt). When token is empty, the handler is passed through unchanged
// (auth disabled).
func requireToken(token string, next http.HandlerFunc) http.HandlerFunc {
	if token == "" {
		return next
	}
	// Precompute the full expected header so the compare is constant-time over the whole
	// credential, not a short-circuiting user-then-pass check.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(controlUser+":"+token))
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="synthkit control", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// corsEcho echoes the request's Origin and requested headers (I26 — Grafana's fetch
// adds x-grafana-device-id, which a fixed allow-list would reject).
// Only GET and OPTIONS are advertised — browsers won't attempt cross-origin POST;
// HTTP Basic auth (CONTROL_TOKEN) is the real mutation barrier.
func corsEcho(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if reqHdrs := r.Header.Get("Access-Control-Request-Headers"); reqHdrs != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHdrs)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// decodeStrict decodes JSON rejecting unknown fields (a typo'd knob must 400, never
// silently no-op). Body is capped at maxBodyBytes to prevent oversized payloads.
// Returns false after writing the error response.
func decodeStrict(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		http.Error(w, "bad control payload: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(buf.Bytes())
}
