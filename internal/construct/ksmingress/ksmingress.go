// SPDX-License-Identifier: AGPL-3.0-only

// Package ksmingress implements the "ksm_ingress" construct (ARCHITECTURE §2,
// kind="ksm_ingress", Scope=ScopeSubstrate). It emits kube_ingress_* KSM metrics for
// every ingress in the fixture Cluster.
//
// Kind:     "ksm_ingress"
// Scope:    core.ScopeSubstrate (no blueprint label; cluster disambiguates — I21)
// Signals:  []{core.Metrics}
// Interval: 60 s
//
// ⚠ CRITICAL — cluster injection (ARCHITECTURE I16):
// KSM's default label set for kube_ingress_* is only {namespace, ingress}. Because
// multiple clusters can have ingresses with identical namespace+name, the construct MUST
// inject `cluster` (and `k8s_cluster_name`) on every series to prevent series collisions
// on Metrics.Write. This is the only KSM-family series where the emitter explicitly adds
// cluster beyond the normal base.
//
// Config (YAML):
//
//	ingresses:
//	  - name: <required>
//	    namespace: <default: first workload's namespace>
//	    host: <default: "<name>.example.com">
//	    path: <default: "/">
//	    service_name: <required>
//	    service_port: <default: 80>
//	    tls: <default: false>
//
// When `ingresses` is empty, one ingress is derived per fixture.Cluster.Workload entry:
//   - name = workload.Name
//   - namespace = workload.Namespace
//   - host = "<workload.Name>.example.com"
//   - path = "/"
//   - service_name = workload.Name
//   - service_port = 80
//   - tls = false
//
// Families emitted (signals/k8s-addons.md [slug: k8s-ksm-ingress]):
//   - kube_ingress_info (G; namespace, ingress, ingressclass)
//   - kube_ingress_path (G; namespace, ingress, host, path, path_type, service_name, service_port)
//   - kube_ingress_tls (G; namespace, ingress, tls_host, secret) — only when tls=true
//   - kube_ingress_created (G; namespace, ingress)
//   - kube_ingress_labels (G; namespace, ingress)
//   - kube_ingress_annotations (G ALPHA; namespace, ingress)
//   - kube_ingress_metadata_resource_version (G ALPHA; namespace, ingress)
package ksmingress

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "ksm_ingress"
	interval = 60 * time.Second

	// KSM job and instance values (signals/k8s-addons.md [slug: k8s-ksm-ingress] — reuse KSM job).
	ksmJob      = "integrations/kubernetes/kube-state-metrics"
	ksmInstance = "10.1.30.200:8080"
	k8sSource   = "kubernetes"

	// ksmCreatedEpoch is a frozen synthetic creation timestamp (2025-06-01 00:00:00 UTC).
	ksmCreatedEpoch = 1748736000

	defaultIngressClass = "alb"
	defaultPathType     = "Prefix"
)

// IngressConfig is one ingress entry in the construct config.
type IngressConfig struct {
	Name        string `yaml:"name"`
	Namespace   string `yaml:"namespace"`    // default: first workload's namespace
	Host        string `yaml:"host"`         // default: "<name>.example.com"
	Path        string `yaml:"path"`         // default: "/"
	ServiceName string `yaml:"service_name"` // required
	ServicePort int    `yaml:"service_port"` // default: 80
	TLS         bool   `yaml:"tls"`          // default: false
}

// Config is the YAML config struct for the ksm_ingress construct.
type Config struct {
	Ingresses []IngressConfig `yaml:"ingresses"`
}

// resolved is a fully-resolved ingress definition used at tick time.
type resolved struct {
	name         string
	namespace    string
	host         string
	path         string
	pathType     string
	ingressClass string
	serviceName  string
	servicePort  string
	tls          bool
	tlsSecret    string
}

// Construct is one ksm_ingress instance covering all ingresses for a cluster.
type Construct struct {
	cluster   *fixture.Cluster
	ingresses []resolved
	st        *state.State
}

// New builds a Construct from the cfg pointer and the resolved fixture set.
// Returns an error if fx.Cluster is nil (required for cluster label and default
// ingress derivation).
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	c := cfg.(*Config)
	if fx.Cluster == nil {
		return nil, errors.New("ksm_ingress: fixture.Cluster is required (nil)")
	}

	ingresses := resolveIngresses(c, fx.Cluster)
	if len(ingresses) == 0 {
		return nil, errors.New("ksm_ingress: no ingresses resolved (cluster has no workloads and config.ingresses is empty)")
	}

	return &Construct{
		cluster:   fx.Cluster,
		ingresses: ingresses,
		st:        state.NewState(),
	}, nil
}

