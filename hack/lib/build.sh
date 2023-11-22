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

REPO_ROOT=${REPO_ROOT:-"$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"}
source "${REPO_ROOT}/hack/lib/util.sh"

BUILD_OUTPUT_DIR=${BUILD_OUTPUT_DIR:-"${REPO_ROOT}/output/bin"}

BINARY_TARGET_SOURCE=(
  kubeadmiral-controller-manager=cmd/controller-manager
  kubeadmiral-hpa-aggregator=cmd/hpa-aggregator
)

# This function builds multi-platform binaries for kubeadmiral-controller-manager
# Args:
#  $1 - target, the name of the target binary
#  $2 - platforms, the array of platforms that need to be compiled, e.g.: linux/amd64,linux/arm64
#  $3 - output_dir, the output directory for binaries
# Environments:
#  GOPROXY:      it specifies the download address of the dependent package
#  GOLDFLAGS:    pass to the `-ldflags` parameter of go build
#  BUILD_FLAGS:  the other flags pass to go build
function build::build_multiplatform_binaries() {
  local -r target="${1:-"all"}"
  local -r platforms="${2:-"$(go env GOHOSTOS)/$(go env GOHOSTARCH)"}"
  local -r output_dir="${3:-"${BUILD_OUTPUT_DIR}"}"

  IFS="," read -ra platform_array <<< "${platforms}"
  for platform in "${platform_array[@]}"; do
    count=0
    for component in ${BINARY_TARGET_SOURCE[@]};do
      IFS="=" read -ra info <<< "${component}"
      if [[ ${target} =~ "all" || ${target} =~ ${info[0]} ]]; then
        build::build_binary "${info[0]}" "${platform}" "${output_dir}" "${info[1]}"
        count=$((count + 1))
      fi
    done

    if [[ ${count} == 0 ]]; then
      echo "Target component ${target} not found in [${BINARY_TARGET_SOURCE[@]}]"
      exit 1
    fi
  done
}

# This function build a single binary for target component
# Args:
#  $1 - target, the name of the target binary
#  $2 - platforms, the array of platforms that need to be compiled, e.g.: linux/amd64,linux/arm64
#  $3 - output_dir, the output directory for binary
#  $4 - source_dir, the source code path for binary
# Environments:
#  GOPROXY:      it specifies the download address of the dependent package
#  GOLDFLAGS:    pass to the `-ldflags` parameter of go build
#  BUILD_FLAGS:  the other flags pass to go build
function build::build_binary() {
  local -r target=$1
  local -r platforms="${2:-"$(go env GOHOSTOS)/$(go env GOHOSTARCH)"}"
  local -r output_dir="${3:-"${BUILD_OUTPUT_DIR}"}"
  local -r source_dir=$4
  local -r goldflags="${GOLDFLAGS:-}"
  local -r build_args="${BUILD_FLAGS:-}"
  local cgo_enabled=${CGO_ENABLED:-0}

  # '-race' flag requires cgo; enable cgo by setting CGO_ENABLED=1
  if [[ ${build_args} =~ "-race" ]]; then
    cgo_enabled=1
  fi

  echo "Building ${target} for ${platform}"
  set -x
  CGO_ENABLED=${cgo_enabled} GOPROXY=${GOPROXY:-"$(go env GOPROXY)"} GOOS=${platform%/*} GOARCH=${platform##*/} go build \
    -o "${output_dir}/${platform}/${target}" \
    -ldflags "${goldflags:-}" \
    ${build_args:-} \
    ${REPO_ROOT}/${source_dir}/main.go
  set +x
  echo ""
}

# This function builds multi-architecture docker images,
# Args:
#  $1 - image_name, the full name of image, include the name and tag.
#  $2 - dockerfile_path, the path of dockerfile.
#  $3 - platforms, the platforms that need to be compiled, e.g.: linux/amd64,linux/arm64,linux/arm
#  $4 - output_type, destination to save image(`docker`/`registry`/`local,dest=path`, default is `docker`).
#  $5 - docker_build_args, the extra docker build arguments.
function build::build_images() {
  local -r image_name=$1
  local -r dockerfile_path=$2
  local -r platforms=$3
  local -r output_type=${4:-"docker"}
  local -r docker_build_args=${5:-}

  echo "Building ${image_name} for ${platforms}"
  docker buildx build --no-cache --output=type=${output_type} \
    ${docker_build_args}  \
    --platform ${platforms} \
    --tag ${image_name} \
    --file ${dockerfile_path} \
    ${REPO_ROOT}
}
