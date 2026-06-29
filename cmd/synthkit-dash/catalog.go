// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"github.com/rknightion/synthkit/dashboard"
	acmeaieval "github.com/rknightion/synthkit/dashboards/examples/acme_ai_eval"
	acmeaiplatform "github.com/rknightion/synthkit/dashboards/examples/acme_ai_platform"
	acmeaiplatformeval "github.com/rknightion/synthkit/dashboards/examples/acme_ai_platform_eval"
)

// templateCatalog maps a blueprint name → its dashboard templates. Single-owner wiring
// (mirrors runner.Catalog): adding a blueprint's dashboards = one import + one line here.
// No init() self-registration.
func templateCatalog() map[string][]dashboard.Template {
	return map[string][]dashboard.Template{
		"acme-ai-platform":      acmeaiplatform.Templates(),
		"acme-ai-platform-eval": acmeaiplatformeval.Templates(),
		"acme-ai-eval":          acmeaieval.Templates(),
	}
}

// rulesCatalog maps a blueprint name → its recording/alert rule generator. Only blueprints
// that define rules appear here; absent entries are silently skipped in generate().
func rulesCatalog() map[string]func(*dashboard.Manifest) []dashboard.RuleGroup {
	return map[string]func(*dashboard.Manifest) []dashboard.RuleGroup{
		"acme-ai-platform":      acmeaiplatform.Rules,
		"acme-ai-platform-eval": acmeaiplatformeval.Rules,
		"acme-ai-eval":          acmeaieval.Rules,
	}
}
