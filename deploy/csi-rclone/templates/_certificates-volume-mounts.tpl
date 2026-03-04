{{- define "csiRcloneCertificates.volumeMounts.system" -}}
- name: etc-ssl-certs
  mountPath: /etc/ssl/certs/
  readOnly: true
{{- end -}}
