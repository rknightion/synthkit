// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCallDecl_ServiceAndVia(t *testing.T) {
	var c CallDecl
	if err := yaml.Unmarshal([]byte("service: payments\nvia: gateway\n"), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Service != "payments" || c.Via != "gateway" {
		t.Fatalf("got service=%q via=%q, want payments/gateway", c.Service, c.Via)
	}
}

func TestHostsDecodeFromTopLevelList(t *testing.T) {
	var d Decl
	src := `
name: t
hosts:
  - name: camden
    os: linux
    cpus: 4
    memory_gb: 16
    metrics_profile: integration
    observability: { docker: true, logs: false }
  - name: winbox
    os: windows
    metrics_profile: full
`
	if err := yaml.Unmarshal([]byte(src), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(d.Hosts) != 2 {
		t.Fatalf("Hosts not decoded: got %d, want 2 (%+v)", len(d.Hosts), d.Hosts)
	}
	h0 := d.Hosts[0]
	if h0.Name != "camden" || h0.OS != "linux" || h0.CPUs != 4 || h0.MemoryGB != 16 || h0.MetricsProfile != "integration" {
		t.Fatalf("host[0] fields wrong: %+v", h0)
	}
	if h0.Observability == nil || h0.Observability.Docker == nil || !*h0.Observability.Docker {
		t.Fatalf("host[0] observability.docker not decoded: %+v", h0.Observability)
	}
	if h0.Observability.Logs == nil || *h0.Observability.Logs {
		t.Fatalf("host[0] observability.logs not decoded as false: %+v", h0.Observability)
	}
	h1 := d.Hosts[1]
	if h1.Name != "winbox" || h1.OS != "windows" || h1.MetricsProfile != "full" {
		t.Fatalf("host[1] fields wrong: %+v", h1)
	}
	if h1.Observability != nil {
		t.Fatalf("host[1] observability should be nil when omitted: %+v", h1.Observability)
	}
}

func TestWorkloadDecodesForEachEnv(t *testing.T) {
	var d Decl
	src := `
name: t
environments: [{name: a}, {name: b}]
workloads:
  - { type: web_service, name: app, for_each_env: true, envs: [a, b], calls: [{agent: planner}] }
`
	if err := yaml.Unmarshal([]byte(src), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	w := d.Workloads[0]
	if !w.ForEachEnv || len(w.Envs) != 2 || w.Envs[0] != "a" || w.Envs[1] != "b" {
		t.Fatalf("for_each_env/envs not decoded: ForEachEnv=%v Envs=%v", w.ForEachEnv, w.Envs)
	}
	// the remaining wiring keys must still decode, and the AI call must land in Calls
	if w.Type != "web_service" || w.Name != "app" || len(w.Calls) != 1 || w.Calls[0].Agent != "planner" {
		t.Fatalf("other workload keys regressed: %+v", w)
	}
}
