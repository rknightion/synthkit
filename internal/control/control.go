// SPDX-License-Identifier: AGPL-3.0-only

// Package control is synthkit's schema-driven runtime control plane: live knobs
// (volume multiplier, failure injection, per-blueprint enable) persisted to a JSON
// snapshot. Invariants: knob structs carry NO omitempty (zero/false must round-trip —
// I24); the snapshot is saved atomically into a DIRECTORY (I25); GETs are strictly
// side-effect-free and CORS echoes the request's headers (I26).
package control

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

// FailureSetting is one live failure mode's knob set. NO omitempty — ever (I24).
type FailureSetting struct {
	Enabled   bool    `json:"enabled"`
	Intensity float64 `json:"intensity"`
	Scope     string  `json:"scope"`
}

// RuntimeIncident is an operator-created scheduled incident (control-plane analogue of a
// blueprint `incidents:` entry). It is evaluated live by the runner's per-blueprint runtime
// engine. NO omitempty — ever (I24). At uses shape's schedule grammar (RFC3339 | local
// "2006-01-02T15:04[:05]" | daily "HH:MM[:SS]" | interval "everyNm"); For is a Go duration.
type RuntimeIncident struct {
	ID        string  `json:"id"`        // server-minted; stable key for DELETE
	Blueprint string  `json:"blueprint"` // owning blueprint (incidents are blueprint-scoped)
	Mode      string  `json:"mode"`      // failure mode
	Target    string  `json:"target"`    // "" = blueprint-wide | target name | "<axis>:*"
	At        string  `json:"at"`
	For       string  `json:"for"`
	Intensity float64 `json:"intensity"`
}

// State is the complete persisted knob state. NO omitempty — ever (I24).
type State struct {
	VolumeMultiplier      float64                   `json:"volume_multiplier"`
	DisabledBlueprints    []string                  `json:"disabled_blueprints"`
	Failures              map[string]FailureSetting `json:"failures"`
	ActiveScenarios       []string                  `json:"active_scenarios"`        // "blueprint/name" ids
	Scaling               map[string]int            `json:"scaling"`                 // target → live count
	DisabledConstructs    []string                  `json:"disabled_constructs"`     // "blueprint/kind:name" keys
	DisabledKinds         []string                  `json:"disabled_kinds"`          // bare construct kinds
	SpanMetricsBlueprints []string                  `json:"span_metrics_blueprints"` // opt-IN: blueprints that emit synthkit's own spanmetrics/service-graph
	RuntimeIncidents      []RuntimeIncident         `json:"runtime_incidents"`
	BlueprintSources      []SourceView              `json:"blueprint_sources"` // configured git sources (Task 14)
}

// SpanMetricsEnabled reports whether synthkit should emit its OWN backend spanmetrics +
// service-graph for the named blueprint. Opt-IN (default OFF): emit only when listed, so the
// default defers to Grafana Cloud metrics-generator / beyla.
func (s State) SpanMetricsEnabled(blueprint string) bool {
	return slices.Contains(s.SpanMetricsBlueprints, blueprint)
}

// SetFailure sets/merges one failure mode.
func (s *State) SetFailure(mode string, f FailureSetting) {
	if s.Failures == nil {
		s.Failures = map[string]FailureSetting{}
	}
	s.Failures[mode] = f
}

// DefaultState returns a fully-defaulted control State (VolumeMultiplier 1.0, all slices/maps
// non-nil). Exported so callers outside the package (e.g. dashgen) can build a properly-defaulted
// state — a bare State{} would zero VolumeMultiplier and emit nothing.
func DefaultState() State {
	return State{
		VolumeMultiplier:      1.0,
		DisabledBlueprints:    []string{},
		Failures:              map[string]FailureSetting{},
		ActiveScenarios:       []string{},
		Scaling:               map[string]int{},
		DisabledConstructs:    []string{},
		DisabledKinds:         []string{},
		SpanMetricsBlueprints: []string{},
		RuntimeIncidents:      []RuntimeIncident{},
		BlueprintSources:      []SourceView{},
	}
}

// Store holds the live state and persists every change atomically. The persist-health
// fields (persistErr/persistErrMs/persistOKMs) are RUNTIME status surfaced via
// PersistHealth() for /control/status — they are deliberately NOT part of the persisted
// State snapshot (a persist failure shouldn't itself be persisted).
type Store struct {
	mu           sync.Mutex
	path         string
	state        State
	now          func() time.Time
	persistErr   string
	persistErrMs int64
	persistOKMs  int64
}

