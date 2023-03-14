# Cluster Joining

This guide describes how to join a new Kubernetes member cluster to a KubeAdmiral control plane.

### Prerequisites

Ensure that the following programs are installed:

* [Kubectl](https://github.com/kubernetes/kubectl) version v0.20.15+

Ensure that we have the following files:

* The kubeconfig of the KubeAdmiral control plane's host apiserver
* The new member cluster's client cert, client key and CA

**Note: The client cert, client key and CA may be retrieved from the cluster's kubeconfig.**

### 1. Source the kubeconfig of the host cluster

Replace `HOST_CLUSTER_KUBECONFIG` and `HOST_CLUSTER_CONTEXT` with the kubeconfig and context of the host cluster respectively.

```console
$ export KUBECONFIG=HOST_CLUSTER_KUBECONFIG

# Remember to switch to the right context
$ kubectl config use-context HOST_CLUSTER_CONTEXT
```

### 2. Encode the client cert, client key and CA of the new cluster

Replace `NEW_CLUSTER_CA`, `NEW_CLUSTER_CERT` and `NEW_CLUSTER_KEY` with the certificate authority, client certificate and client key of the new cluster respectively.

```console
$ export ca_data=$(base64 NEW_CLUSTER_CA)
$ export cert_data=$(base64 NEW_CLUSTER_CERT)
$ export key_data=$(base64 NEW_CLUSTER_KEY)
```

### 3. Create the cluster `Secret` for the new cluster

Replace `CLUSTER_NAME` with the name of the new cluster.

```console
$ cat << EOF > cluster-secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: CLUSTER_NAME
  namespace: kube-admiral-system
data:
  certificate-authority-data: $ca_data
  client-certificate-data: $cert_data
  client-key-data: $key_data
EOF
$ kubectl create -f cluster-secret.yaml
```

### 4. Create the `FederatedCluster` object for the new cluster

Replace `CLUSTER_NAME` and `CLUSTER_ENDPOINT` with the name and address of the new cluster.

```console
$ cat <<EOF > cluster.yaml
apiVersion: core.kubeadmiral.io/v1alpha1
kind: FederatedCluster
metadata:
  name: CLUSTER_NAME
spec:
  apiEndpoint: CLUSTER_ENDPOINT
  secretRef:
    name: CLUSTER_NAME
  useServiceAccount: true
EOF
$ kubectl create -f cluster.yaml
```

### 5. Wait for the new cluster to be joined

If both the KubeAdmiral control plane and new cluster are working properly, the joining process should complete shortly after and we should expect to see the following:

```console
$ kubectl get fcluster

NAME                   READY   JOINED   AGE
...
CLUSTER_NAME           True    True     1m
...
```

**Note: The new cluster should be both `READY` and `JOINED`.**

🎉 The new member cluster is now ready to be used.

### 6. Delete the files created in this example

```console
$ rm cluster-secret.yaml cluster.yaml
```

