#!/usr/bin/env bash

# This file is based on https://github.com/karmada-io/karmada/blob/master/hack/deploy-karmada.sh
# Copyright 2023 The Karmada Authors.
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
#
# This file may have been modified by The KubeAdmiral Authors
# ("KubeAdmiral Modifications"). All KubeAdmiral Modifications
# are Copyright 2023 The KubeAdmiral Authors.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
CONTROLPLANE_DEPLOY_PATH="${REPO_ROOT}/config/deploy/controlplane"
source "${REPO_ROOT}/hack/lib/deploy-kubeadmiral.sh"

function usage() {
  echo "This script deploys KubeAdmiral control plane components to a given cluster."
  echo "Note: This script is an internal script and is not intended used by end-users."
  echo "Usage: hack/make-rules/deploy-kubeadmiral.sh <META_CLUSTER_KUBECONFIG> <CONTEXT_NAME> <HOST_CLUSTER_KUBECONFIG>"
  echo "Example: hack/deploy-kubeadmiral.sh ~/.kube/config kubeadmiral-meta "
  echo -e "Parameters:\n\tMETA_CLUSTER_KUBECONFIG\t\t Your cluster's kubeconfig that you want to install to"
  echo -e "\tCONTEXT_NAME\t\t The context name in kubeconfig which used to deploy the kubeadmiral control-plane"
  echo -e "\tHOST_CLUSTER_KUBECONFIG\t\t The kubeconfig path to connect to kubeadmiral"
}

# check config file existence
META_CLUSTER_KUBECONFIG=$1
if [[ ! -f "${META_CLUSTER_KUBECONFIG}" ]]; then
  echo -e "ERROR: failed to get kubernetes config file: '${META_CLUSTER_KUBECONFIG}', not existed.\n"
  usage
  exit 1
fi
META_CLUSTER_NAME=${2:-"kubeadmiral-meta"}
if ! kubectl --kubeconfig="${META_CLUSTER_KUBECONFIG}" config get-contexts "${META_CLUSTER_NAME}" > /dev/null 2>&1;
then
  echo -e "ERROR: failed to get context: "${META_CLUSTER_NAME}"."
  usage
  exit 1
fi
HOST_CLUSTER_KUBECONFIG=$3
HOST_CLUSTER_CONTEXT=${HOST_CLUSTER_CONTEXT:-"kubeadmiral-host"}

# 1. generate certificates and create secrets to save the certificate information in meta cluster
CERT_DIR=${CERT_DIR:-"${REPO_ROOT}/output/cert"}
mkdir -p "${CERT_DIR}" &>/dev/null ||  mkdir -p "${CERT_DIR}"
rm -f "${CERT_DIR}/*" &>/dev/null ||  rm -f "${CERT_DIR}/*"
ROOT_CA_FILE=${CERT_DIR}/ca.crt
ROOT_CA_KEY=${CERT_DIR}/ca.key
CFSSL_VERSION="v1.6.4"
echo -n "Preparing: 'openssl' existence check - "
if util::cmd_exist "openssl"; then
  echo "passed"
else
  echo "not pass"
  echo "Please install 'openssl' and verify it is in \$PATH."
fi

deploy::ensure_cfssl ${CFSSL_VERSION}

# 1.1 generate cert
# create CA signers
deploy::create_signing_certkey "" "${CERT_DIR}" ca kubeadmiral '"client auth","server auth"'
deploy::create_signing_certkey "" "${CERT_DIR}" front-proxy-ca front-proxy-ca '"client auth","server auth"'
deploy::create_signing_certkey "" "${CERT_DIR}" etcd-ca etcd-ca '"client auth","server auth"'
# sign a certificate
deploy::create_certkey "" "${CERT_DIR}" "ca" kubeadmiral system:admin "system:masters" kubernetes.default.svc "*.etcd.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc" "localhost" "127.0.0.1"
deploy::create_certkey "" "${CERT_DIR}" "ca" apiserver kubeadmiral-apiserver "" "*.etcd.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc" "localhost" "127.0.0.1" $(deploy::get_apiserver_ip_from_kubeconfig "${META_CLUSTER_NAME}" "${META_CLUSTER_KUBECONFIG}")
deploy::create_certkey "" "${CERT_DIR}" "front-proxy-ca" front-proxy-client front-proxy-client "" kubernetes.default.svc "*.etcd.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc" "localhost" "127.0.0.1"
deploy::create_certkey "" "${CERT_DIR}" "etcd-ca" etcd-server etcd-server "" kubernetes.default.svc "*.etcd.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc" "localhost" "127.0.0.1"
deploy::create_certkey "" "${CERT_DIR}" "etcd-ca" etcd-client etcd-client "" "*.etcd.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc.cluster.local" "*.kubeadmiral-system.svc" "localhost" "127.0.0.1"

