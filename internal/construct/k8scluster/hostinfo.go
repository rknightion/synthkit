// SPDX-License-Identifier: AGPL-3.0-only

// hostinfo.go — traces_host_info metric family for k8scluster.
// Gated by K8sMonitoring.Features["application_observability"].
// Emits one gauge=1 per node carrying ONLY grafana_host_id (= EC2 InstanceID) — live reference
// 2026-06-15 confirmed the otelcol.connector.host_info output has no other label (SK-55 resolved).
package k8scluster

import (
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// emitHostInfo emits traces_host_info (gauge=1) per node when the
// application_observability feature is enabled.
func emitHostInfo(st *state.State, cluster string, cl *fixture.Cluster, nodes []fixture.Node) {
	if !cl.K8sMonitoring.Features["application_observability"] {
		return
	}
	for _, n := range nodes {
		st.Set("traces_host_info", map[string]string{
			"grafana_host_id": n.InstanceID,
		}, 1)
	}
}
