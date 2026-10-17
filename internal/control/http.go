// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rknightion/synthkit/internal/ha"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rknightion/synthkit/internal/config"
	"time"

	"github.com/rknightion/synthkit/internal/fleetstatus"
	"github.com/rknightion/synthkit/internal/optionallane"
	"github.com/rknightion/synthkit/internal/pushstatus"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// Handler is the control-plane HTTP handler. It is a concrete type (NewHandler used to
// return http.Handler) so a runtime status source can be attached after construction
// without disturbing existing call sites — *Handler satisfies http.Handler (ServeHTTP)
// and the variadic NewHandler tail stays byte-identical.
type Handler struct {
	basePath string
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
	gate     ha.LeaderGate
	bounded  ha.Bounded
	haMode   bool
	serial   chan struct{}

	managedFeatures map[string]string
	readyBlueprints BlueprintLister // opt-in: per-blueprint emitting flags on the public readiness probe
}

// ServeHTTP dispatches to the assembled router.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Support either a prefix-preserving proxy or one that strips the prefix.
	// Clone rather than mutate the request seen by outer middleware.
	if h.basePath != "" && strings.HasPrefix(r.URL.Path, h.basePath+"/control/") {
		r = r.Clone(r.Context())
		r.URL.Path = strings.TrimPrefix(r.URL.Path, h.basePath)
		r.URL.RawPath = ""
	}
	h.mux.ServeHTTP(w, r)
}

// SetManagedFeatures installs presentation-only feature reasons before serving requests.
// It does not restrict API endpoints. Copy input so callers cannot mutate runtime metadata.
func (h *Handler) SetManagedFeatures(features map[string]string) *Handler {
	h.managedFeatures = make(map[string]string, len(features))
	for key, reason := range features {
		h.managedFeatures[key] = reason
	}
	return h
}

// SetBasePath installs the trusted external prefix before serving requests.
// The composition root must mount this handler at prefix+"/control/" when
// the proxy preserves the prefix. No forwarded headers influence these URLs.
func (h *Handler) SetBasePath(prefix string) *Handler {
	if err := config.ValidateControlBasePath(prefix); err != nil {
		panic(err) // config.Load already validates this operator-provided value
	}
	h.basePath = prefix
	return h
}

// SetHA installs the same gate as file persistence, before serving any request.
func (h *Handler) SetHA(gate ha.LeaderGate, bounded ha.Bounded) *Handler {
	if gate == nil {
		panic("control: nil HA gate")
	}
	h.gate = gate
	h.bounded = bounded
	h.bounded.Gate = gate
	h.haMode = true
	return h
}

// mutationProgress retains the single HTTP outcome and exposes the real socket
// to ResponseController through middleware. A timed-out operation cannot emit a
// fresh successful body or append a second not-leader response.
type mutationProgress struct {
	http.ResponseWriter
	ctx     context.Context
	written bool
}

func (w *mutationProgress) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *mutationProgress) WriteHeader(code int) {
	if w.written {
		return
	}
	w.written = true
	w.ResponseWriter.WriteHeader(code)
}
func (w *mutationProgress) Write(body []byte) (int, error) {
	if !w.written && w.ctx != nil && w.ctx.Err() != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, err := w.ResponseWriter.Write([]byte(`{"code":"state_operation_timeout"}`))
		return len(body), err
	}
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func mutationError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrDeclaredSource) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, map[string]string{"code": "declared_source", "reason": ErrDeclaredSource.Error()})
		return true
	}
	code := "state_persist_failed"
	if errors.Is(err, ErrOutcomeUnknown) {
		code = "state_outcome_unknown"
	}
	if errors.Is(err, ErrConflict) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]string{"code": "state_conflict"})
		return true
	}
	if errors.Is(err, ha.ErrNotLeader) {
		code = "not_leader"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	writeJSON(w, map[string]string{"code": code})
	return true
}

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

