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

#https://textkool.com/en/ascii-art-generator?hl=default&vl=default&font=Big&text=KUBEADMIRAL
KUBEADMIRAL_GREETING="
-------------------------------------------------------------------------------------
  _  ___    _ ____  ______     __     _____  __  __ _____ _____       __     _
 | |/ / |  | |  _ \|  ____|   /  \   |  __ \|  \/  |_   _|  __ \     /  \   | |
 | ' /| |  | | |_) | |__     / __ \  | |  | | \  / | | | | |__) |   / __ \  | |
 |  < | |  | |  _ <|  __|   / /  \ \ | |  | | |\/| | | | |  _  /   / /  \ \ | |
 | . \| |__| | |_) | |____ / /____\ \| |__| | |  | |_| |_| | \ \  / /____\ \| |____
 |_|\_.\____/|____/|______/_/      \_\_____/|_|  |_|_____|_|  \_\/_/      \_\______|
-------------------------------------------------------------------------------------
"

# util::cmd_exist check whether command is installed.
function util::cmd_exist {
  local CMD=$(command -v ${1})
  if [[ ! -x ${CMD} ]]; then
    return 1
  fi
  return 0
}

function util::ensure_go_version {
  local min_version=$1
  if [[ $(util::cmd_exist "go") == 1 ]]; then
    return 1
  fi
  local go_version
  IFS=" " read -ra go_version <<< "$(GOFLAGS='' go version)"
  if [[ "${min_version}" != $(echo -e "${min_version}\n${go_version[2]}" | sort -s -t. -k 1,1 -k 2,2n -k 3,3n | head -n1) && "${go_version[2]}" != "devel" ]]; then
    return 1
  fi
  return 0
}

function util::ensure_docker_version() {
  local min_version=$1
  if [[ $(util::cmd_exist "docker") == 1 ]]; then
    return 1
  fi
  local docker_version=$(docker version --format '{{.Client.Version}}' | cut -d"-" -f1)
  if [[ ${docker_version} != ${min_version} && ${docker_version} < ${min_version} ]]; then
    return 1
  fi
  return 0
}

# This function installs a Go tools by 'go install' command.
# Parameters:
#  - $1: package name, such as "sigs.k8s.io/controller-tools/cmd/controller-gen"
#  - $2: package version, such as "v0.4.1"
function util::install_tools() {
	local package="$1"
	local version="$2"
	echo "go install ${package}@${version}"
	GO111MODULE=on go install "${package}"@"${version}"
	GOPATH=$(go env GOPATH | awk -F ':' '{print $1}')
	export PATH=$PATH:$GOPATH/bin
}

