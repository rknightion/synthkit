// SPDX-License-Identifier: AGPL-3.0-only

// Package semconv is the frozen single-source-of-truth for OTEL semantic-convention
// resource-attribute and correlation key NAMES used across synthkit's emit lanes AND its
// dashboards. It is a stdlib-only constants library (peer to internal/genai / internal/cw):
// it emits NOTHING and imports nothing. Names are LAW — sourced verbatim from
// the OpenTelemetry semantic conventions (deployment.environment.name v1.42.0 Stable;
// GenAI conventions experimental).
package semconv

// Metric-label forms (OTLP→Prometheus underscored).
const (
	LabelDeploymentEnvironmentName = "deployment_environment_name"
	LabelServiceName               = "service_name"
	LabelServiceNamespace          = "service_namespace"
	LabelServiceVersion            = "service_version"
	LabelContext                   = "context"
	LabelUseCase                   = "use_case"
	LabelTeam                      = "team"
)

// OTLP dotted resource-attribute forms (span / resource block).
const (
	AttrDeploymentEnvironmentName = "deployment.environment.name"
	AttrServiceName               = "service.name"
	AttrServiceNamespace          = "service.namespace"
	AttrServiceVersion            = "service.version"
)

// Application label canon — FLAT keys (not dotted OTEL semconv), identical on the metric-label and
// resource-attr sides. Carried via OTEL_RESOURCE_ATTRIBUTES in the real estate; synthkit emits them
// as flat resource-attr keys (matching the resource-attr form) + bounded metric labels.
const (
	AttrContext = "context"
	AttrUseCase = "use_case"
	AttrTeam    = "team"
)

// Correlation keys. app.correlation_id is the SPAN attr; correlation_id is the LOG field.
const (
	AttrCorrelationID  = "app.correlation_id" // span attribute (application-level correlation)
	FieldCorrelationID = "correlation_id"     // log structured-metadata field
	KeyPortkeyTraceID  = "portkey_trace_id"
	KeyTraceparent     = "traceparent"
)

// TeamAcmeAI is the team value for the Acme AI demo blueprint.
const TeamAcmeAI = "Acme AI"

// Context enum (§5): the bounded context values.
const (
	ContextPlatform   = "Platform"
	ContextContentGen = "ContentGen"
	ContextDataGen    = "DataGen"
)
