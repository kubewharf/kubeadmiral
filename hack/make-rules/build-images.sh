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

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
source "${REPO_ROOT}/hack/lib/build.sh"
source "${REPO_ROOT}/hack/lib/util.sh"

REGISTRY=${REGISTRY:-"ghcr.io/kubewharf"}
COMPENT_NAME="kubeadmiral-controller-manager"
TAG=${TAG:-"unknown"}
DOCKERFILE_PATH=${REPO_ROOT}/hack/dockerfiles/Dockerfile
ARCHS=${ARCHS:-"$(go env GOARCH)"}

# append ‘linux/’ string at the beginning of each arch_array element
IFS="," read -ra arch_array <<< "${ARCHS}"
platform_array=("${arch_array[@]/#/linux/}")
PLATFORMS=$(IFS=","; echo "${platform_array[*]}")
REGISTRY=${REGISTRY:-"ghcr.io/kubewharf"}
OUTPUT_TYPE="docker"
DOCKER_BUILD_ARGS="${DOCKER_BUILD_ARGS:-}"
GOPROXY=${GOPROXY:-$(go env GOPROXY)}

if [[ ${#arch_array[@]} -gt 1 ]]; then
  # If you build images for multiple platforms at one time, the image tag will be added with the architecture name.
  for arch in ${arch_array[@]}; do
    build::build_images "${REGISTRY}/${COMPENT_NAME}:${TAG}-${arch}" ${DOCKERFILE_PATH} "linux/${arch}" ${OUTPUT_TYPE} "${DOCKER_BUILD_ARGS} --build-arg GOPROXY=${GOPROXY}"
  done
else
  build::build_images "${REGISTRY}/${COMPENT_NAME}:${TAG}" ${DOCKERFILE_PATH} "${PLATFORMS}" ${OUTPUT_TYPE} "${DOCKER_BUILD_ARGS} --build-arg GOPROXY=${GOPROXY}"
fi
