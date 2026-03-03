{{- define "csiRcloneCertificates.env.python" -}}
- name: REQUESTS_CA_BUNDLE
  value: /etc/ssl/certs/ca-certificates.crt
- name: SSL_CERT_FILE
  value: /etc/ssl/certs/ca-certificates.crt
{{- end -}}

{{- define "csiRcloneCertificates.env.grpc" -}}
- name: GRPC_DEFAULT_SSL_ROOTS_FILE_PATH
  value: /etc/ssl/certs/ca-certificates.crt
{{- end -}}

{{- define "csiRcloneCertificates.env.nodejs" -}}
- name: NODE_EXTRA_CA_CERTS
  value: /etc/ssl/certs/ca-certificates.crt
{{- end -}}
