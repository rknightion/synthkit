// SPDX-License-Identifier: AGPL-3.0-only

package preflight

import (
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/optionallane"
)

// Optional reports configuration-only dispositions for every optional product lane. Runtime
// declaration, emitter, and landing facts are deliberately not guessed by preflight; authenticated
// status evaluates those same closed inputs after blueprint resolution and delivery observation.
func Optional(cfg *config.Config) ([]optionallane.Disposition, error) {
	base := func(requested, dryRun bool, missing []optionallane.Field) optionallane.Input {
		return optionallane.Input{
			Requested: requested, DryRun: dryRun, MissingFields: missing,
			Declaration: optionallane.GateNotRequired, Emitter: optionallane.GateNotRequired,
			Verification: optionallane.VerificationNotRequired,
		}
	}
	entries := []optionallane.Entry{
		{Lane: optionallane.RUM, Input: base(
			anyPresent(cfg.FaroCollector, cfg.FaroAppKey), cfg.DryRun,
			missingFields(
				field(optionallane.GCFaroCollector, cfg.FaroCollector != ""),
				field(optionallane.GCFaroAppKey, cfg.FaroAppKey != ""),
			),
		)},
		// Fleet metrics are requested by blueprint declarations, not environment configuration.
		{Lane: optionallane.FleetMetrics, Input: base(false, false, nil)},
		{Lane: optionallane.FleetRegistration, Input: base(
			anyPresent(cfg.FMURL, cfg.FMStackID, cfg.FMToken), cfg.DryRun,
			missingFields(field(optionallane.GCFMURL, cfg.FMURL != ""), field(optionallane.GCFMStackID, cfg.FMStackID != ""), field(optionallane.GCFMToken, cfg.FMToken != "")),
		)},
		{Lane: optionallane.SyntheticMonitoring, Input: base(
			anyPresent(cfg.SMURL, cfg.SMToken), false,
			missingFields(field(optionallane.GCSMURL, cfg.SMURL != ""), field(optionallane.GCSMToken, cfg.SMToken != "")),
		)},
		{Lane: optionallane.SelfObservability, Input: base(
			cfg.SelfObsEnabled || anyPresent(cfg.SelfOTLPEndpoint, cfg.SelfOTLPUser, cfg.SelfOTLPPassword), false,
			missingFields(
				field(optionallane.SelfObsEnabled, cfg.SelfObsEnabled),
				field(optionallane.GCSelfOTLPEndpoint, cfg.SelfOTLPEndpoint != ""),
				field(optionallane.GCSelfOTLPUser, cfg.SelfOTLPUser != ""),
				field(optionallane.GCSelfOTLPPassword, cfg.SelfOTLPPassword != ""),
			),
		)},
		{Lane: optionallane.ProcessProfiling, Input: base(
			anyPresent(cfg.PyroscopeURL, cfg.PyroscopeUser, cfg.PyroscopePassword), false,
			missingFields(
				field(optionallane.SelfObsEnabled, cfg.SelfObsEnabled),
				field(optionallane.GCPyroscopeURL, cfg.PyroscopeURL != ""),
				field(optionallane.GCPyroscopeUser, cfg.PyroscopeUser != ""),
				field(optionallane.GCPyroscopePassword, cfg.PyroscopePassword != ""),
			),
		)},
		{Lane: optionallane.Sigil, Input: base(
			anyPresent(cfg.SigilEndpoint, cfg.SigilTenantID, cfg.SigilToken), cfg.DryRun,
			missingFields(field(optionallane.GCSigilEndpoint, cfg.SigilEndpoint != ""), field(optionallane.GCSigilTenantID, cfg.SigilTenantID != ""), field(optionallane.GCSigilToken, cfg.SigilToken != "")),
		)},
		{Lane: optionallane.PrivateGit, Input: base(cfg.GitTokenDefault != "", false, nil)},
		{Lane: optionallane.SyntheticProfiles, Input: base(
			anyPresent(cfg.ProfilesURL, cfg.ProfilesUser), cfg.DryRun,
			missingFields(field(optionallane.GCProfilesURL, cfg.ProfilesURL != ""), field(optionallane.GCProfilesUser, cfg.ProfilesUser != ""), field(optionallane.GCToken, cfg.Token != "")),
		)},
	}
	return optionallane.EvaluateAll(entries)
}

type fieldPresence struct {
	field   optionallane.Field
	present bool
}

func field(name optionallane.Field, present bool) fieldPresence {
	return fieldPresence{field: name, present: present}
}

func missingFields(fields ...fieldPresence) []optionallane.Field {
	missing := make([]optionallane.Field, 0, len(fields))
	for _, candidate := range fields {
		if !candidate.present {
			missing = append(missing, candidate.field)
		}
	}
	return missing
}

func anyPresent(values ...string) bool {
	for _, value := range values {
		if value != "" {
			return true
		}
	}
	return false
}