// SetReadinessBlueprintFlags is the opt-in behind READINESS_BLUEPRINT_FLAGS. A non-nil lister adds a
// per-blueprint emitting boolean (blueprint_emitting) to the public GET /control/readiness probe;
// nil, the default, leaves the probe byte-identical. Chained; returns h.
func (h *Handler) SetReadinessBlueprintFlags(l BlueprintLister) *Handler {
	h.readyBlueprints = l
	return h
}

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
	Queues      []pushstatus.QueueStat                  `json:"queues"`
	ByBlueprint map[string]pushstatus.BlueprintEmission `json:"by_blueprint,omitempty"`
	Fleet       *fleetstatus.FleetStat                  `json:"fleet,omitempty"`
	Persist     PersistHealth                           `json:"persist"`
	DryRun      bool                                    `json:"dry_run"`
	Readiness   *ReadinessReport                        `json:"readiness,omitempty"`
	Optional    []optionallane.Disposition              `json:"optional_lanes,omitempty"`
}

// StatusSources supplies the runtime status the /control/status route renders. Sinks and Fleet are
// read at request time (typically pushstatus.Store.Snapshot / fleetstatus.Store.Snapshot); DryRun
// reflects the run mode. ByBlueprint (pushstatus.Store.SnapshotByBlueprint) rolls per-blueprint
// emission for the Overview grid. A nil func leaves that section absent from the report.
type StatusSources struct {
	Sinks       func() []pushstatus.SinkStat
	Queues      func() []pushstatus.QueueStat
	ByBlueprint func() map[string]pushstatus.BlueprintEmission
	Fleet       func() fleetstatus.FleetStat
	Readiness   func() ReadinessReport
	Optional    func() []optionallane.Disposition
	DryRun      bool
}

