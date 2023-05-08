#!/usr/bin/env bash

# Copyright 2023 The KubeAdmiral Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

function util::create_host_cluster() {
  local HOST_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}
  local MANIFEST_DIR=${3}
  local CONFIG_DIR=${4}
  local HOST_CONTEXT

  if [[ $CLUSTER_PROVIDER == "kind" ]]; then
    util::create_host_kind_cluster "${HOST_CLUSTER_NAME}" "${KUBECONFIG_PATH}"
    HOST_CONTEXT="kind-${HOST_CLUSTER_NAME}"
  elif [[ $CLUSTER_PROVIDER == "kwok" ]]; then
    util::create_host_kwok_cluster "${HOST_CLUSTER_NAME}" "${KUBECONFIG_PATH}"
    HOST_CONTEXT="kwok-${HOST_CLUSTER_NAME}"
  else
    echo "Invalid provider, only kwok or kind allowed"
    exit 1
  fi

  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" create -f "${MANIFEST_DIR}"
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" create -f "${CONFIG_DIR}"

  NOFED=kubeadmiral.io/no-federated-resource=1
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate deploy -A --all $NOFED
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate daemonset -A --all $NOFED
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate cm -A --all $NOFED
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate secret -A --all $NOFED
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate role -A --all $NOFED
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate rolebinding -A --all $NOFED
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate clusterrole -A --all $NOFED
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate clusterrolebinding -A --all $NOFED
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate svc -A --all $NOFED
}

function util::create_host_kind_cluster() {
  local HOST_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}

  local KIND_CONFIG=$(cat <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  image: kindest/node:v1.20.15
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    controllerManager:
      extraArgs:
        controllers: "namespace,garbagecollector"
    apiServer:
      extraArgs:
        disable-admission-plugins: StorageObjectInUseProtection
EOF
)

  kind create cluster --config=<(echo "${KIND_CONFIG}") --name="${HOST_CLUSTER_NAME}" --kubeconfig="${KUBECONFIG_PATH}"
}

function util::create_host_kwok_cluster() {
  local HOST_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}
  local APISERVER_PORT=$(( $RANDOM + 30000 ))

  local KWOK_CONFIG=$(cat <<EOF
kind: KwokctlConfiguration
apiVersion: config.kwok.x-k8s.io/v1alpha1
options:
  kubeApiserverPort: ${APISERVER_PORT}
  kubeVersion: "v1.20.15"
componentsPatches:
# we do not need to disable StorageObjectInUseProtection explictly for the apiserver because kwok disables admission plugins by default
- name: kube-controller-manager
  extraArgs:
  - key: controllers
    value: "namespace,garbagecollector"
EOF
)

  KUBECONFIG=${KUBECONFIG_PATH} kwokctl create cluster --name="${HOST_CLUSTER_NAME}" --config=<(echo "${KWOK_CONFIG}")
}

function util::create_member_cluster() {
  local MEMBER_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}

  if [[ $CLUSTER_PROVIDER == "kind" ]]; then
    kind create cluster --image=kindest/node:v1.20.15 --name="${MEMBER_CLUSTER_NAME}" --kubeconfig="${KUBECONFIG_PATH}"
  elif [[ $CLUSTER_PROVIDER == "kwok" ]]; then
    local APISERVER_PORT=$(( $RANDOM + 30000 ))
    KUBECONFIG=${KUBECONFIG_PATH} KWOK_KUBE_VERSION="v1.20.15" kwokctl create cluster --name=${MEMBER_CLUSTER_NAME} --kube-apiserver-port=${APISERVER_PORT} --kube-authorization
    for i in $(seq 1 10); do
      kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kwok-${MEMBER_CLUSTER_NAME}" apply -f - <<EOF
apiVersion: v1
kind: Node
metadata:
  annotations:
    node.alpha.kubernetes.io/ttl: "0"
    kwok.x-k8s.io/node: fake
  labels:
    beta.kubernetes.io/arch: amd64
    beta.kubernetes.io/os: linux
    kubernetes.io/arch: amd64
    kubernetes.io/hostname: kwok-node-0
    kubernetes.io/os: linux
    kubernetes.io/role: agent
    node-role.kubernetes.io/agent: ""
    type: kwok
  name: kwok-node-${i}
status:
  allocatable:
    cpu: 32
    memory: 256Gi
    pods: 110
  capacity:
    cpu: 32
    memory: 256Gi
    pods: 110
  nodeInfo:
    architecture: amd64
    bootID: ""
    containerRuntimeVersion: ""
    kernelVersion: ""
    kubeProxyVersion: fake
    kubeletVersion: fake
    machineID: ""
    operatingSystem: linux
    osImage: ""
    systemUUID: ""
  phase: Running
EOF
    done
  else
    echo "Invalid provider, only kwok or kind allowed"
    exit 1
  fi
}

function util::join_member_cluster() {
  local MEMBER_CLUSTER_NAME=${1}
  local HOST_CLUSTER_NAME=${2}
  local KUBECONFIG_PATH=${3}

  local HOST_CONTEXT MEMBER_API_ENDPOINT MEMBER_CA MEMBER_CERT MEMBER_KEY
  if [[ $CLUSTER_PROVIDER == "kind" ]]; then
    HOST_CONTEXT="kind-${HOST_CLUSTER_NAME}"
    MEMBER_API_ENDPOINT=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'server:' | awk '{ print $2 }')
    MEMBER_CA=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'certificate-authority-data:' | awk '{ print $2 }')
    MEMBER_CERT=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-certificate-data:' | awk '{ print $2 }')
    MEMBER_KEY=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-key-data:' | awk '{ print $2 }')
  elif [[ $CLUSTER_PROVIDER == "kwok" ]]; then
    HOST_CONTEXT="kwok-${HOST_CLUSTER_NAME}"
    MEMBER_API_ENDPOINT=$(kwokctl get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'server:' | awk '{ print $2 }')
    MEMBER_CA=$(cat ${KWOK_WORKDIR:-"${HOME}/.kwok/clusters/${MEMBER_CLUSTER_NAME}/pki/ca.crt"} | base64 | tr -d '\n')
    MEMBER_CERT=$(kwokctl get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-certificate-data:' | awk '{ print $2 }')
    MEMBER_KEY=$(kwokctl get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-key-data:' | awk '{ print $2 }')
  else
    echo "Invalid provider, only kwok or kind allowed"
    exit 1
  fi

  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" create -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${MEMBER_CLUSTER_NAME}
  namespace: kube-admiral-system
type: Opaque
data:
  certificate-authority-data: "${MEMBER_CA}"
  client-certificate-data: "${MEMBER_CERT}"
  client-key-data: "${MEMBER_KEY}"
EOF
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" create -f - <<EOF
apiVersion: core.kubeadmiral.io/v1alpha1
kind: FederatedCluster
metadata:
  labels:
    cluster: ${MEMBER_CLUSTER_NAME}
    name: ${MEMBER_CLUSTER_NAME}
    idc: kind
    vdc: kind
  name: ${MEMBER_CLUSTER_NAME}
  namespace: kube-admiral-system
spec:
  apiEndpoint: ${MEMBER_API_ENDPOINT}
  useServiceAccount: true
  secretRef:
    name: ${MEMBER_CLUSTER_NAME}
EOF
}

