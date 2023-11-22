#!/usr/bin/env bash

# This file is based on https://github.com/kubernetes/code-generator/blob/master/generate-groups.sh
# Copyright 2017 The Kubernetes Authors.
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

CODEGEN_VERSION=${CODEGEN_VERSION:-"v0.26.6"}
CONTROLLERGEN_VERSION=${CONTROLLERGEN_VERSION:-"v0.11.1"}
YQ_VERSION=${YQ_VERSION:-"v4.33.1"}

MODULE_NAME=${MODULE_NAME:-"github.com/kubewharf/kubeadmiral"}
groups=(
  core/v1alpha1
  hpaaggregator/v1alpha1
)

# install code-generator binaries
go install k8s.io/code-generator/cmd/{client-gen,lister-gen,informer-gen,deepcopy-gen,openapi-gen}@${CODEGEN_VERSION}
go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLERGEN_VERSION}
go install github.com/mikefarah/yq/v4@${YQ_VERSION}

# define variables
GOBIN="$(go env GOBIN)"
GOBIN="${GOBIN:-$(go env GOPATH)/bin}"

ROOT_DIR="$(dirname "${0}")/.."
HEADER_FILE="${ROOT_DIR}/hack/boilerplate.go.txt"
OUTPUT_DIR="${ROOT_DIR}/generated"
INPUT_BASE="${MODULE_NAME}/pkg/apis"

INPUT_DIRS=()
for group in "${groups[@]}"; do
  INPUT_DIRS+=("${INPUT_BASE}/${group}")
done

NO_FEDERATED_ANNOTATION="kubeadmiral.io/no-federated-resource"

# generate code
function codegen::join() {
  local IFS="$1"
  shift
  echo "$*"
}

# generate manifests
echo "Generating manifests"
${GOBIN}/controller-gen crd paths=$(codegen::join ";" "${INPUT_DIRS[@]}") output:crd:artifacts:config=config/crds

# patch CRDs with no-federate annotation
for crd_file in config/crds/*.yaml; do
  yq eval -i ".metadata.annotations[\"${NO_FEDERATED_ANNOTATION}\"] = \"true\"" "${crd_file}"
done

# apply other CRD patches
for patch_file in config/crds/patches/*.sh; do
  if [[ $patch_file == *.src.sh ]]; then
    continue
  fi

  crd_file="config/crds/$(basename "${patch_file}" .sh)".yaml
  if [[ ! -f "$crd_file" ]]; then
    echo "CRD patch file $patch_file does not have a corresponding CRD file" >&2
    exit 1
  fi

  PATH="$GOBIN:$PATH" bash $patch_file $crd_file
done

# generate deepcopy
echo "Generating deepcopy funcs"
${GOBIN}/deepcopy-gen -h ${HEADER_FILE} -o ${OUTPUT_DIR} \
  --input-dirs=$(codegen::join , "${INPUT_DIRS[@]}") \
  --output-file-base="zz_generated.deepcopy" \
  "$@"

# generate client
CLIENT_OUTPUT_PACKAGE="${MODULE_NAME}/pkg/client/clientset"

echo "Generating clientsets"
${GOBIN}/client-gen -h ${HEADER_FILE} -o ${OUTPUT_DIR} \
  --clientset-name="versioned" \
  --input-base="" \
  --input=$(codegen::join , "${INPUT_DIRS[@]}") \
  --output-package=${CLIENT_OUTPUT_PACKAGE} \
  "$@"

# generate lister
LISTER_OUTPUT_PACKAGE="${MODULE_NAME}/pkg/client/listers"

echo "Generating listers"
${GOBIN}/lister-gen -h ${HEADER_FILE} -o ${OUTPUT_DIR} \
  --input-dirs=$(codegen::join , "${INPUT_DIRS[@]}") \
  --output-package=${LISTER_OUTPUT_PACKAGE} \
  "$@"

# generate informer
INFORMER_OUTPUT_PACKAGE="${MODULE_NAME}/pkg/client/informers"
VERSIONED_CLIENTSET_PACKAGE="${CLIENT_OUTPUT_PACKAGE}/versioned"

echo "Generating informers"
${GOBIN}/informer-gen -h ${HEADER_FILE} -o ${OUTPUT_DIR} \
  --input-dirs=$(codegen::join , "${INPUT_DIRS[@]}") \
  --output-package=${INFORMER_OUTPUT_PACKAGE} \
  --versioned-clientset-package=${VERSIONED_CLIENTSET_PACKAGE} \
  --listers-package=${LISTER_OUTPUT_PACKAGE} \
  "$@"

# generate open-api
OPENAPI_OUTPUT_PACKAGE="${MODULE_NAME}/pkg/client/openapi"

echo "Generating openapi"
# only hpaaggregator need to generate open-api now
${GOBIN}/openapi-gen -h ${HEADER_FILE} -o ${OUTPUT_DIR} \
  --input-dirs="k8s.io/apimachinery/pkg/apis/meta/v1" \
  --input-dirs="k8s.io/apimachinery/pkg/runtime" \
  --input-dirs="k8s.io/apimachinery/pkg/version" \
  --input-dirs="k8s.io/apimachinery/pkg/api/resource" \
  --input-dirs "k8s.io/metrics/pkg/apis/custom_metrics" \
  --input-dirs "k8s.io/metrics/pkg/apis/custom_metrics/v1beta1" \
  --input-dirs "k8s.io/metrics/pkg/apis/custom_metrics/v1beta2" \
  --input-dirs "k8s.io/metrics/pkg/apis/external_metrics" \
  --input-dirs "k8s.io/metrics/pkg/apis/external_metrics/v1beta1" \
  --input-dirs "k8s.io/metrics/pkg/apis/metrics" \
  --input-dirs "k8s.io/metrics/pkg/apis/metrics/v1beta1" \
  --input-dirs "k8s.io/api/core/v1" \
  --input-dirs="${MODULE_NAME}/pkg/apis/hpaaggregator/v1alpha1" \
  --output-package=${OPENAPI_OUTPUT_PACKAGE} \
  --output-file-base="zz_generated.openapi" \
  "$@"

# copy files to proper location
files=($(find ${OUTPUT_DIR} -type f))

echo "Copying generated code"
for file in "${files[@]}"; do
  TARGET=$(echo $file | sed 's|'${OUTPUT_DIR}/${MODULE_NAME}'|'${ROOT_DIR}'|g')
  mkdir -p $(dirname $TARGET)
  cp $file $TARGET
done

# delete output folder
rm -r ${OUTPUT_DIR}

echo "Done"