// NewStore loads the snapshot at path (defaults apply when absent/corrupt — loud log,
// never a crash).
func NewStore(path string) *Store {
	st := &Store{path: path, state: DefaultState(), now: time.Now}
	data, err := os.ReadFile(path)
	if err == nil {
		var s State
		if jerr := json.Unmarshal(data, &s); jerr != nil {
			log.Printf("control: snapshot %s unreadable (%v) — starting from defaults", path, jerr)
		} else {
			if s.Failures == nil {
				s.Failures = map[string]FailureSetting{}
			}
			if s.DisabledBlueprints == nil {
				s.DisabledBlueprints = []string{}
			}
			if s.ActiveScenarios == nil {
				s.ActiveScenarios = []string{}
			}
			if s.Scaling == nil {
				s.Scaling = map[string]int{}
			}
			if s.DisabledConstructs == nil {
				s.DisabledConstructs = []string{}
			}
			if s.DisabledKinds == nil {
				s.DisabledKinds = []string{}
			}
			if s.SpanMetricsBlueprints == nil {
				s.SpanMetricsBlueprints = []string{}
			}
			if s.RuntimeIncidents == nil {
				s.RuntimeIncidents = []RuntimeIncident{}
			}
			if s.BlueprintSources == nil {
				s.BlueprintSources = []SourceView{}
			}
			if s.VolumeMultiplier <= 0 {
				s.VolumeMultiplier = 1.0
			}
			st.state = s
		}
	}
	return st
}

// Snapshot returns a deep copy of the current state.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.state)
}

// Update mutates the state under the lock, persists atomically inside the lock,
// and returns the new snapshot. persist is called under the mutex so that a
// concurrent Reset/Update cannot overwrite the file with a stale copy.
func (s *Store) Update(fn func(*State)) State {
	s.mu.Lock()
	fn(&s.state)
	out := cloneState(s.state)
	s.recordPersist(s.persist(out))
	s.mu.Unlock()
	return out
}

// Reset returns the state to defaults (persisted atomically under the lock).
func (s *Store) Reset() State {
	s.mu.Lock()
	s.state = DefaultState()
	out := cloneState(s.state)
	s.recordPersist(s.persist(out))
	s.mu.Unlock()
	return out
}

// recordPersist folds a persist outcome into the runtime health fields. Caller holds s.mu.
func (s *Store) recordPersist(err error) {
	ms := s.now().UnixMilli()
	if err != nil {
		s.persistErr, s.persistErrMs = err.Error(), ms
		return
	}
	s.persistErr, s.persistOKMs = "", ms
}

// PersistHealth reports the last persist outcome (runtime status, not persisted state).
func (s *Store) PersistHealth() PersistHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	return PersistHealth{LastOKMs: s.persistOKMs, LastErrorMs: s.persistErrMs, LastError: s.persistErr}
}

// PersistHealth is the last snapshot-persist outcome, surfaced via /control/status.
// It is NOT part of the persisted State (see Store).
type PersistHealth struct {
	LastOKMs    int64  `json:"last_ok_ms"`
	LastErrorMs int64  `json:"last_error_ms"`
	LastError   string `json:"last_error"`
}

// persist writes the snapshot atomically: temp file in the SAME directory + rename
// (I25 — this is why the state mount must be a directory, never a single file).
func (s *Store) persist(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		log.Printf("control: marshal snapshot: %v", err)
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".control-state-*.tmp")
	if err != nil {
		log.Printf("control: persist: %v", err)
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(name)
		log.Printf("control: persist write: %v/%v", werr, cerr)
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(name, s.path); err != nil {
		os.Remove(name)
		log.Printf("control: persist rename: %v", err)
		return err
	}
	return nil
}

func cloneState(s State) State {
	out := State{
		VolumeMultiplier:   s.VolumeMultiplier,
		DisabledBlueprints: append([]string{}, s.DisabledBlueprints...),
		Failures:           make(map[string]FailureSetting, len(s.Failures)),
	}
	for k, v := range s.Failures {
		out.Failures[k] = v
	}
	out.ActiveScenarios = append([]string{}, s.ActiveScenarios...)
	out.Scaling = make(map[string]int, len(s.Scaling))
	for k, v := range s.Scaling {
		out.Scaling[k] = v
	}
	out.DisabledConstructs = append([]string{}, s.DisabledConstructs...)
	out.DisabledKinds = append([]string{}, s.DisabledKinds...)
	out.SpanMetricsBlueprints = append([]string{}, s.SpanMetricsBlueprints...)
	out.RuntimeIncidents = append([]RuntimeIncident{}, s.RuntimeIncidents...)
	out.BlueprintSources = append([]SourceView{}, s.BlueprintSources...)
	return out
}

