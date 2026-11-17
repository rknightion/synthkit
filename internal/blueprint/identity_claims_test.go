// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
)

// fakeIdentityCfg is a construct config that declares substrate identity of its own, standing in
// for any real kind so the loader test stays decoupled from every construct package.
type fakeIdentityCfg struct {
	Claims []string `yaml:"claims"`
}

func (c *fakeIdentityCfg) IdentityClaims() [][2]string {
	out := make([][2]string, 0, len(c.Claims))
	for _, v := range c.Claims {
		out = append(out, [2]string{"fake identity", v})
	}
	return out
}

func loadFakeIdentity(t *testing.T, name string, claims ...string) *Resolved {
	t.Helper()
	reg := core.NewRegistry()
	reg.RegisterConstruct(core.ConstructReg{
		Kind:      "fake_identity",
		Doc:       "test construct declaring substrate identity",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration,
		NewConfig: func() any { return &fakeIdentityCfg{} },
		Build:     func(any, *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	y := "name: " + name + "\nenvironments: [{name: fixture}]\nintegrations:\n  fake_identity:\n    enabled: true\n    claims: [" + strings.Join(claims, ", ") + "]\n"
	r, err := Load([]byte(y), reg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

// A construct config implementing the identity hook claims through the same ValidateSet path as
// fixtures: two blueprints declaring one identity are rejected, distinct ones load.
func TestValidateSetRejectsDeclaredIdentityCollision(t *testing.T) {
	a := loadFakeIdentity(t, "bp-a", "id-1", "id-2")

	same := loadFakeIdentity(t, "bp-b", "id-2")
	if err := ValidateSet([]*Resolved{a, same}); err == nil || !strings.Contains(err.Error(), "id-2") || !strings.Contains(err.Error(), "bp-a") {
		t.Fatalf("shared declared identity not rejected: %v", err)
	}

	distinct := loadFakeIdentity(t, "bp-c", "id-3")
	if err := ValidateSet([]*Resolved{a, distinct}); err != nil {
		t.Fatalf("distinct declared identities must load together: %v", err)
	}
}

func TestDeclaredIdentityDeduplicatesWithinBlueprint(t *testing.T) {
	a := loadFakeIdentity(t, "bp-a", "id-1", "id-1")
	if err := ValidateSet([]*Resolved{a}); err != nil {
		t.Fatalf("a blueprint must not collide with itself: %v", err)
	}
}
