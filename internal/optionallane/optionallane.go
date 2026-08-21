// SPDX-License-Identifier: AGPL-3.0-only

// Package optionallane classifies optional product lanes from secret-safe facts.
package optionallane

import (
	"fmt"
	"sort"
)

// Lane is the closed set of optional product lanes.
type Lane string

const (
	RUM                 Lane = "rum"
	FleetMetrics        Lane = "fleet_metrics"
	FleetRegistration   Lane = "fleet_registration"
	SyntheticMonitoring Lane = "synthetic_monitoring"
	SelfObservability   Lane = "self_observability"
	ProcessProfiling    Lane = "process_profiling"
	Sigil               Lane = "sigil"
	PrivateGit          Lane = "private_git"
	SyntheticProfiles   Lane = "synthetic_profiles"
)

// State is the serialized lane disposition.
type State string

const (
	Enabled     State = "enabled"
	Partial     State = "partial"
	Disabled    State = "disabled"
	Unsupported State = "unsupported"
)

// Reason is the closed explanation for a disposition.
type Reason string

const (
	ReasonIntentionallyDisabled    Reason = "intentionally_disabled"
	ReasonUnsupportedRuntime       Reason = "unsupported_runtime"
	ReasonMissingFields            Reason = "missing_fields"
	ReasonDeclarationMissing       Reason = "declaration_missing"
	ReasonEmitterMissing           Reason = "emitter_missing"
	ReasonDryRun                   Reason = "dry_run"
	ReasonVerificationNotAttempted Reason = "verification_not_attempted"
	ReasonVerificationFailed       Reason = "verification_failed"
	ReasonEnabled                  Reason = "enabled"
)

// Gate is a declaration or emitter gate. NotRequired is useful for status facts
// where a product has no blueprint declaration or process emitter of its own.
type Gate string

const (
	GateNotRequired Gate = "not_required"
	GateSatisfied   Gate = "satisfied"
	GateMissing     Gate = "missing"
)

// Verification is the secret-safe verification result.
type Verification string

const (
	VerificationNotRequired  Verification = "not_required"
	VerificationNotAttempted Verification = "not_attempted"
	VerificationVerified     Verification = "verified"
	VerificationFailed       Verification = "failed"
)

// Field is a closed set of non-secret environment key names that can be missing.
type Field string

const (
	GCFaroCollector     Field = "GC_FARO_COLLECTOR"
	GCFaroAppKey        Field = "GC_FARO_APP_KEY"
	GCFMURL             Field = "GC_FM_URL"
	GCFMStackID         Field = "GC_FM_STACK_ID"
	GCFMToken           Field = "GC_FM_TOKEN"
	GCSMURL             Field = "GC_SM_URL"
	GCSMToken           Field = "GC_SM_TOKEN"
	GCSelfOTLPEndpoint  Field = "GC_SELF_OTLP_ENDPOINT"
	GCSelfOTLPUser      Field = "GC_SELF_OTLP_USER"
	GCSelfOTLPPassword  Field = "GC_SELF_OTLP_PASSWORD"
	SelfObsEnabled      Field = "SELFOBS_ENABLED"
	GCPyroscopeURL      Field = "GC_PYROSCOPE_URL"
	GCPyroscopeUser     Field = "GC_PYROSCOPE_USER"
	GCPyroscopePassword Field = "GC_PYROSCOPE_PASSWORD"
	GCSigilEndpoint     Field = "GC_SIGIL_ENDPOINT"
	GCSigilTenantID     Field = "GC_SIGIL_TENANT_ID"
	GCSigilToken        Field = "GC_SIGIL_TOKEN"
	GITToken            Field = "GIT_TOKEN"
	GCProfilesURL       Field = "GC_PROFILES_URL"
	GCProfilesUser      Field = "GC_PROFILES_USER"
	GCToken             Field = "GC_TOKEN"
)

// Input contains only presence, gate, enum, and count facts. It intentionally
// has no endpoint, identifier, credential, blueprint, or free-form text fields.
type Input struct {
	Requested     bool
	Unsupported   bool
	DryRun        bool
	Declaration   Gate
	Emitter       Gate
	Verification  Verification
	MissingFields []Field
}

// Disposition is the redacted JSON-safe result.
type Disposition struct {
	Lane          Lane         `json:"lane"`
	Requested     bool         `json:"requested"`
	State         State        `json:"state"`
	Reason        Reason       `json:"reason"`
	Declaration   Gate         `json:"declaration"`
	Emitter       Gate         `json:"emitter"`
	Verification  Verification `json:"verification"`
	MissingFields []Field      `json:"missing_fields,omitempty"`
}

var knownLanes = []Lane{RUM, FleetMetrics, FleetRegistration, SyntheticMonitoring, SelfObservability, ProcessProfiling, Sigil, PrivateGit, SyntheticProfiles}