// Descriptor describes one knob for /control/schema (the operator UI renders this).
type Descriptor struct {
	Key     string  `json:"key"`
	Type    string  `json:"type"`
	Help    string  `json:"help"`
	Default any     `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
}

// Descriptors is the knob catalog.
func Descriptors() []Descriptor {
	return []Descriptor{
		{Key: "volume_multiplier", Type: "float", Help: "master load knob — scales ALL synthetic volume (metrics + correlated traces/logs) coherently", Default: 1.0, Min: 0, Max: 100},
		{Key: "disabled_blueprints", Type: "[]string", Help: "blueprint names whose instances stop ticking (re-enable resumes; counters continue from state)", Default: []string{}},
		{Key: "failures", Type: "map[mode]{enabled,intensity,scope}", Help: "live failure injection, unioned with the scheduled incident windows; scope targets a workload/instance name", Default: map[string]FailureSetting{}},
	}
}

const maxFailureModes = 256

// validateState bounds-checks a candidate state.
func validateState(s State) error {
	if s.VolumeMultiplier < 0 || s.VolumeMultiplier > 100 {
		return fmt.Errorf("volume_multiplier %v out of [0,100]", s.VolumeMultiplier)
	}
	if len(s.Failures) > maxFailureModes {
		return fmt.Errorf("failures map exceeds cap of %d modes (got %d)", maxFailureModes, len(s.Failures))
	}
	for mode, f := range s.Failures {
		if f.Intensity < 0 || f.Intensity > 1 {
			return fmt.Errorf("failure %q intensity %v out of [0,1]", mode, f.Intensity)
		}
	}
	return nil
}

// SchemaSource supplies the blueprint-derived control schema. Implemented by the runner; the
// control package stays isolated from runner/blueprint by depending only on this interface.
type SchemaSource interface {
	ControlSchema() Schema
}

// BlueprintSourcer optionally supplies a blueprint's raw YAML source for the operator UI.
type BlueprintSourcer interface {
	BlueprintSource(name string) (string, bool)
}

// Schema is the rich, blueprint-derived knob catalogue served at GET /control/schema.
type Schema struct {
	VolumeMultiplier Descriptor          `json:"volume_multiplier"`
	Blueprints       []string            `json:"blueprints"`
	BlueprintMeta    []BlueprintMetaInfo `json:"blueprint_meta,omitempty"` // optional human-facing annotation, parallel to Blueprints
	Modes            []ModeInfo          `json:"modes"`
	Targets          []TargetInfo        `json:"targets"`
	Scenarios        []ScenarioInfo      `json:"scenarios"`
	Constructs       []ConstructInfo     `json:"constructs"`
	Kinds            []string            `json:"kinds"`
}

// MetaFields is the descriptive annotation payload shared by blueprint- and environment-level
// metadata (purely human-facing; never affects emission). Every field is omitempty so blueprints
// or envs without metadata serialize to nothing.
type MetaFields struct {
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Owner       string            `json:"owner,omitempty"`
	Links       map[string]string `json:"links,omitempty"`
	Category    string            `json:"category,omitempty"`
}

// EnvMetaInfo is one declared environment's name + metadata (UI display, decl order).
type EnvMetaInfo struct {
	Name string `json:"name"`
	MetaFields
}

// BlueprintMetaInfo carries one blueprint's name + metadata + per-env metadata for the operator UI.
type BlueprintMetaInfo struct {
	Name string `json:"name"`
	MetaFields
	Environments []EnvMetaInfo `json:"environments,omitempty"`
}

// ConstructInfo is one construct instance in the schema, blueprint-qualified, with its enable state.
type ConstructInfo struct {
	Blueprint string `json:"blueprint"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
}

// ConstructKey is the single key formatter for a construct instance: "blueprint/kind:name".
func ConstructKey(blueprint, kind, name string) string { return blueprint + "/" + kind + ":" + name }

// validateConstructs checks every "blueprint/kind:name" key exists in the derived schema.
func validateConstructs(keys []string, schema Schema) error {
	known := map[string]bool{}
	for _, c := range schema.Constructs {
		known[ConstructKey(c.Blueprint, c.Kind, c.Name)] = true
	}
	for _, k := range keys {
		if !known[k] {
			return fmt.Errorf("unknown construct %q", k)
		}
	}
	return nil
}

// validateKinds checks every bare construct kind exists in the derived schema.
func validateKinds(kinds []string, schema Schema) error {
	known := map[string]bool{}
	for _, k := range schema.Kinds {
		known[k] = true
	}
	for _, k := range kinds {
		if !known[k] {
			return fmt.Errorf("unknown construct kind %q", k)
		}
	}
	return nil
}

