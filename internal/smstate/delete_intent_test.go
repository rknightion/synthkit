// SPDX-License-Identifier: AGPL-3.0-only
package smstate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeleteIntentRoundTripsThroughTheGenericPendingJournal(t *testing.T) {
	intent := DeleteIntent{SchemaVersion: SchemaVersion, TargetFingerprint: "target", Kind: "check", Key: CheckKey("job", "https://example.test"), ID: 7, Modified: ConfigVersion(12.5), SpecHash: "spec"}
	journal, err := NewDeleteJournal(intent, "snapshot", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateJournal(journal); err != nil || journal.Operation != DeleteOperation {
		t.Fatalf("journal = %+v, %v", journal, err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJournal(dir, journal); err != nil {
		t.Fatal(err)
	}
	read, err := ReadJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DeleteIntentFromJournal(read)
	if err != nil || got != intent {
		t.Fatalf("intent = %+v, %v", got, err)
	}
	if strings.Contains(string(read.ExpectedSpec), "token") {
		t.Fatal("intent carries a credential field")
	}
}

func TestDeleteIntentRejectsOtherOperationsAndTampering(t *testing.T) {
	intent := DeleteIntent{SchemaVersion: SchemaVersion, TargetFingerprint: "target", Kind: "probe", Key: ProbeKey("p", "EMEA"), ID: 3, Modified: ConfigVersion(4), SpecHash: "spec"}
	good, err := NewDeleteJournal(intent, "snapshot", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	rehash := func(j Journal, spec string) Journal {
		j.ExpectedSpec = json.RawMessage(spec)
		j.ExpectedHash = SpecHash(j.ExpectedSpec)
		return j
	}
	for name, journal := range map[string]Journal{
		"create operation":  func() Journal { j := good; j.Operation = "create"; return j }(),
		"kind disagreement": func() Journal { j := good; j.Kind = "check"; return j }(),
		"key disagreement":  func() Journal { j := good; j.Key = "other"; return j }(),
		"unknown field":     rehash(good, `{"schema_version":1,"target_fingerprint":"target","kind":"probe","key":"p\u0000EMEA","id":3,"modified":"4000000000","spec_hash":"spec","name":"x"}`),
		"zero identity":     rehash(good, `{"schema_version":1,"target_fingerprint":"target","kind":"probe","key":"p\u0000EMEA","id":0,"modified":"4000000000","spec_hash":"spec"}`),
		"unbound target":    rehash(good, `{"schema_version":1,"target_fingerprint":"","kind":"probe","key":"p\u0000EMEA","id":3,"modified":"4000000000","spec_hash":"spec"}`),
		"hash mismatch":     func() Journal { j := good; j.ExpectedHash = SpecHash("other"); return j }(),
	} {
		if _, err := DeleteIntentFromJournal(journal); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for name, bad := range map[string]DeleteIntent{
		"no revision": {SchemaVersion: SchemaVersion, TargetFingerprint: "t", Kind: "check", Key: "k", ID: 1, Modified: "0", SpecHash: "s"},
		"no spec":     {SchemaVersion: SchemaVersion, TargetFingerprint: "t", Kind: "check", Key: "k", ID: 1, Modified: "1"},
		"bad kind":    {SchemaVersion: SchemaVersion, TargetFingerprint: "t", Kind: "folder", Key: "k", ID: 1, Modified: "1", SpecHash: "s"},
	} {
		if _, err := NewDeleteJournal(bad, "snapshot", time.Unix(1, 0)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
