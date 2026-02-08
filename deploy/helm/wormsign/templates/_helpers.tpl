{{/*
Expand the name of the chart.
*/}}
{{- define "wormsign.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this
(by the DNS naming spec). If release name contains chart name it will be used
as a full name.
*/}}
{{- define "wormsign.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "wormsign.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "wormsign.labels" -}}
helm.sh/chart: {{ include "wormsign.chart" . }}
{{ include "wormsign.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "wormsign.selectorLabels" -}}
app.kubernetes.io/name: {{ include "wormsign.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "wormsign.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "wormsign.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Controller container image.
*/}}
{{- define "wormsign.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Controller namespace — uses Release.Namespace.
*/}}
{{- define "wormsign.namespace" -}}
{{- .Release.Namespace -}}
{{- end }}

{{/*
Leader election lease name.
*/}}
{{- define "wormsign.leaderElectionLeaseName" -}}
{{- printf "%s-leader" (include "wormsign.fullname" .) -}}
{{- end }}

{{/*
Shard map ConfigMap name.
*/}}
{{- define "wormsign.shardMapName" -}}
{{- printf "%s-shard-map" (include "wormsign.fullname" .) -}}
{{- end }}

{{/*
Config ConfigMap name.
*/}}
{{- define "wormsign.configMapName" -}}
{{- printf "%s-config" (include "wormsign.fullname" .) -}}
{{- end }}
