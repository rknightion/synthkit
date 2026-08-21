// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/fleetstatus"
	"github.com/rknightion/synthkit/internal/optionallane"
	"github.com/rknightion/synthkit/internal/pushstatus"
	appworkload "github.com/rknightion/synthkit/internal/workload/app"
	webworkload "github.com/rknightion/synthkit/internal/workload/webservice"
)

type optionalRuntimeFacts struct {
	rumDeclared             bool
	fleetDeclared           bool
	sigilDeclared           bool
	profilesDeclared        bool
	smDeclared              bool
	smRegistered            bool
	privateGitDeclared      bool
	privateGitCredentialGap bool
	privateGitVerified      bool
}

func collectOptionalRuntimeFacts(resolved []*blueprint.Resolved, sources []bpsource.Source, cfg *config.Config, smState smHandoff) optionalRuntimeFacts {
	facts := optionalRuntimeFacts{smDeclared: smState.Declared, smRegistered: smState.Registered, privateGitVerified: true}
	for _, bp := range resolved {
		for _, construct := range bp.Constructs {
			switch construct.Kind {
			case fleetmgmt.Kind:
				facts.fleetDeclared = true
			case "k8s_profiling":
				facts.profilesDeclared = true
			}
		}
		for _, workload := range bp.Workloads {
			facts.rumDeclared = facts.rumDeclared || workload.RUM
			if workload.Kind == "ai_agent" {
				facts.sigilDeclared = true
			}
			switch typed := workload.Config.(type) {
			case *webworkload.Config:
				facts.profilesDeclared = facts.profilesDeclared || (typed.Pyroscope != nil && typed.Pyroscope.Enabled && typed.Pyroscope.ModeOrDefault() != "scraped")
			case *appworkload.Config:
				for _, service := range typed.Services {
					if service.Pyroscope != nil && service.Pyroscope.Enabled && service.Pyroscope.ModeOrDefault() != "scraped" {
						facts.profilesDeclared = true
					}
				}
			}
		}
	}
	for _, source := range sources {
		// An empty token_env_var is the explicit public-source contract. GIT_TOKEN opts those
		// sources into the default private-token path; a named token variable is always private.
		if source.TokenEnvVar == "" && cfg.GitTokenDefault == "" {
			continue
		}
		facts.privateGitDeclared = true
		if source.TokenEnvVar != "" && os.Getenv(source.TokenEnvVar) == "" {
			facts.privateGitCredentialGap = true
		}
		if source.LoadedSHA == "" || source.LastErr != "" {
			facts.privateGitVerified = false
		}
	}
	return facts
}

