// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import "testing"

import "github.com/rknightion/synthkit/internal/ledger"

func TestHopStampers_KindsRegistered(t *testing.T) {
	for _, k := range []string{"db", "cache", "service"} {
		if _, ok := hopStampers[k]; !ok {
			t.Fatalf("no stamper registered for kind %q", k)
		}
	}
}

func TestServiceStamper_EmitsPeerResource(t *testing.T) {
	w := &Workload{name: "api", namespace: "api", cluster: "c1", env: "prod"}
	s := hopStampers["service"]
	attrs, emit := s.peerResource(w, ledger.Call{Kind: "service", Target: "payments"})
	if !emit {
		t.Fatal("service hop must emit a callee SERVER resource")
	}
	if attrs["service.name"] != "payments" {
		t.Fatalf("callee service.name=%v, want payments", attrs["service.name"])
	}
}

func TestDBStamper_NoPeerResource(t *testing.T) {
	w := &Workload{name: "api"}
	if _, emit := hopStampers["db"].peerResource(w, ledger.Call{Kind: "db", Target: "app-db"}); emit {
		t.Fatal("db hop must not emit a peer resource")
	}
}
