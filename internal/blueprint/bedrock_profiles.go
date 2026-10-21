// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import "fmt"

// validateBedrockProfileClaims rejects multiple environment producers for one
// CloudWatch profile identity within a blueprint. The common blueprint selector
// cannot distinguish them; environment, VPC, agent and team are not dimensions.
func validateBedrockProfileClaims(d *Decl) error {
	type identity struct{ accountID, region, profileID string }
	owners := map[identity]string{}
	for _, env := range d.Environments {
		if env.Cloud == nil || env.Cloud.Bedrock.Kind == 0 {
			continue
		}
		// Project declaration identity only, without importing a construct or reproducing
		// its config schema. Ordinary registry strict decoding and Build validation still
		// run for every declaration, including excluded profile sub-signals.
		var projection struct {
			Profiles []struct {
				ID string `yaml:"id"`
			} `yaml:"application_profiles"`
		}
		if err := env.Cloud.Bedrock.Decode(&projection); err != nil {
			return fmt.Errorf("blueprint %q: env %q bedrock application_profiles: %w", d.Name, env.Name, err)
		}
		for _, profile := range projection.Profiles {
			// Missing IDs remain the construct's validation error.
			if profile.ID == "" {
				continue
			}
			key := identity{env.Cloud.AccountID, env.Cloud.Region, profile.ID}
			if previous, exists := owners[key]; exists && previous != env.Name {
				return fmt.Errorf("blueprint %q: bedrock profile identity collision: account_id %q region %q profile ID %q declared by environments %q and %q", d.Name, key.accountID, key.region, key.profileID, previous, env.Name)
			}
			// Within-list duplicates remain the construct's existing validation responsibility.
			owners[key] = env.Name
		}
	}
	return nil
}
