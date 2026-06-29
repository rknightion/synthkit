// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"testing"
)

func TestResolveConfig_MissingInstance(t *testing.T) {
	_, err := resolveConfig(&Config{}, "seed")
	if err == nil {
		t.Fatal("expected error for missing instance, got nil")
	}
	const want = "nettopo: instance is required"
	if err.Error() != want {
		t.Fatalf("error = %q; want %q", err.Error(), want)
	}
}

func TestResolveConfig_DefaultJob(t *testing.T) {
	rc, err := resolveConfig(&Config{Instance: "host:9100"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	const want = "integrations/network-topology-exporter"
	if rc.job != want {
		t.Fatalf("job = %q; want %q", rc.job, want)
	}
}

func TestResolveConfig_DefaultRole(t *testing.T) {
	rc, err := resolveConfig(&Config{Instance: "host:9100"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if rc.role != RoleStandalone {
		t.Fatalf("role = %q; want %q", rc.role, RoleStandalone)
	}
}

func TestResolveConfig_BadRole(t *testing.T) {
	_, err := resolveConfig(&Config{Instance: "host:9100", Role: "master"}, "seed")
	if err == nil {
		t.Fatal("expected error for bad role, got nil")
	}
}

func TestResolveConfig_SpokeRequiresSpokeID(t *testing.T) {
	_, err := resolveConfig(&Config{Instance: "host:9100", Role: RoleSpoke}, "seed")
	if err == nil {
		t.Fatal("expected error for spoke without spoke_id, got nil")
	}
}

func TestResolveConfig_SpokeWithIDOK(t *testing.T) {
	rc, err := resolveConfig(&Config{Instance: "host:9100", Role: RoleSpoke, SpokeID: "s1"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if rc.spokeID != "s1" {
		t.Fatalf("spokeID = %q; want %q", rc.spokeID, "s1")
	}
}

func TestResolveConfig_DefaultProtocols(t *testing.T) {
	rc, err := resolveConfig(&Config{Instance: "host:9100"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ProtoLLDP, ProtoBGP}
	if len(rc.protocols) != len(want) {
		t.Fatalf("protocols = %v; want %v", rc.protocols, want)
	}
	for i, p := range want {
		if rc.protocols[i] != p {
			t.Fatalf("protocols[%d] = %q; want %q", i, rc.protocols[i], p)
		}
	}
	if !rc.protoSet[ProtoLLDP] || !rc.protoSet[ProtoBGP] {
		t.Fatalf("protoSet missing expected entries: %v", rc.protoSet)
	}
}

func TestResolveConfig_ProtocolsNormalizedDeduped(t *testing.T) {
	rc, err := resolveConfig(&Config{
		Instance:  "host:9100",
		Protocols: []string{"OSPF", "lldp", "OSPF", "BGP", "lldp"},
	}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	// Ladder order: lldp, ospf, bgp — deduplicated
	want := []string{ProtoLLDP, ProtoOSPF, ProtoBGP}
	if len(rc.protocols) != len(want) {
		t.Fatalf("protocols = %v; want %v", rc.protocols, want)
	}
	for i, p := range want {
		if rc.protocols[i] != p {
			t.Fatalf("protocols[%d] = %q; want %q", i, rc.protocols[i], p)
		}
	}
}

func TestResolveConfig_LadderOrder(t *testing.T) {
	// All protocols out of order — should come back in full ladder order.
	rc, err := resolveConfig(&Config{
		Instance:  "host:9100",
		Protocols: []string{"mpls_te", "bgp", "isis", "ospf", "fdb", "cdp", "lldp"},
	}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ProtoLLDP, ProtoCDP, ProtoFDB, ProtoISIS, ProtoOSPF, ProtoBGP, ProtoMPLSTE}
	if len(rc.protocols) != len(want) {
		t.Fatalf("protocols len = %d; want %d: %v", len(rc.protocols), len(want), rc.protocols)
	}
	for i, p := range want {
		if rc.protocols[i] != p {
			t.Fatalf("protocols[%d] = %q; want %q", i, rc.protocols[i], p)
		}
	}
}

func TestResolveConfig_UnknownProtocol(t *testing.T) {
	_, err := resolveConfig(&Config{
		Instance:  "host:9100",
		Protocols: []string{"lldp", "netflow"},
	}, "seed")
	if err == nil {
		t.Fatal("expected error for unknown protocol, got nil")
	}
}

func TestResolveConfig_HubRole(t *testing.T) {
	rc, err := resolveConfig(&Config{
		Instance: "host:9100",
		Role:     RoleHub,
		Federation: &FederationConfig{
			Spokes: []string{"spoke1", "spoke2"},
		},
	}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if rc.role != RoleHub {
		t.Fatalf("role = %q; want %q", rc.role, RoleHub)
	}
	if len(rc.spokes) != 2 {
		t.Fatalf("spokes = %v; want [spoke1 spoke2]", rc.spokes)
	}
}

func TestResolveConfig_HubNoFederation(t *testing.T) {
	rc, err := resolveConfig(&Config{
		Instance: "host:9100",
		Role:     RoleHub,
	}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	// hub without federation is valid; spokes is empty
	if len(rc.spokes) != 0 {
		t.Fatalf("expected no spokes for hub without federation, got %v", rc.spokes)
	}
}

func TestResolveConfig_OOSCountClampedNonNegative(t *testing.T) {
	rc, err := resolveConfig(&Config{
		Instance:             "host:9100",
		OutOfScopeNeighbours: -5,
	}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if rc.oosCount != 0 {
		t.Fatalf("oosCount = %d; want 0 (clamped)", rc.oosCount)
	}
}

func TestResolveConfig_FabricValidation_SpineLeavesRequired(t *testing.T) {
	_, err := resolveConfig(&Config{
		Instance: "host:9100",
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 0,
			Leaves: 0,
		},
	}, "seed")
	if err == nil {
		t.Fatal("expected error for spine_leaf with spines=0, got nil")
	}
}

func TestResolveConfig_FabricValidation_BadKind(t *testing.T) {
	_, err := resolveConfig(&Config{
		Instance: "host:9100",
		Fabric: &FabricConfig{
			Kind: "ring",
		},
	}, "seed")
	if err == nil {
		t.Fatal("expected error for unknown fabric kind 'ring', got nil")
	}
}

func TestResolveConfig_FabricValidation_LinearOK(t *testing.T) {
	// linear does not require spines/leaves > 1 (uses Leaves as count, min 2 is applied during graph gen)
	rc, err := resolveConfig(&Config{
		Instance: "host:9100",
		Fabric: &FabricConfig{
			Kind:   "linear",
			Leaves: 3,
		},
	}, "seed")
	if err != nil {
		t.Fatalf("unexpected error for linear fabric: %v", err)
	}
	if rc.fabric == nil {
		t.Fatal("fabric should be retained in resolvedConfig")
	}
}

func TestResolveConfig_CarriesGatedFields(t *testing.T) {
	rc, err := resolveConfig(&Config{
		Instance:    "host:9100",
		SessionPool: true,
		OTLPOutput:  true,
	}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if !rc.sessionPool {
		t.Fatal("sessionPool should be true")
	}
	if !rc.otlpOutput {
		t.Fatal("otlpOutput should be true")
	}
}
