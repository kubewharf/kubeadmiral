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

function dev-up::create_host_cluster() {
  local HOST_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}
  local MANIFEST_DIR=${3}
  local CONFIG_DIR=${4}
  local HOST_CONTEXT

  if [[ $CLUSTER_PROVIDER == "kind" ]]; then
    dev-up::create_host_kind_cluster "${HOST_CLUSTER_NAME}" "${KUBECONFIG_PATH}"
    HOST_CONTEXT="kind-${HOST_CLUSTER_NAME}"
  elif [[ $CLUSTER_PROVIDER == "kwok" ]]; then
    dev-up::create_host_kwok_cluster "${HOST_CLUSTER_NAME}" "${KUBECONFIG_PATH}"
    HOST_CONTEXT="kwok-${HOST_CLUSTER_NAME}"
  else
    echo "Invalid provider, only kwok or kind allowed"
    exit 1
  fi

  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" create -f "${MANIFEST_DIR}"
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" create -f "${CONFIG_DIR}"

  NOFED=kubeadmiral.io/no-federated-resource=1
  local target_array=(deploy daemonset cm secret role rolebinding clusterrole clusterrolebinding svc)
  for target in ${target_array[@]}; do
    kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CONTEXT}" annotate ${target} -A --all ${NOFED}
  done
}

function dev-up::create_host_kind_cluster() {
  local HOST_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}

  local KIND_CONFIG=$(cat <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  image: kindest/node:${KUBE_VERSION}
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    controllerManager:
      extraArgs:
        controllers: "namespace,garbagecollector"
    apiServer:
      extraArgs:
        disable-admission-plugins: StorageObjectInUseProtection,ServiceAccount
EOF
)

  kind create cluster --config=<(echo "${KIND_CONFIG}") --name="${HOST_CLUSTER_NAME}" --kubeconfig="${KUBECONFIG_PATH}"

  # ideally we would use InitConfiguration.skipPhases in kubeadmConfigPatches above to disable the kube-scheduler, but
  # it is only avaiable from v1.22 onwards, so we simply delete the static kube-scheduler pod after the kind cluster is
  # created for now
  docker exec ${HOST_CLUSTER_NAME}-control-plane rm /etc/kubernetes/manifests/kube-scheduler.yaml
}

function dev-up::create_host_kwok_cluster() {
  local HOST_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}
  local APISERVER_PORT=$(( $RANDOM + 30000 ))

  local KWOK_CONFIG=$(cat <<EOF
kind: KwokctlConfiguration
apiVersion: config.kwok.x-k8s.io/v1alpha1
options:
  kubeApiserverPort: ${APISERVER_PORT}
  kubeVersion: "${KUBE_VERSION}"
  disableKubeScheduler: true
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

function dev-up::create_member_cluster() {
  local MEMBER_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}

  if [[ $CLUSTER_PROVIDER == "kind" ]]; then
    kind create cluster --image="kindest/node:${KUBE_VERSION}" --name="${MEMBER_CLUSTER_NAME}" --kubeconfig="${KUBECONFIG_PATH}"
  elif [[ $CLUSTER_PROVIDER == "kwok" ]]; then
    local APISERVER_PORT=$(( $RANDOM + 30000 ))
    KUBECONFIG=${KUBECONFIG_PATH} KWOK_KUBE_VERSION="${KUBE_VERSION}" kwokctl create cluster --name=${MEMBER_CLUSTER_NAME} --kube-apiserver-port=${APISERVER_PORT} --kube-authorization
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
