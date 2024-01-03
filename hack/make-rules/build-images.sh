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

components=(
  kubeadmiral-controller-manager
  kubeadmiral-hpa-aggregator
)

REGISTRY=${REGISTRY:-"ghcr.io/kubewharf"}
TAG=${TAG:-"unknown"}
DOCKERFILE_PATH=${DOCKERFILE_PATH:-"${REPO_ROOT}/hack/dockerfiles/Dockerfile"}
ARCHS=${ARCHS:-"$(go env GOARCH)"}
TARGET_NAME=${TARGET_NAME:-"all"}

# append ‘linux/’ string at the beginning of each arch_array element
IFS="," read -ra arch_array <<< "${ARCHS}"
platform_array=("${arch_array[@]/#/linux/}")
PLATFORMS=$(IFS=","; echo "${platform_array[*]}")
REGISTRY=${REGISTRY:-"ghcr.io/kubewharf"}
OUTPUT_TYPE=${OUTPUT_TYPE:-"docker"}
GOPROXY=${GOPROXY:-$(go env GOPROXY)}
REGION=${REGION:-""}
DOCKER_BUILD_ARGS="${DOCKER_BUILD_ARGS:-} --build-arg GOPROXY=${GOPROXY} --build-arg REGION=${REGION}"

build_count=0
if [[ ${#arch_array[@]} -gt 1 ]]; then
  # If you build images for multiple platforms at one time, the image tag will be added with the architecture name.
  for arch in ${arch_array[@]}; do
    for component in "${components[@]}"; do
      if [[ ${TARGET_NAME} =~ "all" || ${TARGET_NAME} =~ ${component} ]]; then
        build_args="${DOCKER_BUILD_ARGS} --build-arg COMPENT=${component}"
        build::build_images "${REGISTRY}/${component}:${TAG}-${arch}" ${DOCKERFILE_PATH} "linux/${arch}" ${OUTPUT_TYPE} "${build_args}"
      fi
    done
  done
else
  for component in "${components[@]}"; do
    if [[ ${TARGET_NAME} =~ "all" || ${TARGET_NAME} =~ ${component} ]]; then
      build_args="${DOCKER_BUILD_ARGS} --build-arg COMPENT=${component}"
      build::build_images "${REGISTRY}/${component}:${TAG}" ${DOCKERFILE_PATH} "${PLATFORMS}" ${OUTPUT_TYPE} "${build_args}"
    fi
  done
fi
