// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/fleetstatus"
	"github.com/rknightion/synthkit/internal/optionallane"
	"github.com/rknightion/synthkit/internal/pushstatus"
)

func TestOptionalRuntimeStartedSelfObservabilityLanesAreEnabled(t *testing.T) {
	cfg := &config.Config{
		SelfObsEnabled:   true,
		SelfOTLPEndpoint: "https://self.example/otlp", SelfOTLPUser: "123", SelfOTLPPassword: "secret",
		PyroscopeURL: "https://profiles.example", PyroscopeUser: "456", PyroscopePassword: "secret",
	}
	got := optionalRuntimeDispositions(cfg, optionalRuntimeFacts{}, nil, fleetstatus.FleetStat{}, 0, true, true)
	byLane := map[optionallane.Lane]optionallane.Disposition{}
	for _, disposition := range got {
		byLane[disposition.Lane] = disposition
	}
	for _, lane := range []optionallane.Lane{optionallane.SelfObservability, optionallane.ProcessProfiling} {
		if byLane[lane].State != optionallane.Enabled || byLane[lane].Verification != optionallane.VerificationNotRequired {
			t.Fatalf("%s = %+v", lane, byLane[lane])
		}
	}
}

func TestOptionalRuntimeDistinguishesPublicAndPrivateGitSources(t *testing.T) {
	public := collectOptionalRuntimeFacts(nil, []bpsource.Source{{TokenEnvVar: "", LoadedSHA: "abc"}}, &config.Config{}, smHandoff{})
	if public.privateGitDeclared || public.privateGitCredentialGap {
		t.Fatalf("public source classified as private: %+v", public)
	}

	t.Setenv("PRIVATE_BLUEPRINT_TOKEN", "")
	private := collectOptionalRuntimeFacts(nil, []bpsource.Source{{TokenEnvVar: "PRIVATE_BLUEPRINT_TOKEN", LoadedSHA: "abc"}}, &config.Config{}, smHandoff{})
	if !private.privateGitDeclared || !private.privateGitCredentialGap {
		t.Fatalf("private source credential gap not reported: %+v", private)
	}

	fallback := collectOptionalRuntimeFacts(nil, []bpsource.Source{{TokenEnvVar: "", LoadedSHA: "abc"}}, &config.Config{GitTokenDefault: "configured"}, smHandoff{})
	if !fallback.privateGitDeclared || fallback.privateGitCredentialGap {
		t.Fatalf("default-token source not classified as private: %+v", fallback)
	}
}

func TestOptionalRuntimeSeparatesFleetModesAndVerifiesSigil(t *testing.T) {
	cfg := &config.Config{SigilEndpoint: "https://sigil.example", SigilTenantID: "123", SigilToken: "secret"}
	facts := optionalRuntimeFacts{fleetDeclared: true, sigilDeclared: true}
	got := optionalRuntimeDispositions(cfg, facts, []pushstatus.LaneStatus{
		{Name: "promrw", State: pushstatus.LaneSuccess, LiveReady: true},
		{Name: "sigil", State: pushstatus.LaneSuccess, LiveReady: true},
	}, fleetstatus.FleetStat{}, 1, false, false)
	byLane := map[optionallane.Lane]optionallane.Disposition{}
	for _, disposition := range got {
		byLane[disposition.Lane] = disposition
	}
	if byLane[optionallane.FleetMetrics].State != optionallane.Enabled {
		t.Fatalf("fleet metrics = %+v", byLane[optionallane.FleetMetrics])
	}
	if byLane[optionallane.FleetRegistration].State != optionallane.Disabled {
		t.Fatalf("metrics-only registration = %+v", byLane[optionallane.FleetRegistration])
	}
	if byLane[optionallane.Sigil].State != optionallane.Enabled || byLane[optionallane.Sigil].Verification != optionallane.VerificationVerified {
		t.Fatalf("sigil = %+v", byLane[optionallane.Sigil])
	}
}

func TestOptionalRuntimeFleetRegistrationRequiresCompleteHealthyRoster(t *testing.T) {
	cfg := &config.Config{FMURL: "https://fleet.example", FMStackID: "123", FMToken: "secret"}
	facts := optionalRuntimeFacts{fleetDeclared: true}
	disposition := func(stat fleetstatus.FleetStat) optionallane.Disposition {
		for _, got := range optionalRuntimeDispositions(cfg, facts, nil, stat, 2, false, false) {
			if got.Lane == optionallane.FleetRegistration {
				return got
			}
		}
		return optionallane.Disposition{}
	}
	if got := disposition(fleetstatus.FleetStat{Registered: 2, HeartbeatHealthy: 1, Heartbeats: 2, LastHeartbeatOKMs: 1}); got.State != optionallane.Partial {
		t.Fatalf("incomplete heartbeat roster = %+v", got)
	}
	if got := disposition(fleetstatus.FleetStat{Registered: 2, HeartbeatHealthy: 2, Heartbeats: 2, LastHeartbeatOKMs: 1}); got.State != optionallane.Enabled || got.Verification != optionallane.VerificationVerified {
		t.Fatalf("complete heartbeat roster = %+v", got)
	}
}

func TestOptionalRuntimeSMAndSecretsAreFailClosed(t *testing.T) {
	cfg := &config.Config{SMURL: "https://sm.example/secret-path", SMToken: "raw-secret"}
	facts := optionalRuntimeFacts{smDeclared: true, smRegistered: false}
	got := optionalRuntimeDispositions(cfg, facts, nil, fleetstatus.FleetStat{}, 0, false, false)
	byLane := map[optionallane.Lane]optionallane.Disposition{}
	for _, disposition := range got {
		byLane[disposition.Lane] = disposition
	}
	if byLane[optionallane.SyntheticMonitoring].State != optionallane.Partial || byLane[optionallane.SyntheticMonitoring].Reason != optionallane.ReasonEmitterMissing {
		t.Fatalf("SM = %+v", byLane[optionallane.SyntheticMonitoring])
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{cfg.SMURL, cfg.SMToken, "cmF3LXNlY3JldA==", "Bearer raw-secret", "raw-secret%"} {
		if strings.Contains(string(b), canary) {
			t.Fatalf("status exposed canary %q: %s", canary, b)
		}
	}
}