// NewHandler serves the control plane. onApply (optional) is called with the new
// snapshot after every successful mutation — the runner adapter wires knobs from it.
// token (optional) is the HTTP Basic password (username controlUser) gating every mutation and
// topology-bearing read; empty = auth disabled. Only the sanitized readiness probe stays public.
// src (optional, variadic so the existing 3-arg call sites keep compiling) supplies the
// blueprint-derived schema: when present and non-nil, GET /control/schema marshals
// src[0].ControlSchema() and the scenario/scaling POSTs validate against it; absent ⇒ the
// static Descriptors() catalogue and scenario/scaling POSTs are unavailable.
//
// Routes: GET /control/schema · GET /control/state · GET /control/ui ·
// GET /control/blueprint · POST /control/load · POST /control/failures ·
// POST /control/blueprints · POST /control/blueprints/{disable,enable} ·
// POST /control/scenarios · POST /control/scenarios/{activate,deactivate} · POST /control/scaling ·
// POST /control/constructs · POST /control/kinds · POST /control/reset.
// GETs are strictly side-effect-free (I26).
func NewHandler(store *Store, onApply func(State), token string, src ...SchemaSource) *Handler {
	h := &Handler{store: store, gate: ha.AlwaysLeader{}, serial: make(chan struct{}, 1)}
	h.serial <- struct{}{}
	update := func(w http.ResponseWriter, r *http.Request, fn func(*State)) (State, bool) {
		if !h.haMode && store.backend == nil {
			return store.Update(fn), true
		}
		out, err := store.UpdateContext(r.Context(), fn)
		return out, !mutationError(w, err)
	}
	var schemaSrc SchemaSource
	if len(src) > 0 {
		schemaSrc = startupKindsSource{SchemaSource: src[0], store: store}
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
	var liveRuntime LiveBlueprintRuntime
	if len(src) > 0 {
		if lr, ok := src[0].(LiveBlueprintRuntime); ok {
			liveRuntime = lr
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
		state := store.Snapshot()
		if store.backend != nil && h.bpadmin != nil {
			state.BlueprintSources = h.bpadmin.Sources()
		}
		writeJSON(w, state)
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
	mux.HandleFunc("GET /control/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, h.basePath+"/control/ui/", http.StatusFound)
	})
	mux.Handle("GET /control/ui/", spaHandlerWithBase(func() string { return h.basePath }, func() map[string]string { return h.managedFeatures }))
	// GET /control/status — sink readiness + persist health (protected when configured,
	// I26). Status is read off h.status at request time so the source can be attached after
	// construction via SetStatus.
	mux.HandleFunc("GET /control/status", func(w http.ResponseWriter, r *http.Request) {
		var sinks []pushstatus.SinkStat
		if h.status.Sinks != nil {
			sinks = h.status.Sinks()
		}
		var queues []pushstatus.QueueStat
		if h.status.Queues != nil {
			queues = h.status.Queues()
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
		var readiness *ReadinessReport
		if h.status.Readiness != nil {
			report := h.status.Readiness()
			readiness = &report
		}
		var optional []optionallane.Disposition
		if h.status.Optional != nil {
			optional = h.status.Optional()
		}
		writeJSON(w, StatusReport{Sinks: sinks, Queues: queues, ByBlueprint: byBp, Fleet: fleet, Persist: h.store.PersistHealth(), DryRun: h.status.DryRun, Readiness: readiness, Optional: optional})
	})
	// GET /control/readiness is deliberately public so Docker can call it without credentials. Its
	// probe representation omits topology, errors, filesystem paths, and operational reasons.
	mux.HandleFunc("GET /control/readiness", func(w http.ResponseWriter, r *http.Request) {
		if h.status.Readiness == nil {
			http.Error(w, "readiness not configured", http.StatusNotFound)
			return
		}
		report := h.status.Readiness()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if !report.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		if h.readyBlueprints == nil {
			_ = json.NewEncoder(w).Encode(report.Probe())
			return
		}
		var constructs []ConstructInfo
		if schemaSrc != nil {
			constructs = schemaSrc.ControlSchema().Constructs
		}
		flags := BlueprintEmittingFlags(report, h.readyBlueprints.Blueprints(), h.store.Snapshot(),
			constructs, schemaSrc != nil, h.gate.Role() == ha.RoleLeader)
		_ = json.NewEncoder(w).Encode(ReadinessProbeBlueprints{ReadinessProbe: report.Probe(), BlueprintEmitting: flags})
	})

	// GET /control/config — redacted runtime config (protected when configured, side-effect-free, I26).
	// cfg is read at request time so it can be attached after construction via SetConfig.
	mux.HandleFunc("GET /control/config", func(w http.ResponseWriter, r *http.Request) {
		if h.cfg == nil {
			http.Error(w, "config view not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.cfg)
	})
	// GET /control/inventory — live emission + cardinality inventory (protected, side-effect-free, I26).
	mux.HandleFunc("GET /control/inventory", func(w http.ResponseWriter, r *http.Request) {
		if h.inv == nil {
			http.Error(w, "inventory not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.inv.Inventory())
	})
	// GET /control/health — per-construct tick health + process metrics (protected, side-effect-free, I26).
	mux.HandleFunc("GET /control/health", func(w http.ResponseWriter, r *http.Request) {
		if h.health == nil {
			http.Error(w, "health not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.health())
	})

	// GET /control/diagnostics — load-time problems (skipped blueprints / dropped config entries),
	// errors first. Protected when configured and side-effect-free, like status and health.
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
		out, ok := update(w, r, func(s *State) { s.VolumeMultiplier = next.VolumeMultiplier })
		if !ok {
			return
		}
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
		out, ok := update(w, r, func(s *State) {
			for mode, f := range body {
				s.SetFailure(mode, f)
			}
		})
		if !ok {
			return
		}
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
		out, ok := update(w, r, func(s *State) {
			s.ActiveScenarios = append([]string{}, *body.ActiveScenarios...)
		})
		if !ok {
			return
		}
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/scenarios/activate and /deactivate mutate one scenario without
	// replacing the active list. Their server-side read/modify/write avoids stale UI
	// snapshots clearing a scenario changed by another operator.
	scenarioMutation := func(active bool) http.HandlerFunc {
		return guard(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Scenario string `json:"scenario"`
			}
			if !decodeStrict(w, r, &body) {
				return
			}
			if body.Scenario == "" {
				http.Error(w, "missing scenario", http.StatusBadRequest)
				return
			}
			if schemaSrc == nil {
				http.Error(w, "scenarios unavailable: no schema source configured", http.StatusBadRequest)
				return
			}
			if err := validateScenarios([]string{body.Scenario}, schemaSrc.ControlSchema()); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			out, ok := update(w, r, func(s *State) { s.ActiveScenarios = setMember(s.ActiveScenarios, body.Scenario, active) })
			if !ok {
				return
			}
			apply(out)
			writeJSON(w, out)
		})
	}
	mux.HandleFunc("POST /control/scenarios/activate", scenarioMutation(true))
	mux.HandleFunc("POST /control/scenarios/deactivate", scenarioMutation(false))

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
		out, ok := update(w, r, func(s *State) {
			s.RuntimeIncidents = append(s.RuntimeIncidents, ri)
		})
		if !ok {
			return
		}
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
		out, ok := update(w, r, func(s *State) {
			kept := s.RuntimeIncidents[:0]
			for _, ri := range s.RuntimeIncidents {
				if ri.ID != id {
					kept = append(kept, ri)
				}
			}
			s.RuntimeIncidents = append([]RuntimeIncident{}, kept...)
		})
		if !ok {
			return
		}
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
		out, ok := update(w, r, func(s *State) {
			if s.Scaling == nil {
				s.Scaling = map[string]int{}
			}
			for k, v := range body {
				s.Scaling[k] = v
			}
		})
		if !ok {
			return
		}
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
		out, ok := update(w, r, func(s *State) {
			if body.DisabledBlueprints != nil {
				s.DisabledBlueprints = *body.DisabledBlueprints
			}
		})
		if !ok {
			return
		}
		apply(out)
		writeJSON(w, out)
	}))

	// POST /control/blueprints/disable and /enable mutate one blueprint without
	// replacing the disabled list. They are intentionally available without a schema
	// source, matching the legacy full-replacement endpoint's validation contract.
	blueprintMutation := func(disabled bool) http.HandlerFunc {
		return guard(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Blueprint string `json:"blueprint"`
			}
			if !decodeStrict(w, r, &body) {
				return
			}
			if body.Blueprint == "" {
				http.Error(w, "missing blueprint", http.StatusBadRequest)
				return
			}
			out, ok := update(w, r, func(s *State) { s.DisabledBlueprints = setMember(s.DisabledBlueprints, body.Blueprint, disabled) })
			if !ok {
				return
			}
			apply(out)
			writeJSON(w, out)
		})
	}
	mux.HandleFunc("POST /control/blueprints/disable", blueprintMutation(true))
	mux.HandleFunc("POST /control/blueprints/enable", blueprintMutation(false))

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
		out, ok := update(w, r, func(s *State) {
			if body.SpanMetricsBlueprints != nil {
				s.SpanMetricsBlueprints = *body.SpanMetricsBlueprints
			}
		})
		if !ok {
			return
		}
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
		out, ok := update(w, r, func(s *State) {
			s.DisabledConstructs = append([]string{}, *body.DisabledConstructs...)
		})
		if !ok {
			return
		}
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
		out, ok := update(w, r, func(s *State) {
			s.DisabledKinds = append([]string{}, *body.DisabledKinds...)
		})
		if !ok {
			return
		}
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
		out, ok := update(w, r, resetOwnedState)
		if !ok {
			return
		}
		apply(out)
		writeJSON(w, out)
	}))

	// GET /control/blueprints/staged — protected list of blueprints staged for restart (I26).
	mux.HandleFunc("GET /control/blueprints/staged", func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.bpadmin.ListStaged())
	})

	// GET /control/blueprints/sources — protected list of configured git sources (I26).
	// SourceView has no raw token field — only TokenEnvVar (the env-var NAME).
	mux.HandleFunc("GET /control/blueprints/sources", func(w http.ResponseWriter, r *http.Request) {
		if h.bpadmin == nil {
			http.Error(w, "blueprint admin not configured", http.StatusNotFound)
			return
		}
		writeJSON(w, h.bpadmin.Sources())
	})

	// GET /control/blueprints/pending — staged-vs-manifest diff driving the "restart to apply"
	// banner (protected when configured, side-effect-free I26).
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
	// Calls StageUpload; error (invalid YAML / bad name) → 400; success reports the exact
	// namespaced runtime identity where the admin can provide it.
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
		identity := body.Name
		if named, ok := h.bpadmin.(BlueprintIdentityer); ok {
			identity = named.EffectiveBlueprintIdentity(body.Namespace, body.Name)
		}
		writeJSON(w, StageUploadResponse{Status: "staged", Name: identity})
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

	// POST /control/blueprints/live — apply one namespaced blueprint to the running process
	// (guarded). DELETE /control/blueprints/live?name=<ns/name> removes one applied that way.
	mux.HandleFunc("POST /control/blueprints/live", guard(func(w http.ResponseWriter, r *http.Request) {
		h.applyLiveBlueprint(w, r, liveRuntime, update, apply)
	}))
	mux.HandleFunc("DELETE /control/blueprints/live", guard(func(w http.ResponseWriter, r *http.Request) {
		h.removeLiveBlueprint(w, r, liveRuntime, update, apply)
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
		if sv.Declared || DeclaredSourceID(store.Snapshot(), sv.ID) {
			mutationError(w, ErrDeclaredSource)
			return
		}
		upsert := func() error { return h.bpadmin.UpsertSource(sv) }
		if contextual, ok := h.bpadmin.(interface {
			UpsertSourceContext(context.Context, SourceView) error
		}); ok {
			upsert = func() error { return contextual.UpsertSourceContext(r.Context(), sv) }
		}
		if err := upsert(); err != nil {
			if errors.Is(err, ErrDeclaredSource) {
				mutationError(w, err)
				return
			}
			if errors.Is(err, ErrOutcomeUnknown) || errors.Is(err, ErrConflict) || errors.Is(err, ha.ErrNotLeader) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				mutationError(w, err)
				return
			}
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
		if DeclaredSourceID(store.Snapshot(), id) {
			mutationError(w, ErrDeclaredSource)
			return
		}
		remove := func() error { return h.bpadmin.RemoveSource(id) }
		if contextual, ok := h.bpadmin.(interface {
			RemoveSourceContext(context.Context, string) error
		}); ok {
			remove = func() error { return contextual.RemoveSourceContext(r.Context(), id) }
		}
		if err := remove(); err != nil {
			if errors.Is(err, ErrDeclaredSource) {
				mutationError(w, err)
				return
			}
			if errors.Is(err, ErrOutcomeUnknown) || errors.Is(err, ErrConflict) || errors.Is(err, ha.ErrNotLeader) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				mutationError(w, err)
				return
			}
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
		fetch := func() error { return h.bpadmin.FetchNow(id) }
		if contextual, ok := h.bpadmin.(interface {
			FetchNowContext(context.Context, string) error
		}); ok {
			fetch = func() error { return contextual.FetchNowContext(r.Context(), id) }
		}
		if err := fetch(); err != nil {
			if errors.Is(err, ErrOutcomeUnknown) || errors.Is(err, ErrConflict) || errors.Is(err, ha.ErrNotLeader) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				mutationError(w, err)
				return
			}
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]string{"status": "fetched"})
	}))

	admission := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (!h.haMode && store.backend == nil) || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			mux.ServeHTTP(w, r)
			return
		}
		if !h.haMode {
			RequireToken(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-h.serial:
				case <-r.Context().Done():
					mutationError(w, r.Context().Err())
					return
				}
				defer func() { h.serial <- struct{}{} }()
				mux.ServeHTTP(w, r)
			})).ServeHTTP(w, r)
			return
		}
		RequireToken(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			progress := &mutationProgress{ResponseWriter: w}
			err := h.bounded.Run(r.Context(), ha.Mutation, func(ctx context.Context) error {
				progress.ctx = ctx
				// Body reads stay INSIDE admission. Bind the real connection to
				// the same absolute deadline so a slow body cannot strand the
				// function until the server's unrelated 15s ReadTimeout.
				if deadline, ok := ctx.Deadline(); ok {
					controller := http.NewResponseController(progress)
					for _, setDeadline := range []func(time.Time) error{controller.SetReadDeadline, controller.SetWriteDeadline} {
						if err := setDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
							return err
						}
					}
				}
				select {
				case <-h.serial:
				case <-ctx.Done():
					return ctx.Err()
				}
				defer func() { h.serial <- struct{}{} }()
				if r.URL.Path == "/control/blueprints/custom" {
					progress.WriteHeader(http.StatusServiceUnavailable)
					writeJSON(progress, map[string]string{"code": "ha_upload_unavailable"})
					return nil
				}
				mux.ServeHTTP(progress, r.WithContext(ctx))
				return nil
			})
			if err != nil && !progress.written {
				mutationError(progress, err)
			}
		})).ServeHTTP(w, r)
	})
	h.mux = corsEcho(protectControlReads(token, admission))
	return h
}

