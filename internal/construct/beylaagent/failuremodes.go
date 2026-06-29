// SPDX-License-Identifier: AGPL-3.0-only

package beylaagent

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes for beyla_agent: none in v1. The agent's self/internal metrics are a steady
// footprint of the eBPF instrumentation runtime, not a request-correlated lane — there is no
// modelled incident axis for the agent itself. Declared explicitly (nil) so the registry's
// FailureModes union is well-defined and a future amplifier (e.g. an eBPF probe-error storm)
// has an obvious home. Beyla's request-correlated failure behaviour lives on the web_service
// observation lane, not here.
var FailureModes []failuremode.Mode
