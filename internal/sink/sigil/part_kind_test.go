// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	nativesigil "github.com/rknightion/synthkit/internal/sigil"
)

// TestPartKindOnTheWire posts real generations through the HTTP+protojson sink and compares the
// decoded request body part by part. It pins that an explicit thinking kind survives empty
// content (no placeholder, no retyping to text) while every other variant, and the legacy
// inference for untyped parts, encodes exactly as before.
func TestPartKindOnTheWire(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		if body, err = io.ReadAll(r.Body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	sink, err := New(srv.URL, "tenant", "test-token", false)
	if err != nil {
		t.Fatal(err)
	}
	parts := []nativesigil.Part{
		{Kind: nativesigil.PartKindThinking, ProviderType: "thinking"},
		{Kind: nativesigil.PartKindThinking},
		{Thinking: "reasoning", ProviderType: "thinking"},
		{Kind: nativesigil.PartKindThinking, Thinking: "explicit"},
		{ProviderType: "thinking"},
		{},
		{Text: "answer", ProviderType: "text"},
		{ProviderType: "tool_call", ToolCall: &nativesigil.ToolCall{ID: "c", Name: "t", InputJSON: []byte(`{}`)}},
		{ToolResult: &nativesigil.ToolResult{ToolCallID: "c", Name: "t"}},
		{Media: &nativesigil.Media{Kind: "image", MIMEType: "image/png", Name: "n"}},
	}
	gen := nativesigil.Generation{ID: "g", Output: []nativesigil.Message{{Role: "MESSAGE_ROLE_ASSISTANT", Parts: parts}}}
	if err := sink.Write(context.Background(), []nativesigil.Export{{Generations: []nativesigil.Generation{gen}}}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Generations []struct {
			Output []struct {
				Parts []map[string]any `json:"parts"`
			} `json:"output"`
		} `json:"generations"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	want := []string{
		`{"thinking":"","metadata":{"provider_type":"thinking"}}`,
		`{"thinking":""}`,
		`{"thinking":"reasoning","metadata":{"provider_type":"thinking"}}`,
		`{"thinking":"explicit"}`,
		`{"text":"","metadata":{"provider_type":"thinking"}}`,
		`{"text":""}`,
		`{"text":"answer","metadata":{"provider_type":"text"}}`,
		`{"tool_call":{"id":"c","name":"t","input_json":"e30="},"metadata":{"provider_type":"tool_call"}}`,
		`{"tool_result":{"tool_call_id":"c","name":"t"}}`,
		`{"media":{"kind":"image","mime_type":"image/png","name":"n"}}`,
	}
	if len(got.Generations) != 1 || len(got.Generations[0].Output) != 1 || len(got.Generations[0].Output[0].Parts) != len(want) {
		t.Fatalf("unexpected payload shape: %s", body)
	}
	for i, text := range want {
		var w map[string]any
		if err := json.Unmarshal([]byte(text), &w); err != nil {
			t.Fatal(err)
		}
		if g := got.Generations[0].Output[0].Parts[i]; !reflect.DeepEqual(g, w) {
			enc, _ := json.Marshal(g)
			t.Errorf("part %d\ngot  %s\nwant %s", i, enc, text)
		}
	}
}
