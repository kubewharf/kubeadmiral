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

# local-up::create_cluster creates a kind cluster.
# Parmeters:
#  - $1: cluster_name, such as "host"
#  - $2: kubeconfig_path, sets kubeconfig path instead of $KUBECONFIG or $HOME/.kube/config
#  - $3: log_path, the log file path, such as "/tmp/logs/"
#  - $4: cluster_config, path to a kind config file
function local-up::create_cluster() {
  local cluster_name=${1}
  local kubeconfig_path=${2}
  local log_path=${3}
  local cluster_config=${4:-}

  mkdir -p ${log_path}
  rm -rf "${log_path}/${cluster_name}.log"
  rm -f "${kubeconfig_path}"
  nohup kind delete cluster --name="${cluster_name}" --kubeconfig="${kubeconfig_path}" >> "${log_path}"/"${cluster_name}".log 2>&1 && \
    kind create cluster --name "${cluster_name}" --kubeconfig="${kubeconfig_path}" --config="${cluster_config}" >> "${log_path}"/"${cluster_name}".log 2>&1 &
  echo "Creating cluster ${cluster_name} and the log file is in ${log_path}/${cluster_name}.log"
}

# deploy::get_macos_ipaddress will get ip address on macos interactively, store to 'MAC_NIC_IPADDRESS' if available
MAC_NIC_IPADDRESS=''
function local-up::get_macos_ipaddress() {
  if [[ $(go env GOOS) = "darwin" ]]; then
    tmp_ip=$(ipconfig getifaddr en0 || true)
    echo ""
    echo "Detected that you are installing KubeAdmiral on macOS"
    echo ""
    echo "It needs a Macintosh IP address to bind KubeAdmiral API Server(port 5443),"
    echo "so that member clusters can access it from docker containers, please"
    echo -n "input an available IP, "
    if [[ -z ${tmp_ip} ]]; then
      echo "you can use the command 'ifconfig' to look for one"
      tips_msg="[Enter IP address]:"
    else
      echo "default IP will be en0 inet addr if exists"
      tips_msg="[Enter for default ${tmp_ip}]:"
    fi
    read -r -p "${tips_msg}" MAC_NIC_IPADDRESS
    MAC_NIC_IPADDRESS=${MAC_NIC_IPADDRESS:-$tmp_ip}
    if [[ "${MAC_NIC_IPADDRESS}" =~ ^(([1-9]?[0-9]|1[0-9][0-9]|2([0-4][0-9]|5[0-5]))\.){3}([1-9]?[0-9]|1[0-9][0-9]|2([0-4][0-9]|5[0-5]))$ ]]; then
      echo "Using IP address: ${MAC_NIC_IPADDRESS}"
    else
      echo -e "\nError: you input an invalid IP address"
      exit 1
    fi
  else # non-macOS
    MAC_NIC_IPADDRESS=${MAC_NIC_IPADDRESS:-}
  fi
}
