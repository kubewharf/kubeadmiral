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

  local KIND_CONFIG
  KIND_CONFIG=$(mktemp)
  cat <<EOF > "${KIND_CONFIG}"
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

  kind create cluster --config="${KIND_CONFIG}" --name="${HOST_CLUSTER_NAME}" --kubeconfig="${KUBECONFIG_PATH}"
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" create -f "${MANIFEST_DIR}"
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" create -f "${CONFIG_DIR}"

	NOFED=kubeadmiral.io/no-federated-resource=1
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate deploy -A --all $NOFED
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate daemonset -A --all $NOFED
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate cm -A --all $NOFED
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate secret -A --all $NOFED
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate role -A --all $NOFED
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate rolebinding -A --all $NOFED
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate clusterrole -A --all $NOFED
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate clusterrolebinding -A --all $NOFED
	kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="kind-${HOST_CLUSTER_NAME}" annotate svc -A --all $NOFED

  rm "${KIND_CONFIG}"
}

function util::create_member_cluster() {
  local MEMBER_CLUSTER_NAME=${1}
  local KUBECONFIG_PATH=${2}

  kind create cluster --image=kindest/node:v1.20.15 --name="${MEMBER_CLUSTER_NAME}" --kubeconfig="${KUBECONFIG_PATH}"
}

function util::join_member_cluster() {
  local MEMBER_CLUSTER_NAME=${1}
  local HOST_CLUSTER_NAME=${2}
  local KUBECONFIG_PATH=${3}

  local HOST_CLUSTER_KUBECONFIG
  HOST_CLUSTER_KUBECONFIG=$(mktemp)
  kind get kubeconfig --name="${HOST_CLUSTER_NAME}" > "${HOST_CLUSTER_KUBECONFIG}"

  local MEMBER_API_ENDPOINT MEMBER_CA MEMBER_CERT MEMBER_KEY
  MEMBER_API_ENDPOINT=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'server:' | awk '{ print $2 }')
  MEMBER_CA=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'certificate-authority-data:' | awk '{ print $2 }')
  MEMBER_CERT=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-certificate-data:' | awk '{ print $2 }')
  MEMBER_KEY=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-key-data:' | awk '{ print $2 }')

  local FCLUSTER FCLUSTER_SECRET
  FCLUSTER=$(mktemp)
  FCLUSTER_SECRET=$(mktemp)

  cat <<EOF > "${FCLUSTER}"
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

  cat <<EOF > "${FCLUSTER_SECRET}"
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

  kubectl --kubeconfig="${HOST_CLUSTER_KUBECONFIG}" --context="kind-${HOST_CLUSTER_NAME}" create -f "${FCLUSTER_SECRET}"
  kubectl --kubeconfig="${HOST_CLUSTER_KUBECONFIG}" --context="kind-${HOST_CLUSTER_NAME}" create -f "${FCLUSTER}"

  rm "${HOST_CLUSTER_KUBECONFIG}"
  rm "${FCLUSTER}"
  rm "${FCLUSTER_SECRET}"
}
