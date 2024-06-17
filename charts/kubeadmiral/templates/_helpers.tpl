{{/*
Expand the name of the chart.
*/}}
{{- define "kubeadmiral.name" -}}
{{- default .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kubeadmiral.namespace" -}}
{{- default .Release.Namespace -}}
{{- end -}}

{{/*
params of etcd
*/}}
{{- define "kubeadmiral.etcd.image" -}}
{{- printf "%s" .Values.etcd.image.name -}}
{{- end -}}

{{/*
params of kubeadmiral-apiserver
*/}}
{{- define "kubeadmiral.apiserver.image" -}}
{{- printf "%s" .Values.apiServer.image.name -}}
{{- end -}}

{{/*
params of kubeadmiral-kube-controller-manager
*/}}
{{- define "kubeadmiral.kubeControllerManager.image" -}}
{{- printf "%s" .Values.kubeControllerManager.image.name -}}
{{- end -}}

{{/*
params of kubeadmiral-controller-manager
*/}}
{{- define "kubeadmiral.kubeadmiralControllerManager.image" -}}
{{- printf "%s" .Values.kubeadmiralControllerManager.image.name -}}
{{- end -}}

{{- define "kubeadmiral.kubeadmiralControllerManager.extraCommandArgs" -}}
{{- if .Values.kubeadmiralControllerManager.extraCommandArgs }}
{{- range $key, $value := .Values.kubeadmiralControllerManager.extraCommandArgs }}
- --{{ $key }}={{ $value }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
params of kubeadmiral-hpa-aggregator
*/}}
{{- define "kubeadmiral.kubeadmiralHpaAggregator.image" -}}
{{- printf "%s" .Values.kubeadmiralHpaAggregator.image.name -}}
{{- end -}}

{{- define "kubeadmiral.kubeadmiralHpaAggregator.extraCommandArgs" -}}
{{- if .Values.kubeadmiralHpaAggregator.extraCommandArgs }}
{{- range $key, $value := .Values.kubeadmiralHpaAggregator.extraCommandArgs }}
- --{{ $key }}={{ $value }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
params of cfssl and kubectl components
*/}}
{{- define "kubeadmiral.cfssl.image" -}}
{{- printf "%s" .Values.installTools.cfssl.image.name -}}
{{- end -}}
{{- define "kubeadmiral.kubectl.image" -}}
{{- printf "%s" .Values.installTools.kubectl.image.name -}}
{{- end -}}

{{- define "kubeadmiral.kubeconfig.volume" -}}
{{- $name := include "kubeadmiral.name" . -}}
- name: kubeconfig-secret
  secret:
    secretName: {{ $name }}-kubeconfig-secret
{{- end -}}

{{- define "kubeadmiral.kubeconfig.volumeMount" -}}
- name: kubeconfig-secret
  subPath: kubeconfig
  mountPath: /etc/kubeconfig
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kubeadmiral.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kubeadmiral.labels" -}}
helm.sh/chart: {{ include "kubeadmiral.chart" . }}
{{ include "kubeadmiral.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubeadmiral.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubeadmiral.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
