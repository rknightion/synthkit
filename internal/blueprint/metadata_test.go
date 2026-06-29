// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import "testing"

// TestMetadataResolvesToBlueprintAndEnvs proves the blueprint- and environment-level `metadata:`
// blocks thread through resolve() onto Resolved.Metadata and Resolved.Environments (decl order,
// one entry per declared env, carrying its metadata — empty when omitted).
func TestMetadataResolvesToBlueprintAndEnvs(t *testing.T) {
	const y = `
name: meta
metadata:
  description: "demo blueprint"
  owner: platform-team
  category: reference
  tags: [aws, eks]
  links:
    runbook: https://example.com/rb
environments:
  - name: prod
    metadata:
      description: "primary prod"
      tags: [eu]
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0meta01, nat_gateways: 1 }
  - name: staging
    cloud: { provider: aws, account_id: "111122224444", region: us-east-1, vpc_id: vpc-0meta02, nat_gateways: 1 }
`
	res := load(t, y)

	m := res.Metadata
	if m.Description != "demo blueprint" || m.Owner != "platform-team" || m.Category != "reference" {
		t.Fatalf("blueprint metadata wrong: %+v", m)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "aws" || m.Tags[1] != "eks" {
		t.Fatalf("blueprint tags wrong: %+v", m.Tags)
	}
	if m.Links["runbook"] != "https://example.com/rb" {
		t.Fatalf("blueprint links wrong: %+v", m.Links)
	}

	if len(res.Environments) != 2 {
		t.Fatalf("want 2 resolved envs (decl order), got %d: %+v", len(res.Environments), res.Environments)
	}
	if res.Environments[0].Name != "prod" || res.Environments[1].Name != "staging" {
		t.Fatalf("env order/names wrong: %+v", res.Environments)
	}
	if res.Environments[0].Metadata.Description != "primary prod" ||
		len(res.Environments[0].Metadata.Tags) != 1 || res.Environments[0].Metadata.Tags[0] != "eu" {
		t.Fatalf("prod env metadata wrong: %+v", res.Environments[0].Metadata)
	}
	if !res.Environments[1].Metadata.IsZero() {
		t.Fatalf("staging env metadata should be empty: %+v", res.Environments[1].Metadata)
	}
}
