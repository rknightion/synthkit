// SPDX-License-Identifier: AGPL-3.0-only

package bedrock

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/state"
)

// ApplicationProfile is an agent's declared application inference profile, not a Bedrock
// Agent. Agent/Provider/Model are config associations only, never CloudWatch dimensions.
// Provider + Model must also be declared on the generation workload for backend pricing;
// this construct does not emit generations or rewrite their identity.
type ApplicationProfile struct {
	// ID is a fictional opaque alphanumeric profile ID, not an ARN or foundation model ID.
	ID string `yaml:"id"`
	// Agent names the owning agent in the blueprint. It is not a wire label or a tag readback.
	Agent string `yaml:"agent"`
	// Provider is bedrock, matching the generation provider used for pricing.
	Provider string `yaml:"provider"`
	// Model retains the foundation model identity separately from the profile ID.
	Model string `yaml:"model"`
	// RequestsPerMin is explicit constant traffic, with a real-world basis beside its YAML value.
	// Fractional rates produce whole observations in UTC minute windows, including idle minutes.
	RequestsPerMin float64 `yaml:"requests_per_min"`
	// InputTokensPerRequest is a synthetic constant request size, not a captured production mean.
	InputTokensPerRequest int `yaml:"input_tokens_per_request"`
	// OutputTokensPerRequest is a synthetic constant response size.
	OutputTokensPerRequest int `yaml:"output_tokens_per_request"`
	// LatencyMSPerRequest is synthetic constant per-request latency in milliseconds.
	LatencyMSPerRequest float64 `yaml:"latency_ms_per_request"`
	// EstimatedTPMQuotaUsagePerRequest is an explicit synthetic quota estimate; no formula
	// relating it to token usage, maxTokens or pricing is inferred from the tiny capture.
	EstimatedTPMQuotaUsagePerRequest int `yaml:"estimated_tpm_quota_usage_per_request"`
}

var profileIDPattern = regexp.MustCompile(`^[A-Za-z0-9]+$`)

func validateProfiles(cfg *Config, cloud *fixture.Cloud) error {
	if len(cfg.ApplicationProfiles) == 0 {
		return nil
	}
	// API statistics do not prove raw profile OTLP parity or collector resource enrichment.
	// Fail even when sub_signals excludes profiles: no silently accepted unsupported declaration.
	if cloud.CloudWatchExportMode() == "otlp" {
		return fmt.Errorf("bedrock: application_profiles with cloudwatch_export otlp is PENDING raw profile metric-stream parity and enrichment evidence; use remote_write or omit profiles")
	}
	ids := map[string]bool{}
	for i, p := range cfg.ApplicationProfiles {
		prefix := fmt.Sprintf("bedrock: application_profiles[%d]", i)
		if !profileIDPattern.MatchString(p.ID) {
			return fmt.Errorf("%s: id must be an opaque alphanumeric profile ID, not an ARN/model", prefix)
		}
		if ids[p.ID] {
			return fmt.Errorf("%s: duplicate id %q", prefix, p.ID)
		}
		ids[p.ID] = true
		// A declared model must never share a profile's dimension, including across entries.
		for _, model := range cfg.Models {
			if p.ID == model {
				return fmt.Errorf("%s: id collides with models entry %q", prefix, model)
			}
		}
		if strings.TrimSpace(p.Agent) == "" {
			return fmt.Errorf("%s: agent is required", prefix)
		}
		if p.Provider != "bedrock" {
			return fmt.Errorf("%s: provider must be bedrock for generation pricing", prefix)
		}
		model, ok := genai.Lookup(p.Model)
		if !ok || model.Platform != genai.PlatformBedrock || p.Model == p.ID {
			return fmt.Errorf("%s: model must retain a catalogued Bedrock foundation model identity, not a profile ID", prefix)
		}
		for _, v := range []struct {
			name  string
			value float64
		}{
			{"requests_per_min", p.RequestsPerMin},
			{"input_tokens_per_request", float64(p.InputTokensPerRequest)},
			{"output_tokens_per_request", float64(p.OutputTokensPerRequest)},
			{"latency_ms_per_request", p.LatencyMSPerRequest},
			{"estimated_tpm_quota_usage_per_request", float64(p.EstimatedTPMQuotaUsagePerRequest)},
		} {
			if v.value <= 0 || math.IsNaN(v.value) || math.IsInf(v.value, 0) {
				return fmt.Errorf("%s: %s must be finite and positive (no profile defaults)", prefix, v.name)
			}
			if v.name != "requests_per_min" && math.IsInf(v.value*math.Ceil(p.RequestsPerMin), 0) {
				return fmt.Errorf("%s: %s per-period sum overflows", prefix, v.name)
			}
		}
	}
	return nil
}

func (c *Construct) emitProfiles(now time.Time, st *state.State) {
	for _, p := range c.cfg.ApplicationProfiles {
		// Constant synthetic arrival schedule: differences of cumulative expected arrivals give
		// whole per-period counts without rounding a low rate up on every tick. This is not a
		// checkpoint or lifetime counter; missing ticks do not backfill old requests.
		whole, frac := math.Modf(p.RequestsPerMin)
		minute := math.Floor(float64(now.Unix()) / 60)
		count := whole + math.Floor((minute+1)*frac) - math.Floor(minute*frac)
		if count == 0 {
			continue
		} // CW emits no point during idle periods.
		labels := c.baseLabels("AWS/Bedrock", map[string]string{"dimension_ModelId": p.ID})
		// Use the existing global convention; no profile ARN/tag enrichment was observed.
		labels["name"] = "global"
		// Sources: signals/bedrock.md [slug: bedrock-application-profiles]. These are the ONLY
		// five captured bases. Each request has the explicitly configured constant value, hence
		// min = max = mean; the sum and sample count are independent per-period statistics.
		emit := func(base string, value float64) {
			cw.EmitStats(st, base, labels, cw.StatSet{Sum: value * count, Average: value, Minimum: value, Maximum: value, SampleCount: count})
		}
		emit("aws_bedrock_invocations", 1)
		emit("aws_bedrock_input_token_count", float64(p.InputTokensPerRequest))
		emit("aws_bedrock_output_token_count", float64(p.OutputTokensPerRequest))
		emit("aws_bedrock_invocation_latency", p.LatencyMSPerRequest)
		emit("aws_bedrock_estimated_tpmquota_usage", float64(p.EstimatedTPMQuotaUsagePerRequest))
	}
}