func optionalRuntimeDispositions(cfg *config.Config, facts optionalRuntimeFacts, lanes []pushstatus.LaneStatus, fleet fleetstatus.FleetStat, fleetExpected int, selfObsEnabled, processProfilingEnabled bool) []optionallane.Disposition {
	gate := func(ok bool) optionallane.Gate {
		if ok {
			return optionallane.GateSatisfied
		}
		return optionallane.GateMissing
	}
	verification := func(ok bool) optionallane.Verification {
		if ok {
			return optionallane.VerificationVerified
		}
		return optionallane.VerificationNotAttempted
	}
	sinkVerification := func(name string) optionallane.Verification {
		for _, lane := range lanes {
			if lane.Name != name {
				continue
			}
			switch lane.State {
			case pushstatus.LaneSuccess:
				return optionallane.VerificationVerified
			case pushstatus.LaneError, pushstatus.LaneStaleSuccess:
				return optionallane.VerificationFailed
			}
		}
		return optionallane.VerificationNotAttempted
	}
	missing := func(fields ...runtimeField) []optionallane.Field {
		out := make([]optionallane.Field, 0, len(fields))
		for _, item := range fields {
			if !item.present {
				out = append(out, item.name)
			}
		}
		return out
	}
	field := func(name optionallane.Field, present bool) runtimeField {
		return runtimeField{name: name, present: present}
	}
	any := func(values ...string) bool {
		for _, value := range values {
			if value != "" {
				return true
			}
		}
		return false
	}
	input := func(requested, dryRun bool, declaration, emitter optionallane.Gate, verify optionallane.Verification, fields []optionallane.Field) optionallane.Input {
		return optionallane.Input{Requested: requested, DryRun: dryRun, Declaration: declaration, Emitter: emitter, Verification: verify, MissingFields: fields}
	}
	fmRequested := any(cfg.FMURL, cfg.FMStackID, cfg.FMToken)
	selfRequested := cfg.SelfObsEnabled || any(cfg.SelfOTLPEndpoint, cfg.SelfOTLPUser, cfg.SelfOTLPPassword)
	processRequested := any(cfg.PyroscopeURL, cfg.PyroscopeUser, cfg.PyroscopePassword)
	rumRequested := facts.rumDeclared || any(cfg.FaroCollector, cfg.FaroAppKey)
	sigilRequested := facts.sigilDeclared || any(cfg.SigilEndpoint, cfg.SigilTenantID, cfg.SigilToken)
	profilesRequested := facts.profilesDeclared || any(cfg.ProfilesURL, cfg.ProfilesUser)
	smRequested := facts.smDeclared || any(cfg.SMURL, cfg.SMToken)
	privateGitRequested := facts.privateGitDeclared || cfg.GitTokenDefault != ""
	fleetVerification := optionallane.VerificationNotAttempted
	if fleetExpected > 0 && fleet.Registered == fleetExpected && fleet.HeartbeatHealthy == fleetExpected && fleet.LastHeartbeatOKMs > 0 {
		fleetVerification = optionallane.VerificationVerified
	} else if fleet.Failures > 0 {
		fleetVerification = optionallane.VerificationFailed
	}
	entries := []optionallane.Entry{
		{Lane: optionallane.RUM, Input: input(rumRequested, cfg.DryRun, gate(facts.rumDeclared), gate(cfg.RUMEnabled()), sinkVerification("faro"), missing(field(optionallane.GCFaroCollector, cfg.FaroCollector != ""), field(optionallane.GCFaroAppKey, cfg.FaroAppKey != "")))},
		{Lane: optionallane.FleetMetrics, Input: input(facts.fleetDeclared, cfg.DryRun, gate(facts.fleetDeclared), gate(facts.fleetDeclared), optionallane.VerificationNotRequired, nil)},
		// No FM fields is the intentional metrics-only mode, so registration is disabled rather than partial.
		{Lane: optionallane.FleetRegistration, Input: input(fmRequested, cfg.DryRun, gate(facts.fleetDeclared), gate(cfg.FMURL != "" && cfg.FMStackID != "" && cfg.FMToken != ""), fleetVerification, missing(field(optionallane.GCFMURL, cfg.FMURL != ""), field(optionallane.GCFMStackID, cfg.FMStackID != ""), field(optionallane.GCFMToken, cfg.FMToken != "")))},
		{Lane: optionallane.SyntheticMonitoring, Input: input(smRequested, false, gate(facts.smDeclared), gate(facts.smRegistered), verification(facts.smRegistered), missing(field(optionallane.GCSMURL, cfg.SMURL != ""), field(optionallane.GCSMToken, cfg.SMToken != "")))},
		{Lane: optionallane.SelfObservability, Input: input(selfRequested, false, optionallane.GateNotRequired, gate(selfObsEnabled), optionallane.VerificationNotRequired, missing(field(optionallane.SelfObsEnabled, cfg.SelfObsEnabled), field(optionallane.GCSelfOTLPEndpoint, cfg.SelfOTLPEndpoint != ""), field(optionallane.GCSelfOTLPUser, cfg.SelfOTLPUser != ""), field(optionallane.GCSelfOTLPPassword, cfg.SelfOTLPPassword != "")))},
		{Lane: optionallane.ProcessProfiling, Input: input(processRequested, false, optionallane.GateNotRequired, gate(processProfilingEnabled), optionallane.VerificationNotRequired, missing(field(optionallane.SelfObsEnabled, cfg.SelfObsEnabled), field(optionallane.GCPyroscopeURL, cfg.PyroscopeURL != ""), field(optionallane.GCPyroscopeUser, cfg.PyroscopeUser != ""), field(optionallane.GCPyroscopePassword, cfg.PyroscopePassword != "")))},
		{Lane: optionallane.Sigil, Input: input(sigilRequested, cfg.DryRun, gate(facts.sigilDeclared), gate(cfg.SigilEnabled()), sinkVerification("sigil"), missing(field(optionallane.GCSigilEndpoint, cfg.SigilEndpoint != ""), field(optionallane.GCSigilTenantID, cfg.SigilTenantID != ""), field(optionallane.GCSigilToken, cfg.SigilToken != "")))},
		{Lane: optionallane.PrivateGit, Input: input(privateGitRequested, false, gate(facts.privateGitDeclared), gate(!facts.privateGitCredentialGap), verification(facts.privateGitVerified), missing(field(optionallane.GITToken, !facts.privateGitCredentialGap)))},
		{Lane: optionallane.SyntheticProfiles, Input: input(profilesRequested, cfg.DryRun, gate(facts.profilesDeclared), gate(cfg.SynthProfilesEnabled()), sinkVerification("pyroscope"), missing(field(optionallane.GCProfilesURL, cfg.ProfilesURL != ""), field(optionallane.GCProfilesUser, cfg.ProfilesUser != ""), field(optionallane.GCToken, cfg.Token != "")))},
	}
	dispositions, err := optionallane.EvaluateAll(entries)
	if err != nil {
		return nil
	}
	return dispositions
}

type runtimeField struct {
	name    optionallane.Field
	present bool
}
