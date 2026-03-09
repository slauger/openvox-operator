{{/*
Fullname prefix for all resources.
Defaults to the Release name, overridable via fullnameOverride.
*/}}
{{- define "openvox-stack.fullname" -}}
{{- .Values.fullnameOverride | default .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Config name — defaults to fullname.
*/}}
{{- define "openvox-stack.configName" -}}
{{- .Values.config.name | default (include "openvox-stack.fullname" .) }}
{{- end }}

{{/*
CertificateAuthority name.
*/}}
{{- define "openvox-stack.caName" -}}
{{- if .Values.ca.name -}}
{{- .Values.ca.name }}
{{- else -}}
{{- include "openvox-stack.fullname" . }}-ca
{{- end }}
{{- end }}

{{/*
SigningPolicy name.
*/}}
{{- define "openvox-stack.signingPolicyName" -}}
{{- if .Values.signingPolicy.name -}}
{{- .Values.signingPolicy.name }}
{{- else -}}
{{- include "openvox-stack.fullname" . }}-autosign
{{- end }}
{{- end }}

{{/*
Server name for a server entry — prefixed with fullname.
Usage: include "openvox-stack.serverName" (dict "root" $ "entry" $entry)
*/}}
{{- define "openvox-stack.serverName" -}}
{{- printf "%s-%s" (include "openvox-stack.fullname" .root) .entry.name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Certificate name for a server entry.
Usage: include "openvox-stack.certName" (dict "root" $ "entry" $entry)
*/}}
{{- define "openvox-stack.certName" -}}
{{- if .entry.certificate.name -}}
{{- .entry.certificate.name }}
{{- else -}}
{{- printf "%s-%s" (include "openvox-stack.fullname" .root) .entry.name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Pool name for a pool entry.
Usage: include "openvox-stack.poolName" (dict "root" $ "entry" $entry)
*/}}
{{- define "openvox-stack.poolName" -}}
{{- if .entry.fullName -}}
{{- .entry.fullName }}
{{- else -}}
{{- printf "%s-%s" (include "openvox-stack.fullname" .root) .entry.name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
