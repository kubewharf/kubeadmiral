#!/usr/bin/env bash

# This file is based on https://github.com/karmada-io/karmada/blob/master/hack/local-up-karmada.sh
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
DEPLOY_DIR="${REPO_ROOT}/config/deploy"
source "${REPO_ROOT}/hack/lib/util.sh"
source "${REPO_ROOT}/hack/lib/local-up.sh"

# variable define
KUBECONFIG_PATH=${KUBECONFIG_PATH:-"${HOME}/.kube/kubeadmiral"}
META_CLUSTER_NAME=${META_CLUSTER_NAME:-"kubeadmiral-meta"}
HOST_CLUSTER_CONTEXT=${HOST_CLUSTER_CONTEXT:-"kubeadmiral-host"}
META_CLUSTER_KUBECONFIG=${META_CLUSTER_KUBECONFIG:-"${KUBECONFIG_PATH}/meta.config"}
HOST_CLUSTER_KUBECONFIG=${HOST_CLUSTER_KUBECONFIG:-"${KUBECONFIG_PATH}/kubeadmiral.config"}
KIND_LOG_PATH=${KIND_LOG_PATH:-"${REPO_ROOT}/output/log"}
MEMBER_CLUSTER_NAME_PREFIX=${MEMBER_CLUSTER_NAME_PREFIX:-"kubeadmiral-member"}
MEMBER_CLUSTER_KUBECONFIG_PREFIX=${MEMBER_CLUSTER_KUBECONFIG_PREFIX:-"${KUBECONFIG_PATH}/member"}
NUM_MEMBER_CLUSTERS=${NUM_MEMBER_CLUSTERS:-"3"}

if [[ "${NUM_MEMBER_CLUSTERS}" -le "0" ]]; then
  echo "In order to demonstrate resource distribution capabilities, at least one member-cluster is required, so the value of NUM_MEMBER_CLUSTERS is set to 1."
  NUM_MEMBER_CLUSTERS=1
fi

# 1. Ensure environment
# make sure go exists and the go version is a viable version.
echo -n "Preparing: 'go' existence check - "
if util::ensure_go_version "go1.19.0"; then
  echo "passed"
else
  echo "Please install 'go' and verify it is in \$PATH which version is greater than 1.19.0. "
  exit 1
fi

# make sure docker exists and the docker version is a viable version.
# docker buildx need > 19.03.0
echo -n "Preparing: 'docker' existence check - "
if util::ensure_docker_version "19.03.0"; then
  echo "passed"
else
  echo "Please install 'docker' and verify the version is greater than 19.03.0. "
  exit 1
fi

# install kind and kubectl
# get and check arch and os name before installing, only support the linux/macos, amd64/arm64
BS_ARCH=$(go env GOARCH)
BS_OS=$(go env GOOS)
kind_version=v0.18.0
echo -n "Preparing: 'kind' existence check - "
if util::cmd_exist kind; then
  echo "passed"
else
  echo "not pass"
  util::install_tools "sigs.k8s.io/kind" "${kind_version}"
fi

echo -n "Preparing: 'kubectl' existence check - "
if util::cmd_exist kubectl; then
  echo "passed"
else
  echo "not pass"
  util::install_kubectl "" "${BS_ARCH}" "${BS_OS}"
fi

GOPROXY=${GOPROXY:-$(go env GOPROXY)}
# proxy setting in China mainland
if [ "${REGION}" == "cn" ]; then
  export GOPROXY=https://goproxy.cn,direct # set domestic go proxy
fi

# 2. Create meta cluster and member clusters
# host IP address: script parameter ahead of macOS IP
HOST_IPADDRESS=${1:-}
if [[ -z "${HOST_IPADDRESS}" ]]; then
  local-up::get_macos_ipaddress # Adapt for macOS
  HOST_IPADDRESS=${MAC_NIC_IPADDRESS:-}
fi

