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

set -eu
set -o pipefail

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/../..
source "${REPO_ROOT}"/hack/lib/util.sh
source "${REPO_ROOT}"/hack/lib/dev-up.sh

KUBECONFIG_DIR=${KUBECONFIG_DIR:-"${HOME}/.kube/kubeadmiral"}
HOST_CLUSTER_NAME=${HOST_CLUSTER_NAME:-"kubeadmiral-host"}
MEMBER_CLUSTER_NAME=${MEMBER_CLUSTER_NAME:-"kubeadmiral-member"}
MANIFEST_DIR=${MANIFEST_DIR:-"${REPO_ROOT}/config/crds"}
CONFIG_DIR=${CONFIG_DIR:-"${REPO_ROOT}/config/sample/host"}
NUM_MEMBER_CLUSTERS=${NUM_MEMBER_CLUSTERS:-"3"}
CLUSTER_PROVIDER=${CLUSTER_PROVIDER:-"kind"}
KUBE_VERSION=${KUBE_VERSION:="v1.20.15"}

if [[ "${NUM_MEMBER_CLUSTERS}" -gt "0" ]]; then
  if [[ $CLUSTER_PROVIDER == "kind" ]]; then
    kind delete cluster --name=${HOST_CLUSTER_NAME}
    for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
      kind delete cluster --name="${MEMBER_CLUSTER_NAME}-${i}"
    done
  elif [[ $CLUSTER_PROVIDER == "kwok" ]]; then
    kwokctl delete cluster --name=${HOST_CLUSTER_NAME} || true
    for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
        kwokctl delete cluster --name="$MEMBER_CLUSTER_NAME-${i}" || true
    done
  else
    echo "Invalid provider, only kwok or kind allowed"
    exit 1
  fi
fi

mkdir -p "$(dirname "${KUBECONFIG_DIR}")"

# start host cluster
dev-up::create_host_cluster "${HOST_CLUSTER_NAME}" "${KUBECONFIG_DIR}/${HOST_CLUSTER_NAME}.yaml" "${MANIFEST_DIR}" "${CONFIG_DIR}" &

if [[ "${NUM_MEMBER_CLUSTERS}" -gt "0" ]]; then
  # start member clusters
  for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
      dev-up::create_member_cluster "${MEMBER_CLUSTER_NAME}-${i}" "${KUBECONFIG_DIR}/${MEMBER_CLUSTER_NAME}-${i}.yaml" &
  done

  wait

  # join the member clusters
  HOST_CONTEXT=""
  if [[ $CLUSTER_PROVIDER == "kind" ]]; then
    HOST_CONTEXT="kind-${HOST_CLUSTER_NAME}"
  elif [[ $CLUSTER_PROVIDER == "kwok" ]]; then
    HOST_CONTEXT="kwok-${HOST_CLUSTER_NAME}"
  else
    echo "Invalid provider, only kwok or kind allowed"
    exit 1
  fi
  # join the member clusters
  for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
    util::join_member_cluster "${MEMBER_CLUSTER_NAME}-${i}" "${HOST_CONTEXT}" "${KUBECONFIG_DIR}/${HOST_CLUSTER_NAME}.yaml" "${KUBECONFIG_DIR}/${MEMBER_CLUSTER_NAME}-${i}.yaml" "${CLUSTER_PROVIDER}" &
  done
fi

wait
