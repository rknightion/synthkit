// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"maps"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
)

// resourceInfos maps a blueprint's resolved causal resources to the control-schema wire shape.
// Consumers is always a non-nil slice and every identity map is a copy, so a schema reader can
// neither see null nor reach the resolver's maps.
func resourceInfos(bp string, resources []blueprint.ResolvedResource) []control.ResourceInfo {
	if len(resources) == 0 {
		return nil
	}
	out := make([]control.ResourceInfo, 0, len(resources))
	for _, r := range resources {
		ri := control.ResourceInfo{
			Blueprint: bp, Name: r.Name, Kind: string(r.Kind), Env: r.Env,
			Alias: r.Alias, HeldAcross: r.HeldAcross,
			Consumers: make([]control.ResourceConsumer, 0, len(r.Consumers)),
		}
		for _, c := range r.Consumers {
			rc := control.ResourceConsumer{Kind: c.Kind, Name: c.Name, Service: c.Service, Identity: maps.Clone(c.Identity)}
			if len(c.Callers) > 0 {
				rc.Callers = append([]string(nil), c.Callers...)
			}
			ri.Consumers = append(ri.Consumers, rc)
		}
		out = append(out, ri)
	}
	return out
}