# prepare for kindClusterConfig
TEMP_PATH=$(mktemp -d)
trap '{ rm -rf ${TEMP_PATH}; }' EXIT
echo -e "Preparing KindClusterConfig in path: ${TEMP_PATH}"
cp -rf "${DEPLOY_DIR}/kind-cluster/member.yaml" "${TEMP_PATH}/member.yaml"
# If bind the port of clusters(kubeadmiral-host, kubeadmiral-member1/2/3) to the host IP
if [[ -n "${HOST_IPADDRESS}" ]]; then
  cp -rf "${DEPLOY_DIR}/kind-cluster/meta.yaml" "${TEMP_PATH}/meta.yaml"
  sed -i'' -e "s/{{meta_ipaddress}}/${HOST_IPADDRESS}/g" "${TEMP_PATH}/meta.yaml"
  local-up::create_cluster "${META_CLUSTER_NAME}" "${META_CLUSTER_KUBECONFIG}" "${KIND_LOG_PATH}" "${TEMP_PATH}/meta.yaml"

  sed -i'' -e "s/{{member_ipaddress}}/${HOST_IPADDRESS}/g" "${TEMP_PATH}/member.yaml"
  for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
    local-up::create_cluster "${MEMBER_CLUSTER_NAME_PREFIX}-${i}" "${MEMBER_CLUSTER_KUBECONFIG_PREFIX}-${i}.config" "${KIND_LOG_PATH}" "${TEMP_PATH}/member.yaml"
  done
else
  local-up::create_cluster "${META_CLUSTER_NAME}" "${META_CLUSTER_KUBECONFIG}" "${KIND_LOG_PATH}" "${DEPLOY_DIR}/kind-cluster/general-config.yaml"

  for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
    local-up::create_cluster "${MEMBER_CLUSTER_NAME_PREFIX}-${i}" "${MEMBER_CLUSTER_KUBECONFIG_PREFIX}-${i}.config" "${KIND_LOG_PATH}" "${DEPLOY_DIR}/kind-cluster/general-config.yaml"
  done
fi

# 3. Make image for kubeadmiral-controller-manager and load it to kind cluster
echo "Making a docker image using local kubeadmiral code..."
make images ARCHS=${BS_ARCH} TAG="latest" REGISTRY="ghcr.io/kubewharf" REGION=${REGION} GOPROXY=${GOPROXY}

echo "Waiting for the meta cluster to be ready..."
util::check_clusters_ready "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}"

kind load docker-image "ghcr.io/kubewharf/kubeadmiral-controller-manager:latest" --name="${META_CLUSTER_NAME}"
kind load docker-image "ghcr.io/kubewharf/kubeadmiral-hpa-aggregator:latest" --name="${META_CLUSTER_NAME}"

# 4. Install the KubeAdmiral control-plane components
bash "${REPO_ROOT}/hack/make-rules/deploy-kubeadmiral.sh" "${META_CLUSTER_KUBECONFIG}" "${META_CLUSTER_NAME}" "${HOST_CLUSTER_KUBECONFIG}"

# 5. Wait until the member cluster ready
echo "Waiting for the member clusters to be ready..."
for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
    util::check_clusters_ready "${MEMBER_CLUSTER_KUBECONFIG_PREFIX}-${i}.config" "${MEMBER_CLUSTER_NAME_PREFIX}-${i}"
done

# 6. Join the member clusters
echo "Adding member clusters to kubeadmiral..."
for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
  util::join_member_cluster "${MEMBER_CLUSTER_NAME_PREFIX}-${i}" "${HOST_CLUSTER_CONTEXT}" "${HOST_CLUSTER_KUBECONFIG}" "${MEMBER_CLUSTER_KUBECONFIG_PREFIX}-${i}.config" "kind"
done

function print_success() {
  echo -e "${KUBEADMIRAL_GREETING}"
  echo "Your local KubeAdmiral has been deployed successfully!"
  echo -e "\nTo start using your KubeAdmiral, run:"
  echo -e "  export KUBECONFIG=${HOST_CLUSTER_KUBECONFIG}"
  echo -e "\nTo observe the status of KubeAdmiral control-plane components, run:"
  echo -e "  export KUBECONFIG=${META_CLUSTER_KUBECONFIG}"
  echo -e "\nTo inspect your member clusters, run one of the following:"
  for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
      echo -e "  export KUBECONFIG=${MEMBER_CLUSTER_KUBECONFIG_PREFIX}-${i}.config"
  done

  echo -e "\nTo play with hpa-aggregator in KubeAdmiral, run:"
  echo -e "  export KUBECONFIG=${HOST_CLUSTER_KUBECONFIG}"
  echo -e "  kubectl config use-context hpa-aggregator && kubectl get hpa # change context to hpa-aggregator"
  echo -e "  kubectl config use-context ${HOST_CLUSTER_CONTEXT} # back to KubeAdmiral control-plane context"

  echo -e "\nEnjoy your trip!\n"
}

print_success
