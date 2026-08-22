{{/*
Chart name.
*/}}
{{- define "k8s-r8r.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name.
*/}}
{{- define "k8s-r8r.fullname" -}}
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

{{/*
Common labels.
*/}}
{{- define "k8s-r8r.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "k8s-r8r.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels.
*/}}
{{- define "k8s-r8r.selectorLabels" -}}
app.kubernetes.io/name: {{ include "k8s-r8r.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
ServiceAccount name.
*/}}
{{- define "k8s-r8r.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "k8s-r8r.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Webhook serving-certificate Secret name. cert-manager writes it when
enabled; otherwise the user supplies it via webhook.certSecretName.
*/}}
{{- define "k8s-r8r.webhookCertSecret" -}}
{{- default (printf "%s-webhook-server-cert" (include "k8s-r8r.fullname" .)) .Values.webhook.certSecretName -}}
{{- end -}}

{{/*
Webhook Service name.
*/}}
{{- define "k8s-r8r.webhookServiceName" -}}
{{- printf "%s-webhook-service" (include "k8s-r8r.fullname" .) -}}
{{- end -}}
