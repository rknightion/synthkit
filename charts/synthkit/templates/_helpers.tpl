{{/*
Naming and labels.
*/}}
{{- define "synthkit.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "synthkit.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "synthkit.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "synthkit.labels" -}}
helm.sh/chart: {{ include "synthkit.chart" . }}
{{ include "synthkit.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: synthkit
{{- end -}}

{{- define "synthkit.selectorLabels" -}}
app.kubernetes.io/name: {{ include "synthkit.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "synthkit.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "synthkit.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Image reference. A digest wins over a tag; an empty tag falls back to appVersion. Standing
deployments should pin the verified index digest.
*/}}
{{- define "synthkit.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
{{- end -}}

{{- define "synthkit.pvcName" -}}
{{- if .Values.persistence.existingClaim -}}
{{- .Values.persistence.existingClaim -}}
{{- else -}}
{{- printf "%s-data" (include "synthkit.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
CREDENTIAL GROUP OWNERSHIP — the table that keeps the self-observability separation structural.

Each environment variable belongs to exactly ONE destination stack, and this chart will only ever
project it from that group's Secret. Listing GC_SELF_OTLP_PASSWORD under `data` is not a
misconfiguration that produces surprising telemetry later; it fails the render.
*/}}
{{- define "synthkit.credentialOwnership" -}}
data:
  - GC_TOKEN
  - GC_PROM_RW
  - GC_PROM_USER
  - GC_OTLP_ENDPOINT
  - GC_OTLP_USER
  - GC_LOKI
  - GC_LOKI_USER
  - GC_PROFILES_URL
  - GC_PROFILES_USER
selfObs:
  - GC_SELF_OTLP_ENDPOINT
  - GC_SELF_OTLP_USER
  - GC_SELF_OTLP_PASSWORD
  - GC_SELF_GRAFANA_URL
  - GC_PYROSCOPE_URL
  - GC_PYROSCOPE_USER
  - GC_PYROSCOPE_PASSWORD
rum:
  - GC_FARO_COLLECTOR
  - GC_FARO_APP_KEY
sm:
  - GC_SM_URL
  - GC_SM_TOKEN
fm:
  - GC_FM_URL
  - GC_FM_STACK_ID
  - GC_FM_TOKEN
sigil:
  - GC_SIGIL_ENDPOINT
  - GC_SIGIL_TENANT_ID
  - GC_SIGIL_TOKEN
control:
  - CONTROL_TOKEN
git:
  - GIT_TOKEN
{{- end -}}

{{/*
Environment variable names this chart sets itself, from values rather than from a Secret. Together
with the ownership table above these form the reserved surface that extraEnv may not shadow.
*/}}
{{- define "synthkit.chartSetEnvNames" -}}
- DRY_RUN
- TICK_DEFAULT
- SERIES_CAP
- TICK_TIMEOUT
- BLUEPRINTS
- BLUEPRINT_NAMES
- BLUEPRINT_DATA_DIR
- CONFIG_SNAPSHOT_PATH
- JSON_HTTP_ADDR
- SYNTHKIT_BIND
- SYNTHKIT_IN_CONTAINER
- CONTROL_EXPOSURE_ACK
- GIT_POLL_INTERVAL
- SEND_SHARDS
- SEND_BATCH_MAX
- SEND_BATCH_DEADLINE
- SEND_QUEUE_CAPACITY
- SEND_DRAIN_DEADLINE
- SELFOBS_ENABLED
- SELFOBS_TAGS
- SELFOBS_METRIC_INTERVAL
- PYROSCOPE_TAGS
- PYROSCOPE_MUTEX_FRACTION
- PYROSCOPE_BLOCK_RATE
- SM_PROVISION_APPLY
- SM_PROVISION_ADOPT_LEGACY
- SM_PROVISION_MIGRATE_TARGET
{{- end -}}

{{/*
The effective in-pod bind. Closed (the default) binds loopback, which no other pod can reach and
which `kubectl port-forward` still reaches because forwarding runs inside the pod's own network
namespace. An acknowledged exposure binds all interfaces.
*/}}
{{- define "synthkit.httpAddr" -}}
{{- if .Values.controlPlane.exposure.ack -}}
0.0.0.0:8088
{{- else -}}
127.0.0.1:8088
{{- end -}}
{{- end -}}

{{/*
SYNTHKIT_BIND mirrors the host portion of JSON_HTTP_ADDR.

The binary recognises Kubernetes Pods via KUBERNETES_SERVICE_HOST, including containerd and CRI-O.
Mirroring the value remains deliberate belt-and-braces: both the in-container listener and the
effective exposure must agree, so the chart can never present a loopback host bind in front of an
all-interfaces listener.
*/}}
{{- define "synthkit.hostBind" -}}
{{- if .Values.controlPlane.exposure.ack -}}
0.0.0.0
{{- else -}}
127.0.0.1
{{- end -}}
{{- end -}}

{{/*
Credential environment entries for one group, as secretKeyRef projections. Never `optional: true`:
a Secret missing a projected key must hold the pod in CreateContainerConfigError rather than start
synthkit with a silently blank credential.

Usage: {{ include "synthkit.credentialEnv" (dict "root" $ "group" "data") }}
*/}}
{{- define "synthkit.credentialEnv" -}}
{{- $cfg := index .root.Values.credentials .group | default dict -}}
{{- $secret := get $cfg "existingSecret" -}}
{{- $keys := (get $cfg "keys") | default dict -}}
{{- if $secret -}}
{{- range $env := (keys $keys | sortAlpha) }}
- name: {{ $env }}
  valueFrom:
    secretKeyRef:
      name: {{ $secret | quote }}
      key: {{ index $keys $env | quote }}
{{- end }}
{{- end -}}
{{- end -}}

{{/*
Every credential group's environment entries, in a stable order.
*/}}
{{- define "synthkit.allCredentialEnv" -}}
{{- $root := . -}}
{{- range $group := (list "data" "selfObs" "rum" "sm" "fm" "sigil" "control" "git") -}}
{{- include "synthkit.credentialEnv" (dict "root" $root "group" $group) -}}
{{- end -}}
{{- end -}}

{{/*
VALIDATION. Included by every rendered object, so a bad permutation fails `helm template`,
`helm lint`, `helm install` and `helm upgrade` alike rather than producing a manifest that only
misbehaves once it is running.
*/}}
{{- define "synthkit.validate" -}}
{{- $own := include "synthkit.credentialOwnership" . | fromYaml -}}
{{- $creds := .Values.credentials -}}
{{- $seen := dict -}}

{{/* 1. Group ownership: an env var may only be projected from the group that owns it. */}}
{{- range $group, $allowed := $own -}}
  {{- $cfg := index $creds $group | default dict -}}
  {{- $secret := get $cfg "existingSecret" -}}
  {{- $keys := (get $cfg "keys") | default dict -}}
  {{/* An empty existingSecret means the group is simply not configured, and its keys are inert.
       The reverse is always a mistake: a named Secret that projects nothing silently leaves the
       lane unconfigured. */}}
  {{- if and $secret (eq (len $keys) 0) -}}
    {{- fail (printf "credentials.%s.existingSecret names %q but credentials.%s.keys projects nothing, so no credential would reach synthkit. Map the variables the Secret carries. Group %s owns: %s" $group $secret $group $group (join ", " $allowed)) -}}
  {{- end -}}
  {{- range $env, $secretKey := $keys -}}
    {{- if not (has $env $allowed) -}}
      {{- fail (printf "credentials.%s may not project %s. That variable belongs to another destination stack; move it to the group that owns it. Group %s owns: %s" $group $env $group (join ", " $allowed)) -}}
    {{- end -}}
    {{- if hasKey $seen $env -}}
      {{- fail (printf "%s is projected by both credentials.%s and credentials.%s" $env (index $seen $env) $group) -}}
    {{- end -}}
    {{- $_ := set $seen $env $group -}}
    {{- if not $secretKey -}}
      {{- fail (printf "credentials.%s.keys.%s has an empty Secret key" $group $env) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{/* 2. The self-obs stack must be a DIFFERENT Secret from every synthetic-data-path stack.
     Sharing one object is how GC_TOKEN ends up authenticating self-observability, which
     ARCHITECTURE 6.1 forbids. */}}
{{- $selfSecret := $creds.selfObs.existingSecret -}}
{{- if $selfSecret -}}
  {{- range $group := (list "data" "rum" "sm" "fm" "sigil" "control" "git") -}}
    {{- $other := (index $creds $group | default dict) -}}
    {{- if eq $selfSecret (get $other "existingSecret" | default "") -}}
      {{- fail (printf "credentials.selfObs.existingSecret must not be the same Secret as credentials.%s.existingSecret (%s). Self-observability ships to a SEPARATE stack with its own token and must never share the synthetic-data credential object." $group $selfSecret) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{/* 3. A live push needs the mandatory synthetic-data lanes. */}}
{{- if not .Values.config.dryRun -}}
  {{- if not $creds.data.existingSecret -}}
    {{- fail "config.dryRun is false but credentials.data.existingSecret is empty. A live push needs the synthetic-data Secret." -}}
  {{- end -}}
  {{- $dataKeys := $creds.data.keys | default dict -}}
  {{- range $required := (list "GC_TOKEN" "GC_PROM_RW" "GC_PROM_USER" "GC_OTLP_ENDPOINT" "GC_OTLP_USER" "GC_LOKI" "GC_LOKI_USER") -}}
    {{- if not (hasKey $dataKeys $required) -}}
      {{- fail (printf "config.dryRun is false but credentials.data.keys does not project %s. The mandatory lanes are metrics, logs and traces." $required) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{/* 4. Self-observability needs its own Secret before it is switched on. */}}
{{- if .Values.selfObs.enabled -}}
  {{- if not $selfSecret -}}
    {{- fail "selfObs.enabled is true but credentials.selfObs.existingSecret is empty. Self-observability ships to a separate stack and needs its own Secret; it must never borrow GC_TOKEN." -}}
  {{- end -}}
  {{- if eq (len ($creds.selfObs.keys | default dict)) 0 -}}
    {{- fail "selfObs.enabled is true but credentials.selfObs.keys projects nothing. Map the self-obs OTLP triplet, the Pyroscope triplet, or both." -}}
  {{- end -}}
{{- end -}}

{{/* 5. Control-plane exposure keeps the binary's own friction. */}}
{{- $ack := .Values.controlPlane.exposure.ack -}}
{{- $routeEnabled := .Values.controlPlane.httpRoute.enabled -}}
{{- $published := or .Values.controlPlane.ingress.enabled $routeEnabled -}}
{{- if and $ack (not (has $ack (list "trusted-network" "tls-proxy"))) -}}
  {{- fail (printf "controlPlane.exposure.ack must be exactly trusted-network or tls-proxy (got %q). Leave it empty to keep the control plane closed." $ack) -}}
{{- end -}}
{{- if and $published (eq $ack "trusted-network") -}}
  {{- fail "controlPlane.exposure.ack is trusted-network but a hostname is published through Ingress or HTTPRoute. Routed TLS termination requires tls-proxy." -}}
{{- end -}}
{{- if $ack -}}
  {{- if not $creds.control.existingSecret -}}
    {{- fail "controlPlane.exposure.ack is set but credentials.control.existingSecret is empty. Exposing the control plane beyond the pod requires CONTROL_TOKEN, exactly as it does outside Kubernetes." -}}
  {{- end -}}
  {{- if not (hasKey ($creds.control.keys | default dict) "CONTROL_TOKEN") -}}
    {{- fail "controlPlane.exposure.ack is set but credentials.control.keys does not project CONTROL_TOKEN." -}}
  {{- end -}}
{{- else -}}
  {{- if .Values.controlPlane.service.enabled -}}
    {{- fail "controlPlane.service.enabled is true but controlPlane.exposure.ack is empty. A Service is a network surface: acknowledge it with trusted-network or tls-proxy and supply a control-token Secret, or leave the control plane on its loopback bind and use kubectl port-forward." -}}
  {{- end -}}
  {{- if .Values.controlPlane.ingress.enabled -}}
    {{- fail "controlPlane.ingress.enabled is true but controlPlane.exposure.ack is empty." -}}
  {{- end -}}
  {{- if $routeEnabled -}}
    {{- fail "controlPlane.httpRoute.enabled is true but controlPlane.exposure.ack is empty." -}}
  {{- end -}}
{{- end -}}
{{- if .Values.controlPlane.ingress.enabled -}}
  {{- if not .Values.controlPlane.service.enabled -}}
    {{- fail "controlPlane.ingress.enabled requires controlPlane.service.enabled." -}}
  {{- end -}}
  {{- if eq (len .Values.controlPlane.ingress.hosts) 0 -}}
    {{- fail "controlPlane.ingress.enabled requires at least one entry under controlPlane.ingress.hosts." -}}
  {{- end -}}
{{- end -}}
{{- if $routeEnabled -}}
  {{- if not .Values.controlPlane.service.enabled -}}
    {{- fail "controlPlane.httpRoute.enabled requires controlPlane.service.enabled." -}}
  {{- end -}}
  {{- if eq (len .Values.controlPlane.httpRoute.parentRefs) 0 -}}
    {{- fail "controlPlane.httpRoute.enabled requires at least one entry under controlPlane.httpRoute.parentRefs." -}}
  {{- end -}}
  {{- if eq (len .Values.controlPlane.httpRoute.hostnames) 0 -}}
    {{- fail "controlPlane.httpRoute.enabled requires at least one entry under controlPlane.httpRoute.hostnames." -}}
  {{- end -}}
  {{- if eq (len .Values.controlPlane.httpRoute.rules) 0 -}}
    {{- fail "controlPlane.httpRoute.enabled requires at least one entry under controlPlane.httpRoute.rules." -}}
  {{- end -}}
{{- end -}}
{{- if and .Values.controlPlane.ingress.enabled $routeEnabled -}}
  {{- range $routeHost := .Values.controlPlane.httpRoute.hostnames -}}
    {{- range $ingressHost := $.Values.controlPlane.ingress.hosts -}}
      {{- if eq $routeHost $ingressHost.host -}}
        {{- fail (printf "controlPlane.ingress and controlPlane.httpRoute both publish host %q; choose one route type for each host." $routeHost) -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if and $published .Values.networkPolicy.enabled (eq (len .Values.networkPolicy.ingressFrom) 0) -}}
  {{- fail "a published control-plane Ingress or HTTPRoute requires at least one networkPolicy.ingressFrom peer while networkPolicy.enabled is true; otherwise the default-deny policy leaves the route pointing at a blocked pod." -}}
{{- end -}}

{{/* 6. extraEnv may not shadow synthkit's own configuration surface, which would let a value
     route an owned credential around the group table above. */}}
{{- $reservedFlat := (include "synthkit.chartSetEnvNames" . | fromYamlArray) -}}
{{- range $group, $allowed := $own -}}
  {{- $reservedFlat = concat $reservedFlat $allowed -}}
{{- end -}}
{{- range $e := .Values.extraEnv -}}
  {{- if has $e.name $reservedFlat -}}
    {{- fail (printf "extraEnv may not set %s: it is part of synthkit's own configuration surface and this chart owns it. Credentials belong in a credentials.<group> Secret." $e.name) -}}
  {{- end -}}
{{- end -}}

{{/* 7. Persistence combinations. */}}
{{- if and .Values.persistence.enabled .Values.persistence.existingClaim -}}
  {{- if .Values.persistence.storageClass -}}
    {{- fail "persistence.existingClaim and persistence.storageClass are mutually exclusive." -}}
  {{- end -}}
{{- end -}}
{{- if .Values.smProvision.enabled -}}
  {{- if not $creds.sm.existingSecret -}}
    {{- fail "smProvision.enabled is true but credentials.sm.existingSecret is empty. The provisioner needs GC_SM_URL and GC_SM_TOKEN, and it receives no other credential." -}}
  {{- end -}}
  {{- if not .Values.persistence.enabled -}}
    {{- fail "smProvision.enabled requires persistence.enabled: the ownership ledger it writes must survive a reschedule." -}}
  {{- end -}}
{{- end -}}
{{- end -}}
