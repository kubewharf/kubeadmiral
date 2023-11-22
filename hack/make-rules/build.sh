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

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
BUILD_OUTPUT_DIR=${BUILD_OUTPUT_DIR:-"${REPO_ROOT}/output/bin"}
TARGET_NAME=${TARGET_NAME:-"all"}
source "${REPO_ROOT}/hack/lib/build.sh"

# clean old binaries
rm -rf ${BUILD_OUTPUT_DIR}

# build multiplatform binaries
build::build_multiplatform_binaries ${TARGET_NAME} ${BUILD_PLATFORMS:-"$(go env GOHOSTOS)/$(go env GOHOSTARCH)"} ${BUILD_OUTPUT_DIR}
