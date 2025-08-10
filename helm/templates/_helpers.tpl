{{/*
Expand the name of the chart.
*/}}
{{- define "kubevirt-redfish.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kubevirt-redfish.fullname" -}}
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
{{- define "kubevirt-redfish.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kubevirt-redfish.labels" -}}
helm.sh/chart: {{ include "kubevirt-redfish.chart" . }}
{{ include "kubevirt-redfish.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubevirt-redfish.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubevirt-redfish.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kubevirt-redfish.serviceAccountName" -}}
{{- if .Values.rbac.serviceAccount.create }}
{{- default (include "kubevirt-redfish.fullname" .) .Values.rbac.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.rbac.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the cluster role to use
*/}}
{{- define "kubevirt-redfish.clusterRoleName" -}}
{{- default (include "kubevirt-redfish.fullname" .) .Values.rbac.clusterRole.name }}
{{- end }}

{{/*
Create the name of the cluster role binding to use
*/}}
{{- define "kubevirt-redfish.clusterRoleBindingName" -}}
{{- default (include "kubevirt-redfish.fullname" .) .Values.rbac.clusterRoleBinding.name }}
{{- end }}

{{/*
Create the name of the config map to use
*/}}
{{- define "kubevirt-redfish.configMapName" -}}
{{- default (include "kubevirt-redfish.fullname" .) .Values.configMap.name }}
{{- end }} 