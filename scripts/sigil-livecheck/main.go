//go:build ignore

// sigil-livecheck — throwaway live-verify harness (NOT part of any build; run with go run).
// Exercises the REAL path: embedded corpus → sigil.AssembleConversation → sigil.Generation →
// internal/sink/sigil protojson mapping → live HTTP ingest. Creds from GC_SIGIL_* env.
//
//	GC_SIGIL_ENDPOINT=… GC_SIGIL_TENANT_ID=… GC_SIGIL_TOKEN=… go run scripts/sigil-livecheck/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
	nsigil "github.com/rknightion/synthkit/internal/sigil"
	sigilsink "github.com/rknightion/synthkit/internal/sink/sigil"
)

func gens(agent, archetype, sdk, provider, model string, seed uint64, n int, end time.Time) []nsigil.Generation {
	turns := nsigil.AssembleConversation(seed, archetype, n)
	conv := fmt.Sprintf("livecheck-%s-%d", agent, seed)
	out := make([]nsigil.Generation, 0, len(turns))
	step := 90 * time.Second
	for i, t := range turns {
		st := end.Add(-time.Duration(len(turns)-i) * step)
		g := nsigil.Generation{
			ID:               fmt.Sprintf("%s-gen-%d", conv, i),
			ConversationID:   conv,
			OperationName:    nsigil.OpGenerateText,
			Mode:             nsigil.ModeSync,
			TraceID:          ledger.NewTraceID(),
			SpanID:           ledger.NewSpanID(),
			Provider:         provider,
			Model:            model,
			ResponseModel:    model,
			SystemPrompt:     t.SystemPrompt,
			Input:            t.Input,
			Output:           t.Output,
			Tools:            t.Tools,
			Usage:            nsigil.Usage{Input: 12, Output: 80, Total: 92, CacheRead: int64(2000 * (i + 1)), CacheWrite: 1500},
			StopReason:       t.StopReason,
			StartedAt:        st,
			EndedAt:          st.Add(step / 2),
			AgentName:        agent,
			Metadata:         map[string]any{"sigil.sdk.name": sdk, "sigil.sdk.content_capture_mode": "full"},
			EffectiveVersion: nsigil.EffectiveVersion(t.SystemPrompt),
			Tags:             map[string]string{"livecheck": "true"},
		}
		out = append(out, g)
	}
	return out
}

func main() {
	ep, ten, tok := os.Getenv("GC_SIGIL_ENDPOINT"), os.Getenv("GC_SIGIL_TENANT_ID"), os.Getenv("GC_SIGIL_TOKEN")
	if ep == "" || ten == "" || tok == "" {
		log.Fatal("set GC_SIGIL_ENDPOINT/GC_SIGIL_TENANT_ID/GC_SIGIL_TOKEN")
	}
	sink, err := sigilsink.New(ep, ten, tok, false)
	if err != nil {
		log.Fatal(err)
	}
	now := time.Now()
	batches := []nsigil.Export{
		{ConvKey: "livecheck-claude-code-1", Generations: gens("synthkit-livecheck-claude-code", nsigil.ArchetypeCodingClaudeCode, "sdk-go", "anthropic", "claude-opus-4-8", 1, 5, now)},
		{ConvKey: "livecheck-general-1", Generations: gens("synthkit-livecheck-general", nsigil.ArchetypeGeneralOrchestration, "sdk-python", "openai", "gpt-4.1-nano", 2, 4, now)},
	}
	if err := sink.Write(context.Background(), batches); err != nil {
		log.Fatalf("write: %v", err)
	}
	inv := sink.Inventory()
	fmt.Printf("OK — posted generations=%d workflow_steps=%d scores=%d\n", inv.Generations, inv.WorkflowSteps, inv.Scores)
	fmt.Println("conversations: livecheck-claude-code-1, livecheck-general-1")
}
