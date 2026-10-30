{{/*
Helpers partagés — kubemanager-watcher.
*/}}

{{- define "watcher.fullname" -}}
{{- default .Release.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "watcher.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
    {{- default (include "watcher.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
    {{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "watcher.secretName" -}}
{{- printf "%s-api-key" (include "watcher.fullname" .) -}}
{{- end -}}

{{- define "watcher.labels" -}}
app.kubernetes.io/name: {{ include "watcher.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: watcher
app.kubernetes.io/part-of: kubemanager
{{- end -}}

{{- define "watcher.selectorLabels" -}}
app.kubernetes.io/name: {{ include "watcher.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