// ModeInfo is one failure mode in the schema.
type ModeInfo struct {
	Name string `json:"name"`
	Axis string `json:"axis"`
	Help string `json:"help"`
}

// TargetInfo is one addressable target; Scalable is non-nil when live-scalable. Blueprint names the
// owning blueprint (authoritative — same as ScenarioInfo/ConstructInfo) so the UI never has to guess
// ownership from a name-prefix heuristic. The scaling wire key is the qualified "Blueprint/Name" id.
type TargetInfo struct {
	Blueprint string        `json:"blueprint"`
	Name      string        `json:"name"`
	Axis      string        `json:"axis"`
	Scalable  *ScalableInfo `json:"scalable"`
}

// ScalableInfo bounds a scalable target and reports its current effective count.
type ScalableInfo struct {
	Dimension string `json:"dimension"`
	Min       int    `json:"min"`
	Max       int    `json:"max"`
	Default   int    `json:"default"`
	Current   int    `json:"current"`
}

// ScenarioInfo is one defined scenario, blueprint-qualified, with its activation state.
type ScenarioInfo struct {
	Blueprint string       `json:"blueprint"`
	Name      string       `json:"name"`
	Title     string       `json:"title"`
	Summary   string       `json:"summary"`
	Effects   []EffectInfo `json:"effects"`
	Active    bool         `json:"active"`
}

// EffectInfo mirrors a scenario effect for display.
type EffectInfo struct {
	Mode      string  `json:"mode"`
	Target    string  `json:"target"`
	Intensity float64 `json:"intensity"`
}

// validateScenarios checks every activation id ("blueprint/name") exists in the derived schema.
func validateScenarios(ids []string, schema Schema) error {
	known := map[string]bool{}
	for _, s := range schema.Scenarios {
		known[s.Blueprint+"/"+s.Name] = true
	}
	for _, id := range ids {
		if !known[id] {
			return fmt.Errorf("unknown scenario %q", id)
		}
	}
	return nil
}

// validateScaling checks each entry targets a scalable target within bounds. Scalable targets are
// workloads, keyed by the qualified "blueprint/name" id (the same form as scenario ids) so the same
// bare workload name declared in two blueprints stays distinct.
func validateScaling(m map[string]int, schema Schema) error {
	bounds := map[string]ScalableInfo{}
	for _, t := range schema.Targets {
		if t.Scalable != nil {
			bounds[t.Blueprint+"/"+t.Name] = *t.Scalable
		}
	}
	for key, count := range m {
		b, ok := bounds[key]
		if !ok {
			return fmt.Errorf("target %q is not scalable", key)
		}
		if count < b.Min || count > b.Max {
			return fmt.Errorf("scaling %q=%d out of [%d,%d]", key, count, b.Min, b.Max)
		}
	}
	return nil
}

// ScheduleSpec formats a shape-engine schedule entry: mode@at/for[#intensity][@target].
// It is the single formatter shared by the control POST path and the runner's incident view,
// mirroring blueprint.scheduleEntry so runtime and declared incidents use identical grammar.
func ScheduleSpec(mode, target, at, forDur string, intensity float64) string {
	entry := mode + "@" + at + "/" + forDur
	if intensity > 0 {
		entry += "#" + strconv.FormatFloat(intensity, 'g', -1, 64)
	}
	if target != "" {
		entry += "@" + target
	}
	return entry
}

// IncidentInfo is one incident (declared in a blueprint OR operator-created at runtime) for
// GET /control/incidents. ActiveNow is computed authoritatively in Go (shape.Engine.Active);
// the UI parses ScheduleSpec for timeline layout only.
type IncidentInfo struct {
	Source       string  `json:"source"` // "declared" | "runtime"
	ID           string  `json:"id"`     // runtime only ("" for declared)
	Blueprint    string  `json:"blueprint"`
	Mode         string  `json:"mode"`
	Target       string  `json:"target"`
	At           string  `json:"at"`  // "" for declared (parse ScheduleSpec UI-side)
	For          string  `json:"for"` // ""  for declared
	ScheduleSpec string  `json:"schedule_spec"`
	Intensity    float64 `json:"intensity"`
	ActiveNow    bool    `json:"active_now"`
}

// IncidentSource supplies the incident list + runtime-incident validation for the
// /control/incidents routes. Implemented by the runner (it owns the shape engines + schema);
// control stays decoupled from runner/shape by depending only on this interface.
type IncidentSource interface {
	ControlIncidents() []IncidentInfo
	ValidateRuntimeIncident(RuntimeIncident) error
}
