// SPDX-License-Identifier: AGPL-3.0-only

package portkeypoller

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the failure modes the portkey_poller construct responds to.
// Scoped to env (when env-scoped) so a fired incident degrades the targeted env's
// Portkey Analytics scrape coherently across all affected series.
var FailureModes = []failuremode.Mode{
	{
		Name: "portkey_scrape_degraded",
		Axis: failuremode.AxisCloud,
		Help: "Portkey Analytics scrape degrades — API error_rate and 4xx/5xx share climb, latency rises, and the poller falls behind (poller errors + window lag grow)",
	},
}
