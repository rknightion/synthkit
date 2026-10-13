{{- define "synthkit.sourceInputName" -}}
{{- default (printf "%s-git-sources" (include "synthkit.fullname" . | trunc 51 | trimSuffix "-")) .Values.blueprintSources.existingConfigMap -}}
{{- end -}}

{{- define "synthkit.validateBlueprintSources" -}}
{{- $input := .Values.blueprintSources -}}
{{- if and (not $input.enabled) (or $input.existingConfigMap (gt (len $input.sources) 0)) -}}
{{- fail "blueprintSources input requires enabled: true" -}}
{{- end -}}
{{- if and $input.existingConfigMap (gt (len $input.sources) 0) -}}
{{- fail "blueprintSources sources and existingConfigMap are mutually exclusive" -}}
{{- end -}}
{{- if gt (len (toJson $input.sources)) 262144 -}}
{{- fail "blueprintSources input exceeds 256 KiB" -}}
{{- end -}}
{{- $ids := dict -}}
{{- range $source := $input.sources -}}
{{- if or (hasKey $ids $source.id) (contains "__" $source.id) (contains "__" $source.namespace) -}}
{{- fail "blueprintSources requires distinct stable IDs and namespaces without __" -}}
{{- end -}}
{{- $_ := set $ids $source.id true -}}
{{- if and $.Values.ha.enabled (not (hasKey $.Values.ha.gitSourceConfigMaps $source.id)) -}}
{{- fail "blueprintSources HA declaration requires a predeclared gitSourceConfigMaps slot" -}}
{{- end -}}
{{- end -}}
{{- if and $input.enabled .Values.ha.enabled -}}
{{- $name := include "synthkit.sourceInputName" . -}}
{{- if or (eq $name .Values.ha.controlConfigMap) (eq $name .Values.ha.bootConfigMap) (eq $name .Values.ha.fleetConfigMap) -}}
{{- fail "blueprintSources input must be separate from mutable state ConfigMaps" -}}
{{- end -}}
{{- range $slot := .Values.ha.gitSourceConfigMaps -}}
{{- if eq $name $slot -}}{{- fail "blueprintSources input must be separate from mutable state ConfigMaps" -}}{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