# util::install_kubectl will install the given version kubectl
function util::install_kubectl {
  local KUBECTL_VERSION=${1}
  local ARCH=${2}
  local OS=${3:-linux}
  if [ -z "$KUBECTL_VERSION" ]; then
    KUBECTL_VERSION=$(curl -L -s https://dl.k8s.io/release/stable.txt)
  fi
  echo "Installing 'kubectl ${KUBECTL_VERSION}' for you"
  curl --retry 5 -sSLo ./kubectl -w "%{http_code}" https://dl.k8s.io/release/"$KUBECTL_VERSION"/bin/"$OS"/"$ARCH"/kubectl | grep '200' > /dev/null
  ret=$?
  if [ ${ret} -ne 0 ]; then
      chmod +x ./kubectl
      mkdir -p ~/.local/bin/
      mv ./kubectl ~/.local/bin/kubectl

      export PATH=$PATH:~/.local/bin
  else
      echo "Failed to install kubectl, can not download the binary file at https://dl.k8s.io/release/$KUBECTL_VERSION/bin/$OS/$ARCH/kubectl"
      exit 1
  fi
}

# project_info generates the project information and the corresponding value
# for 'ldflags -X' option
function util::project_info() {
  KUBEADMIRAL_INFO_PKG="github.com/kubewharf/kubeadmiral/pkg"
  GIT_VERSION=${GIT_VERSION:-$(git describe --tags --dirty)}
  GIT_COMMIT_HASH=$(git rev-parse HEAD)
  if git_status=$(git status --porcelain 2>/dev/null) && [[ -z ${git_status} ]]; then
    GIT_TREESTATE="clean"
  else
    GIT_TREESTATE="dirty"
  fi
  BUILD_DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ')

  LDFLAGS="-X ${KUBEADMIRAL_INFO_PKG}/version.gitVersion=${GIT_VERSION} \
            -X ${KUBEADMIRAL_INFO_PKG}/version.gitCommit=${GIT_COMMIT_HASH} \
            -X ${KUBEADMIRAL_INFO_PKG}/version.gitTreeState=${GIT_TREESTATE} \
            -X ${KUBEADMIRAL_INFO_PKG}/version.buildDate=${BUILD_DATE}"
  echo ${LDFLAGS}
}

function util::join_member_cluster() {
  local MEMBER_CLUSTER_NAME=${1}
  local HOST_CLUSTER_CONTEXT=${2}
  local KUBECONFIG_PATH=${3}
  local CLUSTER_PROVIDER=${4:-"kind"}

  local MEMBER_API_ENDPOINT MEMBER_CA MEMBER_CERT MEMBER_KEY
  if [[ $CLUSTER_PROVIDER == "kind" ]]; then
    MEMBER_API_ENDPOINT=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'server:' | awk '{ print $2 }')
    MEMBER_CA=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'certificate-authority-data:' | awk '{ print $2 }')
    MEMBER_CERT=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-certificate-data:' | awk '{ print $2 }')
    MEMBER_KEY=$(kind get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-key-data:' | awk '{ print $2 }')
  elif [[ $CLUSTER_PROVIDER == "kwok" ]]; then
    MEMBER_API_ENDPOINT=$(kwokctl get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'server:' | awk '{ print $2 }')
    MEMBER_CA=$(cat ${KWOK_WORKDIR:-"${HOME}/.kwok/clusters/${MEMBER_CLUSTER_NAME}/pki/ca.crt"} | base64 | tr -d '\n')
    MEMBER_CERT=$(kwokctl get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-certificate-data:' | awk '{ print $2 }')
    MEMBER_KEY=$(kwokctl get kubeconfig --name="${MEMBER_CLUSTER_NAME}" | grep 'client-key-data:' | awk '{ print $2 }')
  else
    echo "Invalid provider, only kwok or kind allowed"
    exit 1
  fi

  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CLUSTER_CONTEXT}" create -f - <<EOF
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
  kubectl --kubeconfig="${KUBECONFIG_PATH}" --context="${HOST_CLUSTER_CONTEXT}" create -f - <<EOF
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
}

# util::wait_file_exist checks if a file exists, if not, wait until timeout
function util::wait_file_exist() {
    local file_path=${1}
    local timeout=${2}
    local error_msg="[ERROR] Timeout waiting for file exist ${file_path}"
    for ((time=0; time<${timeout}; time++)); do
        if [[ -e ${file_path} ]]; then
            return 0
        fi
        sleep 1
    done
    echo -e "\n${error_msg}"
    return 1
}

# util::wait_for_condition blocks until the provided condition becomes true
# Arguments:
#  - 1: message indicating what conditions is being waited for (e.g. 'ok')
#  - 2: a string representing an eval'able condition.  When eval'd it should not output
#       anything to stdout or stderr.
#  - 3: optional timeout in seconds. If not provided, waits forever.
# Returns:
#  1 if the condition is not met before the timeout
function util::wait_for_condition() {
  local msg=$1
  # condition should be a string that can be eval'd.
  local condition=$2
  local timeout=${3:-}

  local start_msg="Waiting for ${msg}"
  local error_msg="[ERROR] Timeout waiting for condition ${msg}"

  local counter=0
  while ! eval ${condition}; do
    if [[ "${counter}" = "0" ]]; then
      echo -n "${start_msg}"
    fi

    if [[ -z "${timeout}" || "${counter}" -lt "${timeout}" ]]; then
      counter=$((counter + 1))
      if [[ -n "${timeout}" ]]; then
        echo -n '.'
      fi
      sleep 1
    else
      echo -e "\n${error_msg}"
      return 1
    fi
  done

  if [[ "${counter}" != "0" && -n "${timeout}" ]]; then
    echo ' done'
  fi
}

