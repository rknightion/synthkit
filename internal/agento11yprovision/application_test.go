// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPreparedPrivateCodecs(t *testing.T) {
	f := newEngineTLS(t, false)
	prepared, e := Prepare(f.input)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(bindingToWire(f.input.Binding))
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodeBinding(raw)
	if e != nil || got != f.input.Binding {
		t.Fatal("private existing-wire Binding decode")
	}
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(engineContractRevision), []byte("arbitrary-contract"), 1),
		bytes.Replace(raw, []byte(engineSourceRevision), []byte("prior-source"), 1),
		bytes.Replace(raw, []byte(`"state_id":`), []byte(`"State_ID":`), 1),
		append(append([]byte{}, raw...), []byte(`{}`)...),
		[]byte(`null`), []byte(strings.Repeat(" ", 65537)),
	} {
		if _, e := DecodeBinding(bad); e == nil {
			t.Fatal("inexact/prior Binding accepted")
		}
	}
	pv, e := PreviewPrepared(context.Background(), f.client, f.store, prepared, ModeApply)
	if e != nil {
		t.Fatal(e)
	}
	restored, e := DecodePreparedPreview(prepared, pv.JSON())
	if e != nil || restored.Digest() != pv.Digest() {
		t.Fatal("private preview did not rehydrate exact existing bytes")
	}
	for _, bad := range [][]byte{
		append([]byte(" "), pv.JSON()...),
		bytes.Replace(pv.JSON(), []byte(`"mode":"apply"`), []byte(`"mode":"teardown"`), 1),
		bytes.Replace(pv.JSON(), []byte(`"namespaces":[`), []byte(`"namespaces":["unapproved",`), 1),
	} {
		if _, e := DecodePreparedPreview(prepared, bad); e == nil {
			t.Fatal("preview evidence changed without refusal")
		}
	}
	if _, e := PreviewPrepared(context.Background(), nil, nil, PreparedPlan{}, ModeApply); e == nil {
		t.Fatal("zero PreparedPlan usable")
	}
	if _, e := ApplyPrepared(context.Background(), nil, nil, PreparedPlan{}, Preview{}, Approval{}); e == nil {
		t.Fatal("zero apply usable")
	}
	if _, e := RecoverPrepared(context.Background(), nil, nil, PreparedPlan{}, Preview{}, Approval{}); e == nil {
		t.Fatal("zero recover usable")
	}
	if _, e := CleanupPrepared(context.Background(), nil, nil, PreparedPlan{}, Preview{}, Approval{}); e == nil {
		t.Fatal("zero cleanup usable")
	}
}
