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
source "${REPO_ROOT}/hack/lib/util.sh"


# This function gets api server's ip from kubeconfig by context name
function deploy::get_apiserver_ip_from_kubeconfig(){
  local context_name=$1
  local kubeconfig_path=$2
  local cluster_name apiserver_url
  cluster_name=$(kubectl --kubeconfig="${kubeconfig_path}" config view --template='{{ range $_, $value := .contexts }}{{if eq $value.name '"\"${context_name}\""'}}{{$value.context.cluster}}{{end}}{{end}}')
  apiserver_url=$(kubectl --kubeconfig="${kubeconfig_path}" config view --template='{{range $_, $value := .clusters }}{{if eq $value.name '"\"${cluster_name}\""'}}{{$value.cluster.server}}{{end}}{{end}}')
  echo "${apiserver_url}" | awk -F/ '{print $3}' | sed 's/:.*//'
}

# deploy::kubectl_with_retry will retry if execute kubectl command failed
# tolerate kubectl command failure that may happen before the pod is created by StatefulSet/Deployment.
function deploy::kubectl_with_retry() {
    local ret=0
    for i in {1..10}; do
        kubectl "$@"
        ret=$?
        if [[ ${ret} -ne 0 ]]; then
            echo "kubectl $@ failed, retrying(${i} times)"
            sleep 2
            continue
        else
            return 0
        fi
    done

    echo "kubectl $@ failed"
    kubectl "$@"
    return ${ret}
}

# deploy::wait_pod_ready waits for pod state becomes ready until timeout.
# Parameters:
#  - $1: kubeconfig_path, the path of kubeconfig
#  - $2: k8s context name, such as "kubeadmiral-apiserver"
#  - $3: pod label, such as "app=etcd"
#  - $4: pod namespace, such as "kubeadmiral-system"
function deploy::wait_pod_ready() {
    local kubeconfig_path=$1
    local context_name=$2
    local pod_label=$3
    local pod_namespace=$4

    echo "wait the ${pod_label} ready..."
    set +e
    deploy::kubectl_with_retry --kubeconfig="${kubeconfig_path}" --context="${context_name}" wait --for=condition=Ready --timeout=30s pods -l ${pod_label} -n ${pod_namespace}
    ret=$?
    set -e
    if [ $ret -ne 0 ];then
      echo "kubectl describe info:"
      kubectl --kubeconfig="${kubeconfig_path}" --context="${context_name}" describe pod -l ${pod_label} -n ${pod_namespace}
      echo "kubectl logs info:"
      kubectl --kubeconfig="${kubeconfig_path}" --context="${context_name}" logs -l ${pod_label} -n ${pod_namespace}
    fi
    return ${ret}
}

# deploy::create_signing_certkey creates a CA, args are sudo, dest-dir, ca-id, cn, purpose
function deploy::create_signing_certkey {
    local sudo=$1
    local dest_dir=$2
    local id=$3
    local cn=$4
    local purpose=$5
    OPENSSL_BIN=$(command -v openssl)
    # Create ca
    ${sudo} /usr/bin/env bash -e <<EOF
    rm -f "${dest_dir}/${id}.crt" "${dest_dir}/${id}.key"
    ${OPENSSL_BIN} req -nodes \
      -newkey rsa:2048 -keyout "${dest_dir}/${id}.key" -out "${dest_dir}/${id}.crt" \
      -x509 -sha256 -new -days 3650 -subj "/CN=${cn}/"
    echo '{"signing":{"default":{"expiry":"43800h","usages":["signing","key encipherment",${purpose}]}}}' > "${dest_dir}/${id}-config.json"
EOF
}

# deploy::create_certkey signs a certificate: args are sudo, dest-dir, ca, filename (roughly), subject, hosts...
function deploy::create_certkey {
    local sudo=$1
    local dest_dir=$2
    local ca=$3
    local id=$4
    local cn=${5:-$4}
    local og=$6
    local hosts=""
    local SEP=""
    shift 6
    while [[ -n "${1:-}" ]]; do
        hosts+="${SEP}\"$1\""
        SEP=","
        shift 1
    done
    ${sudo} /usr/bin/env bash -e <<EOF
    cd ${dest_dir}
    echo '{"CN":"${cn}","hosts":[${hosts}],"names":[{"O":"${og}"}],"key":{"algo":"rsa","size":2048}}' \
      | ${CFSSL_BIN} gencert -ca=${ca}.crt -ca-key=${ca}.key -config=${ca}-config.json - \
      | ${CFSSLJSON_BIN} -bare ${id}
    mv "${id}-key.pem" "${id}.key"
    mv "${id}.pem" "${id}.crt"
    rm -f "${id}.csr"
EOF
}

