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

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
source "${REPO_ROOT}"/hack/util.sh

KUBECONFIG_PATH=${KUBECONFIG_PATH:-"${HOME}/.kube/kubeadmiral/kubeconfig.yaml"}
HOST_CLUSTER_NAME=${HOST_CLUSTER_NAME:-"kubeadmiral-host"}
MEMBER_CLUSTER_NAME=${MEMBER_CLUSTER_NAME:-"kubeadmiral-member"}
MANIFEST_DIR=${MANIFEST_DIR:-"${REPO_ROOT}/config/crds"}
CONFIG_DIR=${CONFIG_DIR:-"${REPO_ROOT}/config/sample/host"}
NUM_MEMBER_CLUSTERS=${NUM_MEMBER_CLUSTERS:-"3"}

mkdir -p "$(dirname "${KUBECONFIG_PATH}")"

kind delete clusters "${HOST_CLUSTER_NAME}" $(seq 1 "${NUM_MEMBER_CLUSTERS}" | xargs -I{} echo "${MEMBER_CLUSTER_NAME}-{}")

# start host cluster
util::create_host_cluster "${HOST_CLUSTER_NAME}" "${KUBECONFIG_PATH}" "${MANIFEST_DIR}" "${CONFIG_DIR}" &

# start member clusters
for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
  echo
  util::create_member_cluster "${MEMBER_CLUSTER_NAME}-${i}" "${KUBECONFIG_PATH}" &
done

wait

# join the member clusters
for i in $(seq 1 "${NUM_MEMBER_CLUSTERS}"); do
  util::join_member_cluster "${MEMBER_CLUSTER_NAME}-${i}" "${HOST_CLUSTER_NAME}" "${KUBECONFIG_PATH}"
done
