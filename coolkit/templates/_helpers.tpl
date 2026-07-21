{{/*
Expand the name of the chart.
*/}}
{{- define "coolkit.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. Truncated to 63 chars for DNS/label limits.
*/}}
{{- define "coolkit.fullname" -}}
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

{{- define "coolkit.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels applied to every object.
*/}}
{{- define "coolkit.labels" -}}
helm.sh/chart: {{ include "coolkit.chart" . }}
{{ include "coolkit.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels — immutable across upgrades, so keep these minimal and stable.
*/}}
{{- define "coolkit.selectorLabels" -}}
app.kubernetes.io/name: {{ include "coolkit.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Name of the headless Service used for cache peer discovery.
*/}}
{{- define "coolkit.headlessName" -}}
{{- printf "%s-headless" (include "coolkit.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
ServiceAccount name to use.
*/}}
{{- define "coolkit.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "coolkit.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Fully-qualified image reference. REQUIRES image.tag to be set (immutable SHA); rendering
fails otherwise, which is the guardrail against shipping a floating "latest" tag.
*/}}
{{- define "coolkit.image" -}}
{{- $tag := required "image.tag is required and must be an immutable SHA (never 'latest')" .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/*
Name of the Secret holding DB credentials (existing, or chart-created).
*/}}
{{- define "coolkit.dbSecretName" -}}
{{- if .Values.cloudsql.existingSecret -}}
{{- .Values.cloudsql.existingSecret -}}
{{- else -}}
{{- printf "%s-db" (include "coolkit.fullname" .) -}}
{{- end -}}
{{- end -}}