# generate a secret to store the certificates
function deploy::generate_cert_secret {
  local root_ca_file_path=$1
  local root_ca_key_path=$2
  local cert_yaml_path=$3
  local kubeconfig_path=$4
  local meta_cluster_context=$5

  local kubeadmiral_ca=$(base64 -i "${root_ca_file_path}" | tr -d '\r\n')
  local kubeadmiral_ca_key=$(base64 -i "${root_ca_key_path}" | tr -d '\r\n')

  local TEMP_PATH=$(mktemp -d)

  cp -rf "${cert_yaml_path}"/kubeadmiral-cert-secret.yaml "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  cp -rf "${cert_yaml_path}"/kubeconfig-secret.yaml "${TEMP_PATH}"/kubeconfig-secret-tmp.yaml

  sed -i'' -e "s/{{ca_crt}}/${kubeadmiral_ca}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{ca_key}}/${kubeadmiral_ca_key}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{client_crt}}/${KUBEADMIRAL_CRT}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{client_key}}/${KUBEADMIRAL_KEY}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{apiserver_crt}}/${KUBEADMIRAL_APISERVER_CRT}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{apiserver_key}}/${KUBEADMIRAL_APISERVER_KEY}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml

  sed -i'' -e "s/{{front_proxy_ca_crt}}/${FRONT_PROXY_CA_CRT}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{front_proxy_client_crt}}/${FRONT_PROXY_CLIENT_CRT}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{front_proxy_client_key}}/${FRONT_PROXY_CLIENT_KEY}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml

  sed -i'' -e "s/{{etcd_ca_crt}}/${ETCD_CA_CRT}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{etcd_server_crt}}/${ETCD_SERVER_CRT}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{etcd_server_key}}/${ETCD_SERVER_KEY}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{etcd_client_crt}}/${ETCD_CLIENT_CRT}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  sed -i'' -e "s/{{etcd_client_key}}/${ETCD_CLIENT_KEY}/g" "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml

  sed -i'' -e "s/{{ca_crt}}/${kubeadmiral_ca}/g" "${TEMP_PATH}"/kubeconfig-secret-tmp.yaml
  sed -i'' -e "s/{{client_crt}}/${KUBEADMIRAL_CRT}/g" "${TEMP_PATH}"/kubeconfig-secret-tmp.yaml
  sed -i'' -e "s/{{client_key}}/${KUBEADMIRAL_KEY}/g" "${TEMP_PATH}"/kubeconfig-secret-tmp.yaml

  kubectl --kubeconfig=${kubeconfig_path} --context="${meta_cluster_context}" apply -f "${TEMP_PATH}"/kubeadmiral-cert-secret-tmp.yaml
  kubectl --kubeconfig=${kubeconfig_path} --context="${meta_cluster_context}" apply -f "${TEMP_PATH}"/kubeconfig-secret-tmp.yaml
  rm -rf "${TEMP_PATH}"
}

# deploy::ensure_cfssl downloads cfssl/cfssljson if they do not already exist in PATH
function deploy::ensure_cfssl {
  CFSSL_VERSION=${1}
  if command -v cfssl &>/dev/null && command -v cfssljson &>/dev/null; then
      CFSSL_BIN=$(command -v cfssl)
      CFSSLJSON_BIN=$(command -v cfssljson)
      return 0
  fi

  util::install_tools "github.com/cloudflare/cfssl/cmd/..." ${CFSSL_VERSION}

  GOPATH=$(go env GOPATH | awk -F ':' '{print $1}')
  CFSSL_BIN="${GOPATH}/bin/cfssl"
  CFSSLJSON_BIN="${GOPATH}/bin/cfssljson"
  if [[ ! -x ${CFSSL_BIN} || ! -x ${CFSSLJSON_BIN} ]]; then
    echo "Failed to download 'cfssl'. Please install cfssl and cfssljson and verify they are in \$PATH."
    echo "Hint: export PATH=\$PATH:\$GOPATH/bin; go install github.com/cloudflare/cfssl/cmd/..."
    exit 1
  fi
}

# deploy::append_client_kubeconfig creates a new context including a cluster and a user to the existed kubeconfig file
function deploy::append_client_kubeconfig {
    local kubeconfig_path=$1
    local client_certificate_file=$2
    local client_key_file=$3
    local api_host=$4
    local api_port=$5
    local client_id=$6
    local token=${7:-}
    kubectl config set-cluster "${client_id}" --server=https://"${api_host}:${api_port}" --insecure-skip-tls-verify=true --kubeconfig="${kubeconfig_path}"
    kubectl config set-credentials "${client_id}" --token="${token}" --client-certificate="${client_certificate_file}" --client-key="${client_key_file}" --embed-certs=true --kubeconfig="${kubeconfig_path}"
    kubectl config set-context "${client_id}" --cluster="${client_id}" --user="${client_id}" --kubeconfig="${kubeconfig_path}"
}
