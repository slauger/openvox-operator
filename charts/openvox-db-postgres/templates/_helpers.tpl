{{/*
Cluster name for all resources.
Defaults to the Release name, overridable via .Values.name.
*/}}
{{- define "openvox-db-postgres.name" -}}
{{- .Values.name | default .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