# This function returns the IP address of a docker instance
# Parameters:
#  - $1: docker instance name
function util::get_docker_native_ipaddress(){
  local container_name=$1
  docker inspect --format='{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${container_name}"
}

# This function returns the IP address and port of a specific docker instance's host IP
# Parameters:
#  - $1: docker instance name
# Note:
#   Use for getting host IP and port for cluster
#   "6443/tcp" assumes that API server port is 6443 and protocol is TCP
function util::get_docker_host_ip_port(){
  local container_name=$1
  docker inspect --format='{{range $key, $value := index .NetworkSettings.Ports "6443/tcp"}}{{if eq $key 0}}{{$value.HostIp}}:{{$value.HostPort}}{{end}}{{end}}' "${container_name}"
}

# util::check_clusters_ready checks if a cluster is ready, if not, wait until timeout
function util::check_clusters_ready() {
  local kubeconfig_path=${1}
  local context_name=${2}

  echo "Waiting for kubeconfig file ${kubeconfig_path} and clusters ${context_name} to be ready..."
  util::wait_file_exist "${kubeconfig_path}" 300
  util::wait_for_condition 'running' "docker inspect --format='{{.State.Status}}' ${context_name}-control-plane &> /dev/null" 300

  kubectl config rename-context "kind-${context_name}" "${context_name}" --kubeconfig="${kubeconfig_path}"

  local os_name
  os_name=$(go env GOOS)
  local container_ip_port
  case $os_name in
    linux) container_ip_port=$(util::get_docker_native_ipaddress "${context_name}-control-plane")":6443"
    ;;
    darwin) container_ip_port=$(util::get_docker_host_ip_port "${context_name}-control-plane")
    ;;
    *)
      echo "OS ${os_name} does NOT support for getting container ip in installation script"
      exit 1
  esac
  kubectl config set-cluster "kind-${context_name}" --server="https://${container_ip_port}" --kubeconfig="${kubeconfig_path}"

  util::wait_for_condition 'ok' "kubectl --kubeconfig ${kubeconfig_path} --context ${context_name} get --raw=/healthz &> /dev/null" 300
}

# util::set_mirror_registry_for_china_mainland will do proxy setting in China mainland
# Parameters:
#  - $1: the root path of repo
function util::set_mirror_registry_for_china_mainland() {
  local repo_root=${1}
  export GOPROXY=https://goproxy.cn,direct # set domestic go proxy
  # set mirror registry of registry.k8s.io
  registry_files=( # Yaml files that contain image host 'registry.k8s.io' need to be replaced
    "config/deploy/controlplane/etcd.yaml"
    "config/deploy/controlplane/kube-apiserver.yaml"
    "config/deploy/controlplane/kube-controller-manager.yaml"
  )
  for registry_file in "${registry_files[@]}"; do
    sed -i'' -e "s#registry.k8s.io#registry.aliyuncs.com/google_containers#g" ${repo_root}/${registry_file}
  done

  # set mirror registry in the dockerfile of components of kubeadmiral
  dockerfile_list=( # Dockerfile files need to be replaced
    "hack/dockerfiles/Dockerfile"
  )
  for dockerfile in "${dockerfile_list[@]}"; do
    grep 'mirrors.ustc.edu.cn' ${repo_root}/${dockerfile} > /dev/null || sed -i'' -e "9a\\
RUN echo -e http://mirrors.ustc.edu.cn/alpine/v3.17/main/ > /etc/apk/repositories" ${repo_root}/${dockerfile}
  done
}
