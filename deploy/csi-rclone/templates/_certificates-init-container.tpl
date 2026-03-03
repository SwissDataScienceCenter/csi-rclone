{{- define "csiRcloneCertificates.initContainer" -}}
{{- $customCAsEnabled := .Values.csiNodepluginRclone.certificates.customCAs -}}
{{- $customCAsForMountsEnabled := .Values.csiNodepluginRclone.certificates.customCAsForDataConnectorMounts -}}
- name: init-certificates
  image: "{{ .Values.csiNodepluginRclone.certificates.image.repository }}:{{ .Values.csiNodepluginRclone.certificates.image.tag }}"
  volumeMounts:
    - name: etc-ssl-certs
      mountPath: /etc/ssl/certs/
    {{- if or $customCAsEnabled $customCAsForMountsEnabled -}}
    - name: custom-ca-certs
      mountPath: /usr/local/share/ca-certificates
      readOnly: true
    {{- end -}}
{{- end -}}
