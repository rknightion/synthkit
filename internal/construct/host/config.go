// SPDX-License-Identifier: AGPL-3.0-only

package host

// Config is the blueprint-decoded configuration for one host construct. Per-host
// topology and exporter knobs (OS, profile, CPU/mem, docker, logs, OS identity) ride on
// fixture.Host, while OTel is the additive native-metrics emission switch. Keeping the
// switch here follows the existing web_service/k8s_cluster `otel.metrics` contract:
// false or absent leaves the established Prometheus exporter lane untouched.
//
// The construct is substrate-scoped (identity = `instance`=hostname) and never carries a
// blueprint label.
type Config struct {
	OTel *OTelObs `yaml:"otel"`
}

// OTel controls the optional hostmetricsreceiver-shaped native OTLP metrics lane.
// Metrics is deliberately opt-in; it adds core.OTLPMetrics while preserving the
// node_exporter/windows_exporter Prometheus lane.
type OTelObs struct {
	Metrics bool `yaml:"metrics"`
}
