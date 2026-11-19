{{- define "rental-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "rental-operator.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s" (include "rental-operator.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "rental-operator.labels" -}}
app.kubernetes.io/name: {{ include "rental-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "rental-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rental-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "rental-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "rental-operator.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "rental-operator.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/*
The manager's permissions on namespaced objects: its CRDs, the Secrets
holding API keys, and events on GPURentals. Granted cluster-wide, or per
watched namespace.
*/}}
{{- define "rental-operator.managerRules" -}}
- apiGroups: [""]
  resources: [secrets]
  verbs: [get, list, watch]
# Events on GPURentals and SSHKeys (progress and how to get unstuck).
- apiGroups: [events.k8s.io]
  resources: [events]
  verbs: [create, patch]
- apiGroups: [compute.hyperbolic.ai]
  resources: [gpurentals, providerconfigs, gpuavailabilities, sshkeys]
  verbs: [create, delete, get, list, patch, update, watch]
- apiGroups: [compute.hyperbolic.ai]
  resources: [gpurentals/finalizers, providerconfigs/finalizers, sshkeys/finalizers]
  verbs: [update]
- apiGroups: [compute.hyperbolic.ai]
  resources: [gpurentals/status, providerconfigs/status, gpuavailabilities/status, sshkeys/status]
  verbs: [get, patch, update]
{{- end }}

{{/*
The manager's permissions on cluster-scoped objects: ClusterProviderConfigs,
and namespaces (their labels decide which ones a ClusterProviderConfig
allows). These never include Secrets.
*/}}
{{- define "rental-operator.clusterRules" -}}
- apiGroups: [compute.hyperbolic.ai]
  resources: [clusterproviderconfigs]
  verbs: [get, list, watch, patch, update]
- apiGroups: [compute.hyperbolic.ai]
  resources: [clusterproviderconfigs/finalizers]
  verbs: [update]
- apiGroups: [compute.hyperbolic.ai]
  resources: [clusterproviderconfigs/status]
  verbs: [get, patch, update]
- apiGroups: [""]
  resources: [namespaces]
  verbs: [get, list, watch]
{{- end }}
