// SPDX-License-Identifier: AGPL-3.0-only

package optionallane

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func complete() Input {
	return Input{Requested: true, Declaration: GateSatisfied, Emitter: GateSatisfied, Verification: VerificationVerified}
}

func TestEvaluatePrecedenceAndGates(t *testing.T) {
	tests := []struct {
		name   string
		in     Input
		state  State
		reason Reason
	}{
		{"unsupported wins", Input{Requested: true, Unsupported: true, DryRun: true}, Unsupported, ReasonUnsupportedRuntime},
		{"not requested", Input{}, Disabled, ReasonIntentionallyDisabled},
		{"dry run", Input{Requested: true, DryRun: true}, Partial, ReasonDryRun},
		{"missing fields", Input{Requested: true, MissingFields: []Field{GCToken}}, Partial, ReasonMissingFields},
		{"declaration", Input{Requested: true, Declaration: GateMissing}, Partial, ReasonDeclarationMissing},
		{"emitter", Input{Requested: true, Declaration: GateSatisfied, Emitter: GateMissing}, Partial, ReasonEmitterMissing},
		{"verification pending", Input{Requested: true, Declaration: GateSatisfied, Emitter: GateSatisfied, Verification: VerificationNotAttempted}, Partial, ReasonVerificationNotAttempted},
		{"verification failed", Input{Requested: true, Declaration: GateSatisfied, Emitter: GateSatisfied, Verification: VerificationFailed}, Partial, ReasonVerificationFailed},
		{"enabled", complete(), Enabled, ReasonEnabled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(RUM, tt.in)
			if got.State != tt.state || got.Reason != tt.reason {
				t.Fatalf("got %s/%s, want %s/%s", got.State, got.Reason, tt.state, tt.reason)
			}
		})
	}
}

func TestMissingFieldsSortedDeduplicated(t *testing.T) {
	got := Evaluate(RUM, Input{Requested: true, MissingFields: []Field{GCToken, GCFaroAppKey, GCToken, GCFaroCollector}})
	want := []Field{GCFaroAppKey, GCFaroCollector, GCToken}
	if !reflect.DeepEqual(got.MissingFields, want) {
		t.Fatalf("fields=%v, want %v", got.MissingFields, want)
	}
}

func TestEvaluateAllRequiresExactKnownSetAndSorts(t *testing.T) {
	inputs := make([]Entry, 0, len(KnownLanes()))
	for _, lane := range KnownLanes() {
		inputs = append(inputs, Entry{Lane: lane, Input: Input{
			Declaration: GateNotRequired, Emitter: GateNotRequired, Verification: VerificationNotRequired,
		}})
	}
	got, err := EvaluateAll(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(KnownLanes()) {
		t.Fatalf("got %d results", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Lane >= got[i].Lane {
			t.Fatalf("not sorted: %v", got)
		}
	}
	if _, err := EvaluateAll(inputs[:len(inputs)-1]); err == nil {
		t.Fatal("missing lane accepted")
	}
	inputs[len(inputs)-1] = Entry{Lane: Lane("unknown")}
	if _, err := EvaluateAll(inputs); err == nil {
		t.Fatal("unknown lane accepted")
	}
	inputs[len(inputs)-1] = Entry{Lane: RUM}
	if _, err := EvaluateAll(inputs); err == nil {
		t.Fatal("duplicate lane accepted")
	}
}

func TestEvaluateAllRejectsOpenEnums(t *testing.T) {
	base := func() []Entry {
		entries := make([]Entry, 0, len(KnownLanes()))
		for _, lane := range KnownLanes() {
			entries = append(entries, Entry{Lane: lane, Input: Input{
				Declaration: GateNotRequired, Emitter: GateNotRequired, Verification: VerificationNotRequired,
			}})
		}
		return entries
	}
	tests := []struct {
		name string
		edit func(*Input)
	}{
		{"declaration", func(in *Input) { in.Declaration = Gate("secret") }},
		{"emitter", func(in *Input) { in.Emitter = Gate("secret") }},
		{"verification", func(in *Input) { in.Verification = Verification("secret") }},
		{"field", func(in *Input) { in.MissingFields = []Field{"SECRET_VALUE"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := base()
			tt.edit(&entries[0].Input)
			if _, err := EvaluateAll(entries); err == nil {
				t.Fatal("open enum accepted")
			}
		})
	}
}

func TestJSONHasNoSecretForms(t *testing.T) {
	got := Evaluate(Sigil, Input{Requested: true, MissingFields: []Field{GCSigilToken}})
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, secret := range []string{"raw-secret", "c2VjcmV0", "Basic c2VjcmV0", "Bearer secret", "<input value=", "\"token\":\""} {
		if strings.Contains(out, secret) {
			t.Fatalf("JSON contains secret form %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, string(GCSigilToken)) {
		t.Fatalf("missing safe field name: %s", out)
	}
}

func TestAllKnownLanes(t *testing.T) {
	if len(KnownLanes()) != 9 {
		t.Fatalf("known lanes=%d", len(KnownLanes()))
	}
}
