// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/runner"
)

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8088", true},
		{"localhost:8088", true},
		{"[::1]:8088", true},
		{":8088", false}, // empty host = all interfaces, NOT loopback
		{"0.0.0.0:8088", false},
		{"10.0.0.1:8088", false},
		{"192.168.1.5:9090", false},
		{"not-an-addr", false}, // SplitHostPort fails
	}
	for _, c := range cases {
		if got := isLoopback(c.addr); got != c.want {
			t.Errorf("isLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// mustLoad is a test helper that loads a blueprint YAML or fails immediately.
func mustLoad(t *testing.T, yaml []byte) *blueprint.Resolved {
	t.Helper()
	reg := runner.Catalog()
	r, err := blueprint.Load(yaml, reg)
	if err != nil {
		t.Fatalf("blueprint.Load failed: %v", err)
	}
	return r
}

// mustLoadNS loads a namespaced blueprint or fails immediately.
func mustLoadNS(t *testing.T, yaml []byte, ns string) *blueprint.Resolved {
	t.Helper()
	reg := runner.Catalog()
	r, err := blueprint.LoadNamespaced(yaml, ns, reg)
	if err != nil {
		t.Fatalf("blueprint.LoadNamespaced(%q) failed: %v", ns, err)
	}
	return r
}

// TestFoldValidated covers the three required cases for foldValidated:
//
//  1. Built-in + custom that collides on a HOST name → custom is skipped (1 diag),
//     built-in is accepted, builtinErr==nil.
//  2. Two built-ins that collide → builtinErr != nil.
//  3. Built-in + non-colliding custom → both accepted, no skips.
func TestFoldValidated(t *testing.T) {
	// Two minimal blueprints declaring the SAME host name ("sharedhost") to force a collision.
	// The built-in uses name "bp-builtin"; the custom uses name "bp-custom" (different blueprint
	// names so only the host name is the collision trigger, not the blueprint/label name).
	builtinYAML := []byte("name: bp-builtin\nhosts:\n  - name: sharedhost\n    os: linux\n")
	customCollidingYAML := []byte("name: bp-custom\nhosts:\n  - name: sharedhost\n    os: linux\n")
	customOKYAML := []byte("name: bp-ok\nhosts:\n  - name: uniquehost\n    os: linux\n")
	// A second built-in with the same host name (for the two-built-ins collision case).
	builtin2YAML := []byte("name: bp-builtin2\nhosts:\n  - name: sharedhost\n    os: linux\n")

	t.Run("custom_collides_with_builtin_skipped", func(t *testing.T) {
		builtinRes := mustLoad(t, builtinYAML)
		// Custom namespaced so its blueprint name and label differ, but host name still collides.
		customRes := mustLoadNS(t, customCollidingYAML, "team-x")

		loaded := []bpsource.Loaded{
			{Resolved: builtinRes, Provenance: bpsource.ProvBuiltin},
			{Resolved: customRes, Provenance: bpsource.ProvUpload},
		}
		accepted, builtinErr, skipped := foldValidated(loaded)
		if builtinErr != nil {
			t.Fatalf("builtinErr should be nil, got: %v", builtinErr)
		}
		if len(accepted) != 1 || accepted[0].Name != "bp-builtin" {
			t.Fatalf("accepted = %v, want [bp-builtin]", resolvedNames(accepted))
		}
		if len(skipped) != 1 {
			t.Fatalf("len(skipped) = %d, want 1; skipped=%v", len(skipped), skipped)
		}
		d := skipped[0]
		if d.Severity != "error" {
			t.Errorf("skipped[0].Severity = %q, want error", d.Severity)
		}
		if d.Source != "team-x/bp-custom" {
			t.Errorf("skipped[0].Source = %q, want team-x/bp-custom", d.Source)
		}
		if d.Stage != "validate" {
			t.Errorf("skipped[0].Stage = %q, want validate", d.Stage)
		}
		if d.Detail == "" {
			t.Error("skipped[0].Detail is empty, want collision message")
		}
	})

	t.Run("two_builtins_collide_fatal", func(t *testing.T) {
		b1 := mustLoad(t, builtinYAML)
		b2 := mustLoad(t, builtin2YAML)

		loaded := []bpsource.Loaded{
			{Resolved: b1, Provenance: bpsource.ProvBuiltin},
			{Resolved: b2, Provenance: bpsource.ProvBuiltin},
		}
		_, builtinErr, _ := foldValidated(loaded)
		if builtinErr == nil {
			t.Fatal("builtinErr should be non-nil for two colliding built-ins")
		}
	})

	t.Run("builtin_plus_noncolliding_custom_both_accepted", func(t *testing.T) {
		builtinRes := mustLoad(t, builtinYAML)
		okRes := mustLoadNS(t, customOKYAML, "team-y")

		loaded := []bpsource.Loaded{
			{Resolved: builtinRes, Provenance: bpsource.ProvBuiltin},
			{Resolved: okRes, Provenance: bpsource.ProvGit},
		}
		accepted, builtinErr, skipped := foldValidated(loaded)
		if builtinErr != nil {
			t.Fatalf("builtinErr should be nil, got: %v", builtinErr)
		}
		if len(skipped) != 0 {
			t.Fatalf("expected no skips, got: %v", skipped)
		}
		if len(accepted) != 2 {
			t.Fatalf("accepted = %v, want 2 blueprints", resolvedNames(accepted))
		}
	})
}

// resolvedNames extracts blueprint names for test error messages.
func resolvedNames(rs []*blueprint.Resolved) []string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	return names
}