// controlUser is the fixed HTTP Basic username for the control plane. The shared secret
// (CONTROL_TOKEN) is the PASSWORD; the username is a constant so the Grafana Infinity
// datasource (basicAuthUser) and the browser's native dialog have one well-known value.
const controlUser = "control"

// requireToken returns a middleware that requires HTTP Basic auth — username controlUser,
// password <token> — when token is non-empty. Uses crypto/subtle.ConstantTimeCompare to
// avoid timing leaks. On failure it emits a Basic WWW-Authenticate challenge so a browser
// pops its native credential dialog. When token is empty, the handler is passed through unchanged
// (auth disabled).
func requireToken(token string, next http.HandlerFunc) http.HandlerFunc {
	protected := requireTokenHandler(token, next)
	return protected.ServeHTTP
}

// RequireToken protects an arbitrary handler with the control plane's fixed Basic-auth contract.
// It is exported for the composition root so the sibling Infinity JSON host can share the exact
// same credential semantics without duplicating secret comparison code.
func RequireToken(token string, next http.Handler) http.Handler {
	return requireTokenHandler(token, next)
}

func requireTokenHandler(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	// Precompute the full expected header so the compare is constant-time over the whole
	// credential, not a short-circuiting user-then-pass check.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(controlUser+":"+token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="synthkit control", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func protectControlReads(token string, next http.Handler) http.Handler {
	protected := RequireToken(token, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path != "/control/readiness" {
			protected.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// corsEcho echoes the request's Origin and requested headers (I26 — Grafana's fetch
// adds x-grafana-device-id, which a fixed allow-list would reject).
// GET, POST, and OPTIONS are advertised so browser action buttons can preflight guarded
// mutations; HTTP Basic auth (CONTROL_TOKEN) remains the mutation barrier.
func corsEcho(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if reqHdrs := r.Header.Get("Access-Control-Request-Headers"); reqHdrs != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHdrs)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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

// maxLiveOwnerBytes bounds the opaque owner label carried by a live-applied blueprint.
const maxLiveOwnerBytes = 128

// errLiveResponseWritten marks a persistence failure whose response the update helper already wrote.
var errLiveResponseWritten = errors.New("control: live blueprint response already written")

type liveUpdateFunc func(http.ResponseWriter, *http.Request, func(*State)) (State, bool)

// liveBlueprintRequest validates the request envelope and derives the sanitized namespaced
// identity with the same sanitiser uploads use. The form name may be the YAML's bare name or that
// exact effective identity; the runtime later requires the YAML name to agree.
func liveBlueprintRequest(identityer BlueprintIdentityer, namespace, name, yamlDoc, owner string) (LiveBlueprint, error) {
	if len(owner) > maxLiveOwnerBytes {
		return LiveBlueprint{}, fmt.Errorf("owner exceeds %d bytes", maxLiveOwnerBytes)
	}
	if !utf8.ValidString(owner) {
		return LiveBlueprint{}, errors.New("owner must be valid UTF-8")
	}
	for _, c := range owner {
		if unicode.IsControl(c) {
			return LiveBlueprint{}, errors.New("owner must not contain control characters")
		}
	}
	if strings.TrimSpace(yamlDoc) == "" {
		return LiveBlueprint{}, errors.New("missing yaml")
	}
	bare := name
	if prefix, rest, found := strings.Cut(name, "/"); found {
		if prefix == "" || identityer.EffectiveBlueprintIdentity(namespace, rest) != name {
			return LiveBlueprint{}, fmt.Errorf("blueprint name %q must be the YAML name or its effective identity %q", name, identityer.EffectiveBlueprintIdentity(namespace, rest))
		}
		bare = rest
	}
	if bare == "" || strings.Contains(bare, "/") || strings.Contains(bare, "__") {
		return LiveBlueprint{}, fmt.Errorf("blueprint name %q must be non-empty and must not contain '/' or '__'", bare)
	}
	identity := identityer.EffectiveBlueprintIdentity(namespace, bare)
	prefix, found := strings.CutSuffix(identity, "/"+bare)
	if !found || prefix == "" || strings.Contains(prefix, "/") {
		return LiveBlueprint{}, fmt.Errorf("blueprint namespace %q does not produce a namespaced identity", namespace)
	}
	return LiveBlueprint{Name: identity, Namespace: prefix, YAML: yamlDoc, Owner: owner}, nil
}

func liveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrLiveInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrLiveCollision):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrLiveNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		mutationError(w, err)
	}
}

