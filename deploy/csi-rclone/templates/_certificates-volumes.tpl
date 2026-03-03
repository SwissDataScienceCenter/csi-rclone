{{- define "certificatesForMounts.volumes" -}}
{{- $customCAsEnabled := .Values.csiNodepluginRclone.certificates.customCAs -}}
{{- $customCAsForMountsEnabled := .Values.csiNodepluginRclone.certificates.customCAsForDataConnectorMounts -}}
- name: etc-ssl-certs
  emptyDir:
    medium: "Memory"
{{- if or $customCAsEnabled $customCAsForMountsEnabled -}}
- name: custom-ca-certs
  projected:
    defaultMode: 0444
    sources:
    {{- if $customCAsEnabled }}
    {{- range $customCA := .Values.csiNodepluginRclone.certificates.customCAs }}
      - secret:
          name: {{ $customCA.secret }}
    {{- end -}}
    {{- end -}}
    {{- if $customCAsForMountsEnabled }}
    {{- range $customCA := .Values.csiNodepluginRclone.certificates.customCAsForDataConnectorMounts }}
      - secret:
          name: {{ $customCA.secret }}
    {{- end -}}
    {{- end -}}
{{- end -}}
{{- end -}}
