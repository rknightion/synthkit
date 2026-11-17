// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sigil"
	sigilsink "github.com/rknightion/synthkit/internal/sink/sigil"
)

const pipelineThinkingSentinel = "private-reasoning-token"

// withThinkingCorpus makes every assistant corpus turn carry a thinking part.
// No embedded archetype has one, and the pipeline reads thinking only from its
// corpus, so this is the only way to drive that shape through the real Build,
// capture and sink path.
func withThinkingCorpus(t *testing.T) {
	t.Helper()
	old := loadPipelineCorpus
	loadPipelineCorpus = func(archetype string) ([]sigil.CorpusRecord, error) {
		records, err := old(archetype)
		if err != nil {
			return nil, err
		}
		out := pipelineClone(records)
		for i := range out {
			if out[i].Kind == "turn" && out[i].Role == "assistant" {
				out[i].Parts = append(out[i].Parts, sigil.CorpusPart{Thinking: pipelineThinkingSentinel})
			}
		}
		return out, nil
	}
	t.Cleanup(func() { loadPipelineCorpus = old })
}

type thinkingWire struct {
	thinking []map[string]any // every part carrying a thinking payload, input and output
	usage    map[string]any   // generation id to decoded usage
	body     string
}

// runThinkingPipeline drives the app pipeline through the real HTTP+protojson
// sigil sink and decodes the wire body part by part.
func runThinkingPipeline(t *testing.T, mode string) thinkingWire {
	t.Helper()
	var (
		mu     sync.Mutex
		bodies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/api/v1/generations:export" {
			mu.Lock()
			bodies = append(bodies, string(b))
			mu.Unlock()
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	sink, err := sigilsink.New(srv.URL, "test", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	c := pipelineConfig(t)
	c.AgentPipeline.Ratings.SampleRate = 0
	for i := range c.Services {
		c.Services[i].AgentObservability.CaptureMode = mode
	}
	raw, err := Registration().Build(c, pipelineBinding(c, &pipelineStages{}, nil))
	if err != nil {
		t.Fatalf("%s: %v", mode, err)
	}
	world := &core.World{Sigil: sink, Traces: &coretest.TraceCapture{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := range 2 {
		r := pipelineRequest(200 + i)
		if err = raw.ProjectBatch(ctx, r.Start, world, []*ledger.Request{r}); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	out := thinkingWire{usage: map[string]any{}}
	mu.Lock()
	defer mu.Unlock()
	for _, body := range bodies {
		out.body += body
		var req struct {
			Generations []struct {
				ID    string         `json:"id"`
				Usage map[string]any `json:"usage"`
				In    []struct {
					Parts []map[string]any `json:"parts"`
				} `json:"input"`
				Out []struct {
					Parts []map[string]any `json:"parts"`
				} `json:"output"`
			} `json:"generations"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		for _, g := range req.Generations {
			out.usage[g.ID] = g.Usage
			for _, m := range g.In {
				for _, p := range m.Parts {
					if _, ok := p["thinking"]; ok {
						out.thinking = append(out.thinking, p)
					}
				}
			}
			for _, m := range g.Out {
				for _, p := range m.Parts {
					if _, ok := p["thinking"]; ok {
						out.thinking = append(out.thinking, p)
					}
				}
			}
		}
	}
	return out
}

// TestAgentPipelineMetadataOnlyThinkingWire pins that metadata_only capture of a
// thinking part reaches the wire as a thinking payload with empty content (not a
// relabelled text part, no placeholder and no retained reasoning), while every
// other capture mode keeps the full thinking content and the JSON-derived usage
// is identical across modes.
func TestAgentPipelineMetadataOnlyThinkingWire(t *testing.T) {
	withThinkingCorpus(t)
	modes := []string{"", sigil.CaptureNoToolContent, sigil.CaptureFull, sigil.CaptureFullWithMetadataSpans, sigil.CaptureMetadataOnly}
	var reference thinkingWire
	for _, mode := range modes {
		name := mode
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			got := runThinkingPipeline(t, mode)
			if len(got.thinking) == 0 {
				t.Fatal("no thinking part reached the wire")
			}
			want := map[string]any{"thinking": pipelineThinkingSentinel, "metadata": map[string]any{"provider_type": "thinking"}}
			if mode == sigil.CaptureMetadataOnly {
				want["thinking"] = ""
				if strings.Contains(got.body, pipelineThinkingSentinel) {
					t.Error("metadata_only retained reasoning content")
				}
			}
			for i, p := range got.thinking {
				if !reflect.DeepEqual(p, want) {
					t.Errorf("thinking part %d\ngot  %v\nwant %v", i, p, want)
				}
			}
			if mode == "" {
				reference = got
				return
			}
			if !reflect.DeepEqual(got.usage, reference.usage) || len(got.usage) == 0 {
				t.Errorf("usage differs from default capture\ngot  %v\nwant %v", got.usage, reference.usage)
			}
		})
	}
}