# 1.2 create namespace for control plane components
kubectl --kubeconfig="${META_CLUSTER_KUBECONFIG}" --context="${META_CLUSTER_NAME}" apply -f "${CONTROLPLANE_DEPLOY_PATH}/kubeadmiral-namespace.yaml"

# 1.3 create secrets in the kubeadmiral-system namespace of meta cluster
KUBEADMIRAL_CRT=$(base64 -i "${CERT_DIR}/kubeadmiral.crt" | tr -d '\r\n')
KUBEADMIRAL_KEY=$(base64 -i "${CERT_DIR}/kubeadmiral.key" | tr -d '\r\n')

KUBEADMIRAL_APISERVER_CRT=$(base64 -i "${CERT_DIR}/apiserver.crt" | tr -d '\r\n')
KUBEADMIRAL_APISERVER_KEY=$(base64 -i "${CERT_DIR}/apiserver.key" | tr -d '\r\n')

FRONT_PROXY_CA_CRT=$(base64 -i "${CERT_DIR}/front-proxy-ca.crt" | tr -d '\r\n')
FRONT_PROXY_CLIENT_CRT=$(base64 -i "${CERT_DIR}/front-proxy-client.crt" | tr -d '\r\n')
FRONT_PROXY_CLIENT_KEY=$(base64 -i "${CERT_DIR}/front-proxy-client.key" | tr -d '\r\n')

ETCD_CA_CRT=$(base64 -i "${CERT_DIR}/etcd-ca.crt" | tr -d '\r\n')
ETCD_SERVER_CRT=$(base64 -i "${CERT_DIR}/etcd-server.crt" | tr -d '\r\n')
ETCD_SERVER_KEY=$(base64 -i "${CERT_DIR}/etcd-server.key" | tr -d '\r\n')
ETCD_CLIENT_CRT=$(base64 -i "${CERT_DIR}/etcd-client.crt" | tr -d '\r\n')
ETCD_CLIENT_KEY=$(base64 -i "${CERT_DIR}/etcd-client.key" | tr -d '\r\n')
deploy::generate_cert_secret "${ROOT_CA_FILE}" "${ROOT_CA_KEY}" "${CONTROLPLANE_DEPLOY_PATH}" "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}"

# 2. deploy k8s control plane components
KUBEADMIRAL_SYSTEM_NAMESPACE="kubeadmiral-system"
ETCD_POD_LABEL="app=etcd"
KUBE_APISERVER_POD_LABEL="app=kubeadmiral-apiserver"
KUBE_CONTROLLER_MANAGER_LABEL="app=kubeadmiral-kube-controller-manager"
KUBEADMIRAL_CONTROLLER_MANAGER_LABEL="app=kubeadmiral-controller-manager"
KUBEADMIRAL_HPA_AGGREGATOR_LABEL="app=kubeadmiral-hpa-aggregator"

# 2.1 deploy kubeadmiral etcd
echo -e "\nDeploying the kubeadmiral-etcd."
util::deploy_components_according_to_different_region "${REGION}" "${CONTROLPLANE_DEPLOY_PATH}/etcd.yaml" "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}"

# Wait for kubeadmiral-etcd to come up before launching the rest of the components.
deploy::wait_pod_ready "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}" "${ETCD_POD_LABEL}" "${KUBEADMIRAL_SYSTEM_NAMESPACE}"

# 2.2 deploy kube-apiserver
echo -e "\nDeploying the kubeadmiral-apiserver."
KUBEADMIRAL_APISERVER_IP=$(deploy::get_apiserver_ip_from_kubeconfig "${META_CLUSTER_NAME}" "${META_CLUSTER_KUBECONFIG}")
util::deploy_components_according_to_different_region "${REGION}" "${CONTROLPLANE_DEPLOY_PATH}/kube-apiserver.yaml" "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}"

# Wait for kubeadmiral-apiserver to come up before launching the rest of the components.
deploy::wait_pod_ready "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}" "${KUBE_APISERVER_POD_LABEL}" "${KUBEADMIRAL_SYSTEM_NAMESPACE}"

if [[ -n "${KUBEADMIRAL_APISERVER_IP}" ]]; then
  echo -e "\nKubeAdmiral API Server's IP is: ${KUBEADMIRAL_APISERVER_IP}"
else
  echo -e "\nERROR: failed to get KubeAdmiral API server IP after creating service 'kubeadmiral-apiserver', please verify."
  exit 1
fi