var knownFields = map[Field]struct{}{
	GCFaroCollector: {}, GCFaroAppKey: {}, GCFMURL: {}, GCFMStackID: {}, GCFMToken: {},
	GCSMURL: {}, GCSMToken: {}, GCSelfOTLPEndpoint: {}, GCSelfOTLPUser: {},
	GCSelfOTLPPassword: {}, SelfObsEnabled: {}, GCPyroscopeURL: {}, GCPyroscopeUser: {},
	GCPyroscopePassword: {}, GCSigilEndpoint: {}, GCSigilTenantID: {}, GCSigilToken: {},
	GITToken: {}, GCProfilesURL: {}, GCProfilesUser: {}, GCToken: {},
}

// KnownLanes returns the canonical lane order, independent of caller mutation.
func KnownLanes() []Lane { return append([]Lane(nil), knownLanes...) }

// Evaluate applies the deterministic precedence: unsupported requested,
// unrequested disabled, incomplete partial, and complete enabled.
func Evaluate(lane Lane, in Input) Disposition {
	missing := uniqueSorted(in.MissingFields)
	d := Disposition{Lane: lane, Requested: in.Requested, Declaration: in.Declaration, Emitter: in.Emitter, Verification: in.Verification, MissingFields: missing}
	switch {
	case in.Requested && in.Unsupported:
		d.State, d.Reason = Unsupported, ReasonUnsupportedRuntime
	case !in.Requested:
		d.State, d.Reason = Disabled, ReasonIntentionallyDisabled
		d.MissingFields = nil
	case in.DryRun:
		d.State, d.Reason = Partial, ReasonDryRun
	case len(missing) > 0:
		d.State, d.Reason = Partial, ReasonMissingFields
	case in.Declaration == GateMissing:
		d.State, d.Reason = Partial, ReasonDeclarationMissing
	case in.Emitter == GateMissing:
		d.State, d.Reason = Partial, ReasonEmitterMissing
	case in.Verification == VerificationNotAttempted:
		d.State, d.Reason = Partial, ReasonVerificationNotAttempted
	case in.Verification == VerificationFailed:
		d.State, d.Reason = Partial, ReasonVerificationFailed
	default:
		d.State, d.Reason = Enabled, ReasonEnabled
	}
	return d
}

// Entry associates one input with one lane. A slice is used so duplicate lanes
// remain observable and rejectable at the boundary.
type Entry struct {
	Lane  Lane
	Input Input
}

// EvaluateAll requires exactly one input for every known lane, rejects duplicate
// or unknown lanes, and returns lane-sorted results.
func EvaluateAll(entries []Entry) ([]Disposition, error) {
	if len(entries) != len(knownLanes) {
		return nil, fmt.Errorf("optionallane: expected exactly %d lanes, got %d", len(knownLanes), len(entries))
	}
	known := make(map[Lane]struct{}, len(knownLanes))
	for _, lane := range knownLanes {
		known[lane] = struct{}{}
	}
	seen := make(map[Lane]struct{}, len(entries))
	for _, entry := range entries {
		if _, ok := known[entry.Lane]; !ok {
			return nil, fmt.Errorf("optionallane: unknown lane %q", entry.Lane)
		}
		if _, duplicate := seen[entry.Lane]; duplicate {
			return nil, fmt.Errorf("optionallane: duplicate lane %q", entry.Lane)
		}
		if err := validateInput(entry.Input); err != nil {
			return nil, fmt.Errorf("optionallane: lane %q: %w", entry.Lane, err)
		}
		seen[entry.Lane] = struct{}{}
	}
	out := make([]Disposition, 0, len(knownLanes))
	for _, entry := range entries {
		out = append(out, Evaluate(entry.Lane, entry.Input))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Lane < out[j].Lane })
	return out, nil
}

func validateInput(in Input) error {
	switch in.Declaration {
	case GateNotRequired, GateSatisfied, GateMissing:
	default:
		return fmt.Errorf("unknown declaration gate %q", in.Declaration)
	}
	switch in.Emitter {
	case GateNotRequired, GateSatisfied, GateMissing:
	default:
		return fmt.Errorf("unknown emitter gate %q", in.Emitter)
	}
	switch in.Verification {
	case VerificationNotRequired, VerificationNotAttempted, VerificationVerified, VerificationFailed:
	default:
		return fmt.Errorf("unknown verification %q", in.Verification)
	}
	for _, field := range in.MissingFields {
		if _, ok := knownFields[field]; !ok {
			return fmt.Errorf("unknown missing field %q", field)
		}
	}
	return nil
}

func uniqueSorted(fields []Field) []Field {
	seen := make(map[Field]struct{}, len(fields))
	for _, field := range fields {
		if field != "" {
			seen[field] = struct{}{}
		}
	}
	out := make([]Field, 0, len(seen))
	for field := range seen {
		out = append(out, field)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
