// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
)

type listedBlueprints []string

func (l listedBlueprints) Blueprints() []string { return l }

// probeKeys returns the top-level keys of the served public readiness probe, and fails when the
// body does not decode strictly into the unextended ReadinessProbe iff wantFlags is false.
func probeKeys(t *testing.T, h http.Handler, wantFlags bool) map[string]json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/control/readiness", nil))
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &keys); err != nil {
		t.Fatalf("probe is not a JSON object: %v: %s", err, rec.Body.String())
	}
	if !wantFlags {
		dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
		dec.DisallowUnknownFields()
		var strict control.ReadinessProbe
		if err := dec.Decode(&strict); err != nil {
			t.Fatalf("probe carries fields beyond the base probe: %v: %s", err, rec.Body.String())
		}
	}
	return keys
}

func TestReadinessBlueprintFlagsStartupGateServesProbe(t *testing.T) {
	report := func() control.ReadinessReport {
		return control.ReadinessReport{Running: true, HTTPReady: true, Ready: true}
	}
	for _, on := range []bool{false, true} {
		h := control.NewHandler(control.NewStore(filepath.Join(t.TempDir(), "state.json")), nil, "").
			SetStatus(control.StatusSources{Readiness: report})
		applyReadinessBlueprintFlags(h, &config.Config{ReadinessBlueprintFlags: on}, listedBlueprints{"alpha"})
		keys := probeKeys(t, h, on)
		if _, has := keys["blueprint_emitting"]; has != on {
			t.Errorf("setting=%v: blueprint_emitting present=%v", on, has)
		}
	}
}

// TestHAReadinessBlueprintFlagsFollowSetting drives the real HA composition root.
func TestHAReadinessBlueprintFlagsFollowSetting(t *testing.T) {
	for _, on := range []bool{false, true} {
		v, _ := readinessHAViewWith(t, ha.NewGate(), func(c *config.Config) { c.ReadinessBlueprintFlags = on })
		keys := probeKeys(t, v.handler, on)
		if _, has := keys["blueprint_emitting"]; has != on {
			t.Errorf("setting=%v: blueprint_emitting present=%v", on, has)
		}
		if on {
			var flags map[string]bool
			if err := json.Unmarshal(keys["blueprint_emitting"], &flags); err != nil {
				t.Fatal(err)
			}
			if _, ok := flags["test"]; !ok || len(flags) != 1 {
				t.Errorf("flags = %v, want exactly the loaded blueprint", flags)
			}
		}
	}
}
