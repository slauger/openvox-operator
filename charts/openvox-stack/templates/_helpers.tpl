{{/*
Environment name.
*/}}
{{- define "openvox-stack.envName" -}}
{{- .Values.environment.name }}
{{- end }}

{{/*
CertificateAuthority name.
*/}}
{{- define "openvox-stack.caName" -}}
{{- if .Values.ca.name -}}
{{- .Values.ca.name }}
{{- else -}}
{{- include "openvox-stack.envName" . }}-ca
{{- end }}
{{- end }}

{{/*
SigningPolicy name.
*/}}
{{- define "openvox-stack.signingPolicyName" -}}
{{- if .Values.signingPolicy.name -}}
{{- .Values.signingPolicy.name }}
{{- else -}}
{{- include "openvox-stack.envName" . }}-autosign
{{- end }}
{{- end }}

{{/*
Certificate name for a server entry.
Usage: include "openvox-stack.certName" (dict "key" $key "val" $val)
*/}}
{{- define "openvox-stack.certName" -}}
{{- if .val.certificate.name -}}
{{- .val.certificate.name }}
{{- else -}}
{{- .key }}-cert
{{- end }}
{{- end }}

{{/*
Pool name for a server entry.
Usage: include "openvox-stack.poolName" (dict "key" $key "val" $val)
*/}}
{{- define "openvox-stack.poolName" -}}
{{- if .val.pool.name -}}
{{- .val.pool.name }}
{{- else -}}
{{- .key }}
{{- end }}
{{- end }}
