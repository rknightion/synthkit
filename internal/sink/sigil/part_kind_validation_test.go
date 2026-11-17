// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	nativesigil "github.com/rknightion/synthkit/internal/sigil"
)

// TestMalformedPartKindRejected pins that the encoder refuses a Kind it would otherwise drop or
// ignore silently: thinking Kind with text content, and any Kind other than the zero value or the
// thinking kind. Rejection precedes the dry-run short circuit and any POST.
func TestMalformedPartKindRejected(t *testing.T) {
	cases := []struct {
		name string
		part nativesigil.Part
		want string
	}{
		{"thinking kind with text", nativesigil.Part{Kind: nativesigil.PartKindThinking, Text: "leaked"}, "text"},
		{"unknown kind", nativesigil.Part{Kind: "reasoning", Thinking: "x"}, "unknown"},
		{"unknown kind on empty part", nativesigil.Part{Kind: "text"}, "unknown"},
	}
	for _, dry := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var posts atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					posts.Add(1)
					w.WriteHeader(http.StatusAccepted)
				}))
				defer srv.Close()
				sink, err := New(srv.URL, "tenant", "test-token", dry)
				if err != nil {
					t.Fatal(err)
				}
				for _, side := range []string{"input", "output"} {
					msgs := []nativesigil.Message{{Role: "assistant", Parts: []nativesigil.Part{{Text: "ok"}, tc.part}}}
					gen := nativesigil.Generation{ID: "g"}
					if side == "input" {
						gen.Input = msgs
					} else {
						gen.Output = msgs
					}
					err = sink.Write(context.Background(), []nativesigil.Export{{Generations: []nativesigil.Generation{gen}}})
					if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Errorf("%s: want error containing %q, got %v", side, tc.want, err)
					}
				}
				if posts.Load() != 0 {
					t.Errorf("malformed part reached the wire (%d posts)", posts.Load())
				}
			})
		}
	}
}