KUBEADMIRAL_APISERVER_SECURE_PORT=${KUBEADMIRAL_APISERVER_SECURE_PORT:-5443}
# write kubeadmiral-apiserver config to HOST_CLUSTER_KUBECONFIG file
deploy::create_client_kubeconfig "${HOST_CLUSTER_KUBECONFIG}" "${CERT_DIR}/kubeadmiral.crt" "${CERT_DIR}/kubeadmiral.key" "${KUBEADMIRAL_APISERVER_IP}" "${KUBEADMIRAL_APISERVER_SECURE_PORT}" "${HOST_CLUSTER_CONTEXT}"

# 2.3 deploy kube-controller-manager
echo -e "\nDeploying the kubeadmiral-kube-controller-manager."
util::deploy_components_according_to_different_region "${REGION}" "${CONTROLPLANE_DEPLOY_PATH}/kube-controller-manager.yaml" "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}"
deploy::wait_pod_ready "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}" "${KUBE_CONTROLLER_MANAGER_LABEL}" "${KUBEADMIRAL_SYSTEM_NAMESPACE}"

# 3. install CRD APIs on kubeadmiral control plane
if ! kubectl --kubeconfig="${HOST_CLUSTER_KUBECONFIG}" config get-contexts "${HOST_CLUSTER_CONTEXT}" > /dev/null 2>&1;
then
  echo -e "ERROR: failed to get context: ${HOST_CLUSTER_CONTEXT}."
  usage
  exit 1
fi

MANIFEST_DIR=${MANIFEST_DIR:-"${REPO_ROOT}/config/crds"}
CONFIG_DIR=${CONFIG_DIR:-"${REPO_ROOT}/config/sample/host"}
kubectl --kubeconfig="${HOST_CLUSTER_KUBECONFIG}" --context="${HOST_CLUSTER_CONTEXT}" create -f "${MANIFEST_DIR}"
kubectl --kubeconfig="${HOST_CLUSTER_KUBECONFIG}" --context="${HOST_CLUSTER_CONTEXT}" create -f "${CONFIG_DIR}"

# 4. annotate 'no-federated-resource' on some types of resources in the host cluster
NOFED="kubeadmiral.io/no-federated-resource=1"
TARGET_ARRAY=(deploy daemonset cm secret role rolebinding clusterrole clusterrolebinding svc)
for target in ${TARGET_ARRAY[@]}; do
  kubectl --kubeconfig="${HOST_CLUSTER_KUBECONFIG}" --context="${HOST_CLUSTER_CONTEXT}" annotate ${target} -A --all ${NOFED}
done

# 5. deploy kubeadmiral-controller-manager component
echo -e "\nDeploying the kubeadmiral-controller-manager."
kubectl --kubeconfig="${META_CLUSTER_KUBECONFIG}" --context="${META_CLUSTER_NAME}" apply -f "${CONTROLPLANE_DEPLOY_PATH}/kubeadmiral-controller-manager.yaml"
deploy::wait_pod_ready "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}" "${KUBEADMIRAL_CONTROLLER_MANAGER_LABEL}" "${KUBEADMIRAL_SYSTEM_NAMESPACE}"

# 6. deploy kubeadmiral-hpa-aggregator component
echo -e "\nDeploying the kubeadmiral-hpa-aggregator."
kubectl --kubeconfig="${HOST_CLUSTER_KUBECONFIG}" --context="${HOST_CLUSTER_CONTEXT}" apply -f "${CONTROLPLANE_DEPLOY_PATH}/kubeadmiral-hpa-aggregator-apiservice.yaml"
kubectl --kubeconfig="${META_CLUSTER_KUBECONFIG}" --context="${META_CLUSTER_NAME}" apply -f "${CONTROLPLANE_DEPLOY_PATH}/kubeadmiral-hpa-aggregator.yaml"
deploy::wait_pod_ready "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}" "${KUBEADMIRAL_HPA_AGGREGATOR_LABEL}" "${KUBEADMIRAL_SYSTEM_NAMESPACE}"
kubectl config set-cluster hpa-aggregator-api \
  --server="https://${KUBEADMIRAL_APISERVER_IP}:${KUBEADMIRAL_APISERVER_SECURE_PORT}/apis/hpaaggregator.kubeadmiral.io/v1alpha1/aggregations/hpa/proxy" \
  --insecure-skip-tls-verify=true \
  --kubeconfig="${HOST_CLUSTER_KUBECONFIG}"
kubectl config set-context hpa-aggregator --cluster=hpa-aggregator-api --user="${HOST_CLUSTER_CONTEXT}" --kubeconfig="${HOST_CLUSTER_KUBECONFIG}"
