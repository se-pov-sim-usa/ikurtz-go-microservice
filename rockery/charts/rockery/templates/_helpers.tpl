{{- define "rockery.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "rockery.fullname" -}}
{{- if contains (include "rockery.name" .) .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "rockery.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "rockery.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "rockery.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Immutable image reference, no implicit "latest". Supply EITHER:
  image.reference                       one full ref, e.g. <+artifacts.primary.image> from a Harness service
  image.repository + image.tag          two parts, e.g. from `make deploy`
*/}}
{{- define "rockery.image" -}}
{{- if .Values.image.reference -}}
{{- .Values.image.reference -}}
{{- else -}}
{{- printf "%s:%s" (required "set image.reference, or image.repository + image.tag" .Values.image.repository) (required "image.tag is required (an immutable tag, never 'latest')" .Values.image.tag) -}}
{{- end -}}
{{- end }}

{{/* Just the tag, for the version label and the test pod. Works for either form. */}}
{{- define "rockery.imageTag" -}}
{{- if .Values.image.reference -}}
{{- regexFind "[^:@]+$" .Values.image.reference -}}
{{- else -}}
{{- required "image.tag is required" .Values.image.tag -}}
{{- end -}}
{{- end }}

{{/* Selector labels must never change after first install. */}}
{{- define "rockery.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rockery.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* Full label set. The version label is the image tag, so `kubectl get pods -L app.kubernetes.io/version` shows what is running. */}}
{{- define "rockery.labels" -}}
{{ include "rockery.selectorLabels" . }}
app.kubernetes.io/version: {{ include "rockery.imageTag" . | trunc 63 | trimSuffix "-" | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
rockery.example.com/environment: {{ .Values.environment | quote }}
{{- with .Values.cluster.name }}
rockery.example.com/cluster: {{ . | quote }}
{{- end }}
{{- with .Values.cluster.group }}
rockery.example.com/cluster-group: {{ . | quote }}
{{- end }}
{{- end }}
