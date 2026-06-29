// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"fmt"
	"sort"
	"strings"
)

// protoLadder defines the canonical ordering of discovery protocols (LD-10 rank ladder).
// Protocols are always returned in this order after normalization.
var protoLadder = []string{
	ProtoLLDP,
	ProtoCDP,
	ProtoFDB,
	ProtoISIS,
	ProtoOSPF,
	ProtoBGP,
	ProtoMPLSTE,
}

// protoAllowed is the set of valid protocol names.
var protoAllowed = func() map[string]bool {
	m := make(map[string]bool, len(protoLadder))
	for _, p := range protoLadder {
		m[p] = true
	}
	return m
}()

// validFabricKinds is the set of valid fabric topology kinds.
var validFabricKinds = map[string]bool{
	"spine_leaf": true,
	"clos":       true,
	"linear":     true,
	"star":       true,
}

// resolveConfig validates cfg and applies defaults, producing the emit-ready resolvedConfig.
func resolveConfig(c *Config, _ string) (resolvedConfig, error) {
	// ── instance (required) ──────────────────────────────────────────────────
	if c.Instance == "" {
		return resolvedConfig{}, fmt.Errorf("nettopo: instance is required")
	}

	// ── job ──────────────────────────────────────────────────────────────────
	job := c.Job
	if job == "" {
		job = "integrations/network-topology-exporter"
	}

	// ── role ─────────────────────────────────────────────────────────────────
	role := c.Role
	if role == "" {
		role = RoleStandalone
	}
	switch role {
	case RoleStandalone, RoleHub, RoleSpoke:
		// valid
	default:
		return resolvedConfig{}, fmt.Errorf("nettopo: invalid role %q: must be one of standalone|hub|spoke", role)
	}
	if role == RoleSpoke && c.SpokeID == "" {
		return resolvedConfig{}, fmt.Errorf("nettopo: role=spoke requires spoke_id")
	}

	// ── federation spokes (hub mode) ─────────────────────────────────────────
	var spokes []string
	if role == RoleHub && c.Federation != nil {
		spokes = c.Federation.Spokes
	}

	// ── protocols ────────────────────────────────────────────────────────────
	rawProtos := c.Protocols
	if len(rawProtos) == 0 {
		rawProtos = []string{ProtoLLDP, ProtoBGP}
	}
	// Normalize to lowercase, validate, dedupe.
	seen := make(map[string]bool)
	for _, p := range rawProtos {
		pn := strings.ToLower(p)
		if !protoAllowed[pn] {
			return resolvedConfig{}, fmt.Errorf("nettopo: unknown protocol %q", p)
		}
		seen[pn] = true
	}
	// Emit in ladder order.
	protocols := make([]string, 0, len(seen))
	for _, p := range protoLadder {
		if seen[p] {
			protocols = append(protocols, p)
		}
	}
	protoSet := make(map[string]bool, len(protocols))
	for _, p := range protocols {
		protoSet[p] = true
	}

	// ── out-of-scope count (clamp ≥ 0) ───────────────────────────────────────
	oosCount := c.OutOfScopeNeighbours
	if oosCount < 0 {
		oosCount = 0
	}

	// ── fabric validation ─────────────────────────────────────────────────────
	if c.Fabric != nil {
		kind := c.Fabric.Kind
		if !validFabricKinds[kind] {
			return resolvedConfig{}, fmt.Errorf("nettopo: invalid fabric kind %q: must be one of spine_leaf|clos|linear|star", kind)
		}
		switch kind {
		case "spine_leaf", "clos":
			if c.Fabric.Spines < 1 || c.Fabric.Leaves < 1 {
				return resolvedConfig{}, fmt.Errorf("nettopo: fabric kind %q requires spines >= 1 and leaves >= 1", kind)
			}
		}
	}

	// ── sort devices/links for stable output ──────────────────────────────────
	devices := make([]DeviceConfig, len(c.Devices))
	copy(devices, c.Devices)
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })

	links := make([]LinkConfig, len(c.Links))
	copy(links, c.Links)

	return resolvedConfig{
		instance:    c.Instance,
		job:         job,
		role:        role,
		spokeID:     c.SpokeID,
		protocols:   protocols,
		protoSet:    protoSet,
		sessionPool: c.SessionPool,
		oosCount:    oosCount,
		otlpOutput:  c.OTLPOutput,
		spokes:      spokes,
		fabric:      c.Fabric,
		devices:     devices,
		links:       links,
	}, nil
}
