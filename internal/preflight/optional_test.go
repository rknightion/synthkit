// SPDX-License-Identifier: AGPL-3.0-only

package preflight

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/optionallane"
)

func TestOptionalReportsCompleteSecretSafeConfiguration(t *testing.T) {
	cfg := &config.Config{
		DryRun: true, FaroCollector: "https://secret.example/collect/token", FMURL: "https://fleet.example", FMStackID: "123", FMToken: "fleet-secret",
		SelfObsEnabled: true, SelfOTLPEndpoint: "https://self.example/otlp", SelfOTLPUser: "456", SelfOTLPPassword: "self-secret",
		PyroscopeURL: "https://profiles.example", PyroscopeUser: "789", PyroscopePassword: "profile-secret",
	}
	got, err := Optional(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(optionallane.KnownLanes()) {
		t.Fatalf("got %d optional lanes", len(got))
	}
	byLane := map[optionallane.Lane]optionallane.Disposition{}
	for _, disposition := range got {
		byLane[disposition.Lane] = disposition
	}
	if byLane[optionallane.RUM].State != optionallane.Partial || byLane[optionallane.RUM].Reason != optionallane.ReasonDryRun {
		t.Fatalf("RUM disposition = %+v", byLane[optionallane.RUM])
	}
	if byLane[optionallane.SelfObservability].State != optionallane.Enabled || byLane[optionallane.ProcessProfiling].State != optionallane.Enabled {
		t.Fatalf("self lanes must be independent of DRY_RUN: self=%+v profiling=%+v", byLane[optionallane.SelfObservability], byLane[optionallane.ProcessProfiling])
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(b)
	for _, secret := range []string{cfg.FaroCollector, cfg.FMToken, cfg.SelfOTLPPassword, cfg.PyroscopePassword, "c2VsZi1zZWNyZXQ=", "Bearer self-secret"} {
		if secret != "" && strings.Contains(encoded, secret) {
			t.Fatalf("optional JSON exposed secret form %q: %s", secret, encoded)
		}
	}
}

func TestOptionalPartialFieldsAndDisabledLanes(t *testing.T) {
	got, err := Optional(&config.Config{FMURL: "https://fleet.example", SMToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	byLane := map[optionallane.Lane]optionallane.Disposition{}
	for _, disposition := range got {
		byLane[disposition.Lane] = disposition
	}
	if byLane[optionallane.FleetRegistration].Reason != optionallane.ReasonMissingFields || len(byLane[optionallane.FleetRegistration].MissingFields) != 2 {
		t.Fatalf("Fleet registration = %+v", byLane[optionallane.FleetRegistration])
	}
	if byLane[optionallane.SyntheticMonitoring].Reason != optionallane.ReasonMissingFields || len(byLane[optionallane.SyntheticMonitoring].MissingFields) != 1 {
		t.Fatalf("SM = %+v", byLane[optionallane.SyntheticMonitoring])
	}
	if byLane[optionallane.FleetMetrics].State != optionallane.Disabled || byLane[optionallane.RUM].State != optionallane.Disabled {
		t.Fatalf("unrequested lanes were not disabled: %+v", byLane)
	}
}
