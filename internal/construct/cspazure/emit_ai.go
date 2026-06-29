// SPDX-License-Identifier: AGPL-3.0-only

// emit_ai.go — azure_microsoft_cognitiveservices_accounts_* (Azure OpenAI / Cognitive Services)
//
// Sub-signal: "ai"  (OPT-IN — NOT in the default/all set; must be explicit in sub_signals).
// Resource type: microsoft.cognitiveservices/accounts
// Metric source: Azure Monitor Microsoft.CognitiveServices/accounts metrics table
//
//	(https://github.com/microsoftdocs/azure-monitor-docs/blob/main/articles/azure-monitor/
//	 reference/supported-metrics/microsoft-cognitiveservices-accounts-metrics.md)
//	Sourced via ctx7 /microsoftdocs/azure-monitor-docs, 2026-06-15.
//
// ALL metrics use st.Set — window-gauge invariant (ARCHITECTURE I5 / extract §1.3).
//
// # Env-awareness
//
// When fx.Env is set (non-nil), the `env` label is stamped on every series and the
// shape factor scales by Env.Weight / Env.NonProd. When fx.Env is nil the `env` label
// is OMITTED (I13 — absent, never "") and the existing fixed factor path is used
// (Shape.Factor(now, 1.0, false) — byte-for-byte identical to the other sub-signals).
package cspazure

import (
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/genai"
)

// emitAI emits the ai sub-signal (Cognitive Services / Azure OpenAI) for one subscription.
// resourceGroup rg-ai sits at index 5; we derive it deterministically from the subscription.
func (c *construct) emitAI(now time.Time, w *core.World, sub azureSub, bf float64) {
	// Cognitive Services accounts live in a dedicated resource group.
	// We use a synthetic rg-ai name derived from the subscription (no new fixture field needed).
	rg := "rg-ai"

	// One OpenAI account per subscription.
	subSuffix := sub.subscriptionName[len(sub.subscriptionName)-2:]
	accountName := "oai-" + subSuffix + "-01"

	base := c.baseLabelsFor(sub, rg, "microsoft.cognitiveservices/accounts", accountName)

	// Optionally stamp env label.
	lbls := base
	var envBf float64
	if c.fx.Env != nil {
		// Env-scoped: scale by Env weight and stamp env label.
		envBf = w.Shape.Factor(now, c.fx.Env.Weight, c.fx.Env.NonProd)
		if envBf < 0 {
			envBf = 0
		}
		// Clone-before-stamp: never mutate the base map (may be shared).
		lbls = make(map[string]string, len(base)+1)
		for k, v := range base {
			lbls[k] = v
		}
		lbls["env"] = c.fx.Env.Name
	} else {
		// Aggregate path: use the caller-supplied bf (Shape.Factor(now,1.0,false)).
		envBf = bf
	}

	// ── Account-level HTTP call metrics (no per-deployment dimension) ─────────
	// REST API names: TotalCalls, SuccessfulCalls, BlockedCalls, TotalErrors,
	// ClientErrors, ServerErrors, TotalTokenCalls.
	// Source: Azure Monitor Microsoft.CognitiveServices/accounts metrics table;
	// ctx7 /microsoftdocs/azure-monitor-docs, 2026-06-15. v: ok (confirmed REST API name).

	acctN := w.Shape.Noise(0.12)
	totalCalls := rnd(1_000 * envBf * acctN)
	c.st.Set("azure_microsoft_cognitiveservices_accounts_total_calls_total_count",
		lbls, totalCalls)
	c.st.Set("azure_microsoft_cognitiveservices_accounts_successful_calls_total_count",
		lbls, rnd(totalCalls*0.97))
	c.st.Set("azure_microsoft_cognitiveservices_accounts_blocked_calls_total_count",
		lbls, rnd(10*envBf))
	c.st.Set("azure_microsoft_cognitiveservices_accounts_total_errors_total_count",
		lbls, rnd(30*envBf))
	c.st.Set("azure_microsoft_cognitiveservices_accounts_client_errors_total_count",
		lbls, rnd(20*envBf))
	c.st.Set("azure_microsoft_cognitiveservices_accounts_server_errors_total_count",
		lbls, rnd(10*envBf))
	c.st.Set("azure_microsoft_cognitiveservices_accounts_total_token_calls_total_count",
		lbls, rnd(900*envBf*acctN))

	// ── Per-deployment token metrics ──────────────────────────────────────────
	// REST API names: ProcessedPromptTokens, GeneratedCompletionTokens.
	// Source: Azure Monitor Microsoft.CognitiveServices/accounts metrics table;
	// ctx7 /microsoftdocs/azure-monitor-docs, 2026-06-15.
	// v: ok (confirmed by "Number of prompt tokens processed (input) on an OpenAI model"
	// description + "Number of tokens generated (output)" descriptions in the source docs).
	// Dimensions: ModelDeploymentName, ModelName (account-level resource).
	// Deployment set covers the realistic Azure OpenAI model mix (chat + embedding).
	for _, deploy := range []struct{ name, model string }{
		{"gpt4o-deploy", "gpt-4o"},
		{"gpt4o-mini-deploy", "gpt-4o-mini"},
		{"gpt41-mini-deploy", "gpt-4.1-mini"},
		{"o3-deploy", "o3"},
		{"embed-small-deploy", "text-embedding-3-small"},
	} {
		// Per-deployment noise + wander drawn INSIDE the loop so each model moves independently.
		mw := genai.VolumeWeight(deploy.model)
		wob := w.Shape.Wander(deploy.model, now, 0.15)
		n := w.Shape.Noise(0.10)
		scale := envBf * mw * wob * n

		deployLbls := mergeLabels(lbls, c.dim(map[string]string{
			"ModelDeploymentName": deploy.name,
			"ModelName":           deploy.model,
		}))
		promptTokens := rnd(50_000 * scale)
		c.st.Set("azure_microsoft_cognitiveservices_accounts_processed_prompt_tokens_total_count",
			deployLbls, promptTokens)
		// Embedding models produce ~0 completion tokens in reality (output is a vector, not text).
		// Gate completion-token emission to non-embedding models.
		var completionTokens float64
		if !strings.HasPrefix(deploy.model, "text-embedding-") {
			completionTokens = rnd(15_000 * scale)
		}
		c.st.Set("azure_microsoft_cognitiveservices_accounts_generated_completion_tokens_total_count",
			deployLbls, completionTokens)
		// TokensPerSecond: generation speed (average aggregation).
		// REST API name: TokensPerSecond. v: ok (confirmed in ctx7 docs).
		c.st.Set("azure_microsoft_cognitiveservices_accounts_tokens_per_second_average_count",
			deployLbls, rnd(80*scale))
		// FineTuningTrainingHours: emitted only for fine-tuned models.
		// REST API name: ProcessedFineTunedTrainingHours. v: assumed (description confirmed
		// "Number of Training Hours Processed on an OpenAI FineTuned Model" but the REST
		// API name fragment "FineTuned" vs "FineTuning" is not byte-confirmed from the table).
		// Added to cantfind.md as SK-45 pending live capture.
		c.st.Set("azure_microsoft_cognitiveservices_accounts_processed_fine_tuned_training_hours_total_count",
			deployLbls, rnd(2*envBf))
	}
}