// resolveIngresses converts Config + Cluster into the canonical resolved slice.
// When config.Ingresses is empty, one ingress is derived per workload.
func resolveIngresses(cfg *Config, cl *fixture.Cluster) []resolved {
	if len(cfg.Ingresses) > 0 {
		out := make([]resolved, 0, len(cfg.Ingresses))
		for _, ing := range cfg.Ingresses {
			r := resolveOne(ing, cl)
			out = append(out, r)
		}
		return out
	}

	// Default: one ingress per workload.
	out := make([]resolved, 0, len(cl.Workloads))
	for _, wl := range cl.Workloads {
		r := resolveOne(IngressConfig{
			Name:        wl.Name,
			Namespace:   wl.Namespace,
			ServiceName: wl.Name,
		}, cl)
		out = append(out, r)
	}
	return out
}

// resolveOne resolves a single IngressConfig, applying defaults where values are absent.
func resolveOne(ing IngressConfig, cl *fixture.Cluster) resolved {
	ns := ing.Namespace
	if ns == "" {
		// Default: first workload's namespace.
		if len(cl.Workloads) > 0 {
			ns = cl.Workloads[0].Namespace
		} else {
			ns = "default"
		}
	}

	host := ing.Host
	if host == "" {
		host = ing.Name + ".example.com"
	}

	path := ing.Path
	if path == "" {
		path = "/"
	}

	port := ing.ServicePort
	if port == 0 {
		port = 80
	}

	tlsSecret := ""
	if ing.TLS {
		tlsSecret = ing.Name + "-tls"
	}

	return resolved{
		name:         ing.Name,
		namespace:    ns,
		host:         host,
		path:         path,
		pathType:     defaultPathType,
		ingressClass: defaultIngressClass,
		serviceName:  ing.ServiceName,
		servicePort:  fmt.Sprintf("%d", port),
		tls:          ing.TLS,
		tlsSecret:    tlsSecret,
	}
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics only.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one metric batch covering all ingresses for the cluster. All series are
// gauges (state.Set — ARCHITECTURE I3; these are KSM metadata gauges, not counters).
//
// ⚠ Every series carries `cluster` (I16): KSM's default kube_ingress_* label set omits
// cluster, which would cause series collisions when multiple clusters share the same
// namespace+ingress name. Injection here is mandatory.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	clusterName := c.cluster.Name

	// base always includes cluster + k8s_cluster_name (I16 mandatory injection).
	base := map[string]string{
		"cluster":          clusterName,
		"k8s_cluster_name": clusterName,
		"job":              ksmJob,
		"instance":         ksmInstance,
		"source":           k8sSource,
	}

	withExtra := func(extra map[string]string) map[string]string {
		m := make(map[string]string, len(base)+len(extra))
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	for _, ing := range c.ingresses {
		// Per-ingress base: namespace + ingress name always present.
		ingBase := withExtra(map[string]string{
			"namespace": ing.namespace,
			"ingress":   ing.name,
		})

		// kube_ingress_info (G; ingressclass)
		c.st.Set("kube_ingress_info", withExtra(map[string]string{
			"namespace":    ing.namespace,
			"ingress":      ing.name,
			"ingressclass": ing.ingressClass,
		}), 1)

		// kube_ingress_path (G; host, path, path_type, service_name, service_port)
		c.st.Set("kube_ingress_path", withExtra(map[string]string{
			"namespace":    ing.namespace,
			"ingress":      ing.name,
			"host":         ing.host,
			"path":         ing.path,
			"path_type":    ing.pathType,
			"service_name": ing.serviceName,
			"service_port": ing.servicePort,
		}), 1)

		// kube_ingress_tls (G; tls_host, secret) — only when TLS configured (I13: absent = omitted)
		if ing.tls {
			c.st.Set("kube_ingress_tls", withExtra(map[string]string{
				"namespace": ing.namespace,
				"ingress":   ing.name,
				"tls_host":  ing.host,
				"secret":    ing.tlsSecret,
			}), 1)
		}

		// kube_ingress_created (G; frozen epoch)
		c.st.Set("kube_ingress_created", ingBase, float64(ksmCreatedEpoch))

		// kube_ingress_labels (G)
		c.st.Set("kube_ingress_labels", ingBase, 1)

		// kube_ingress_annotations (G ALPHA)
		c.st.Set("kube_ingress_annotations", ingBase, 1)

		// kube_ingress_metadata_resource_version (G ALPHA)
		c.st.Set("kube_ingress_metadata_resource_version", ingBase, 12345)
	}

	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// Registration returns the core.ConstructReg for the "ksm_ingress" kind.
// The composition root's catalog wiring file calls this; no init() self-registration
// (ARCHITECTURE §2).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      kind,
		Doc:       "KSM kube_ingress_* metrics with mandatory cluster label injection (I16)",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build:     New,
	}
}
