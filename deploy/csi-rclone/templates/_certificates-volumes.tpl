{{- define "csiRcloneCertificatesForMounts.volumes" -}}
{{- $customCAsEnabled := .Values.csiNodepluginRclone.certificates.customCAs -}}
- name: etc-ssl-certs
  emptyDir:
    medium: "Memory"
{{- if $customCAsEnabled }}
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
{{- end -}}
{{- end -}}