func (h *Handler) liveCapabilities(w http.ResponseWriter, live LiveBlueprintRuntime) (BlueprintIdentityer, bool) {
	if h.bpadmin == nil {
		http.Error(w, "blueprint admin not configured", http.StatusNotFound)
		return nil, false
	}
	identityer, ok := h.bpadmin.(BlueprintIdentityer)
	if !ok || live == nil {
		http.Error(w, "live blueprints not configured", http.StatusNotFound)
		return nil, false
	}
	return identityer, true
}

// applyLiveBlueprint serves POST /control/blueprints/live.
func (h *Handler) applyLiveBlueprint(w http.ResponseWriter, r *http.Request, live LiveBlueprintRuntime, update liveUpdateFunc, apply func(State)) {
	identityer, ok := h.liveCapabilities(w, live)
	if !ok {
		return
	}
	var body struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		YAML      string `json:"yaml"`
		Owner     string `json:"owner"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	lb, err := liveBlueprintRequest(identityer, body.Namespace, body.Name, body.YAML, body.Owner)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var out State
	err = live.ApplyLiveBlueprint(lb, func() error {
		next, ok := update(w, r, func(s *State) {
			kept := make([]LiveBlueprint, 0, len(s.LiveBlueprints)+1)
			for _, existing := range s.LiveBlueprints {
				if existing.Name != lb.Name {
					kept = append(kept, existing)
				}
			}
			s.LiveBlueprints = append(kept, lb)
		})
		if !ok {
			return errLiveResponseWritten
		}
		out = next
		return nil
	})
	if errors.Is(err, errLiveResponseWritten) {
		return
	}
	if err != nil {
		liveError(w, err)
		return
	}
	apply(out)
	writeJSON(w, LiveApplyResponse{Status: "applied", Blueprint: lb.Name, Owner: lb.Owner})
}

// removeLiveBlueprint serves DELETE /control/blueprints/live?name=<ns/name>.
func (h *Handler) removeLiveBlueprint(w http.ResponseWriter, r *http.Request, live LiveBlueprintRuntime, update liveUpdateFunc, apply func(State)) {
	if _, ok := h.liveCapabilities(w, live); !ok {
		return
	}
	name := r.URL.Query().Get("name")
	recorded := false
	for _, existing := range h.store.Snapshot().LiveBlueprints {
		if name != "" && existing.Name == name {
			recorded = true
		}
	}
	var out State
	err := live.RemoveLiveBlueprint(name, recorded, func(forgetKnobs bool) error {
		next, ok := update(w, r, func(s *State) {
			if forgetKnobs {
				forgetLiveBlueprint(s, name)
				return
			}
			kept := make([]LiveBlueprint, 0, len(s.LiveBlueprints))
			for _, existing := range s.LiveBlueprints {
				if existing.Name != name {
					kept = append(kept, existing)
				}
			}
			s.LiveBlueprints = kept
		})
		if !ok {
			return errLiveResponseWritten
		}
		out = next
		return nil
	})
	if errors.Is(err, errLiveResponseWritten) {
		return
	}
	if err != nil {
		liveError(w, err)
		return
	}
	apply(out)
	writeJSON(w, LiveRemoveResponse{Status: "removed", Blueprint: name})
}

// forgetLiveBlueprint drops a removed live blueprint's record and every per-name knob keyed by it
// (disable, span metrics, active scenarios, construct disables, scaling, runtime incidents), so a
// later re-apply of the same identity starts from defaults.
func forgetLiveBlueprint(s *State, name string) {
	kept := make([]LiveBlueprint, 0, len(s.LiveBlueprints))
	for _, existing := range s.LiveBlueprints {
		if existing.Name != name {
			kept = append(kept, existing)
		}
	}
	s.LiveBlueprints = kept
	s.DisabledBlueprints = setMember(s.DisabledBlueprints, name, false)
	s.SpanMetricsBlueprints = setMember(s.SpanMetricsBlueprints, name, false)
	prefix := name + "/"
	dropPrefixed := func(values []string) []string {
		out := make([]string, 0, len(values))
		for _, v := range values {
			if !strings.HasPrefix(v, prefix) {
				out = append(out, v)
			}
		}
		return out
	}
	s.ActiveScenarios = dropPrefixed(s.ActiveScenarios)
	s.DisabledConstructs = dropPrefixed(s.DisabledConstructs)
	for key := range s.Scaling {
		if strings.HasPrefix(key, prefix) {
			delete(s.Scaling, key)
		}
	}
	incidents := make([]RuntimeIncident, 0, len(s.RuntimeIncidents))
	for _, ri := range s.RuntimeIncidents {
		if ri.Blueprint != name {
			incidents = append(incidents, ri)
		}
	}
	s.RuntimeIncidents = incidents
}
