# Quickstart

This guide will cover:

* [Installation](#installation): different ways to setup and run the KubeAdmiral control plane.
* [Managing Kubernetes resources](#managing-kubernetes-resources): basic example of propgating a Deployment resource.

## Installation

This section lists different ways to setup and run the KubeAdmiral control plane. To learn how to join a new cluster to an existing KubeAdmiral control plane, please refer to our [docs](./cluster-joining.md).

### For development and testing

If you wish to develop KubeAdmiral or simply see how it works, you can easily bootstrap a KubeAdmiral control plane in your local machine.

#### Prerequisites

Ensure that the following programs are installed:

* [Go](https://go.dev/) version v1.19+
* [Kind](https://kind.sigs.k8s.io/) version v0.10.0+
* [Kubectl](https://github.com/kubernetes/kubectl) version v0.20.15+

##### 1. Clone the KubeAdmiral repo to your machine

```console
$ git clone https://github.com/kubewharf/kubeadmiral
```

##### 2. Change to the kubeadmiral directory

```console
$ cd kubeadmiral
```

##### 3. Bootstrap Kubernetes clusters

```console
$ make kind
```

This command will run the script `hack/dev-up.sh` which does the following:

1. Starts a host cluster using Kind.
2. Installs the KubeAdmiral CRDs on the host cluster.
3. Starts 3 member clusters using Kind.
4. Bootstraps the joining of the 3 member clusters.
5. Exports the cluster kubeconfigs to `$HOME/.kube/kubeadmiral/`

If everything went well in the previous steps, we should observe the following:

```console
$ ls $HOME/.kube/kubeadmiral

kubeadmiral-host.yaml kubeadmiral-member-1.yaml kubeadmiral-member-2.yaml kubeadmiral-member-3.yaml
```

##### 4. Build and run the KubeAdmiral controller-manager

```console
$ make manager
$ ./output/manager --create-crds-for-ftcs \
    --klog-logtostderr=false \
    --klog-log-file "./kubeadmiral.log" \
    --kubeconfig "$HOME/.kube/kubeadmiral/kubeadmiral-host.yaml" \
    2>/dev/null
```

**Note: This runs the KubeAdmiral controller-manager on your local machine.**

##### 5. Wait for member clusters to be joined

The KubeAdmiral controller-manager will now handle the joining of the member clusters. Afterwards, we should expect to see the following:

```console
$ KUBECONFIG=$HOME/.kube/kubeadmiral/kubeconfig.yaml kubectl get fcluster

NAME                   READY   JOINED   AGE
kubeadmiral-member-1   True    True     1m
kubeadmiral-member-2   True    True     1m
kubeadmiral-member-3   True    True     1m
```

**Note: The clusters should be both `READY` and `JOINED`.**

🎉 The KubeAdmiral control plane and member clusters are now ready to be used.

## Managing Kubernetes resources

The most common usage of KubeAdmiral is to manage Kubernetes resources across multiple clusters using a single unified API.

This section describes how to propagate a Deployment to multiple member clusters, and view their individual statuses using KubeAdmiral.

For more advanced tutorials on resource management, you may refer to our [docs]().

### Prerequisites

Ensure that the following programs are installed:

* [Kubectl](https://github.com/kubernetes/kubectl) version v0.20.15+

Ensure that we have the following files:

* The kubeconfig of the KubeAdmiral control plane's host cluster

### 1. Source the kubeconfig of the host cluster

Replace `HOST_CLUSTER_KUBECONFIG` and `HOST_CLUSTER_CONTEXT` with the kubeconfig and context of the host cluster respectively.

```console
$ export KUBECONFIG=HOST_CLUSTER_KUBECONFIG

# Remember to switch to the right context
$ kubectl config use-context HOST_CLUSTER_CONTEXT
```

### 2. Create the deployment

```console
$ cat <<EOF > test-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo-server
  labels:
    app: echo-server
spec:
  replicas: 6
  selector:
    matchLabels:
      app: echo-server
  template:
    metadata:
      labels:
        app: echo-server
    spec:
      containers:
      - name: server
        image: ealen/echo-server:latest
        ports:
        - containerPort: 8080
          protocol: TCP
          name: echo-server
EOF
$ kubectl create -f test-deployment.yaml
```

### 3. Create a new propagation policy

```console
$ cat <<EOF > test-policy.yaml
apiVersion: core.kubeadmiral.io/v1alpha1
kind: PropagationPolicy
metadata:
  name: policy-all-clusters
spec:
  schedulingMode: Divide
  clusterSelector: {}
EOF
$ kubectl create -f test-policy.yaml
```

The newly created propagation policy will propagate resources to all clusters.

To learn more about how to use propagation policies, refer to our [docs]().

### 4. Label the deployment with the propagation policy

Attach the new propagation policy to our deployment by labelling the deployment with the policy's name.

```console
$ kubectl label deployment echo-server kubeadmiral.io/propagation-policy-name=policy-all-clusters
```

### 5. Wait for the deployment to be propagated to all clusters

If the KubeAdmiral control plane is working properly, the deployment will be propagated to all clusters shortly. Once that happens, we should see the number of ready replicas get updated:

```console
$ kubectl get deploy echo-server

NAME          READY   UP-TO-DATE   AVAILABLE   AGE
echo-server   6/6     6            6           10m
```

We can also view the status of the deployments in each member cluster by checking its `FederatedClusterStatus` object.

```console
$ kubectl get fdeploystatus echo-server -oyaml | yq .clusterStatus

- clusterName: kubeadmiral-member-1
  collectedFields:
    metadata:
      creationTimestamp: "2023-03-14T08:02:05Z"
    spec:
      replicas: 2
    status:
      availableReplicas: 2
      conditions:
        - lastTransitionTime: "2023-03-14T08:02:10Z"
          lastUpdateTime: "2023-03-14T08:02:10Z"
          message: Deployment has minimum availability.
          reason: MinimumReplicasAvailable
          status: "True"
          type: Available
        - lastTransitionTime: "2023-03-14T08:02:05Z"
          lastUpdateTime: "2023-03-14T08:02:10Z"
          message: ReplicaSet "echo-server-65dcc57996" has successfully progressed.
          reason: NewReplicaSetAvailable
          status: "True"
          type: Progressing
      observedGeneration: 1
      readyReplicas: 2
      replicas: 2
      updatedReplicas: 2
- clusterName: kubeadmiral-member-2
  collectedFields:
    metadata:
      creationTimestamp: "2023-03-14T08:02:05Z"
    spec:
      replicas: 2
    status:
      availableReplicas: 2
      conditions:
        - lastTransitionTime: "2023-03-14T08:02:09Z"
          lastUpdateTime: "2023-03-14T08:02:09Z"
          message: Deployment has minimum availability.
          reason: MinimumReplicasAvailable
          status: "True"
          type: Available
        - lastTransitionTime: "2023-03-14T08:02:05Z"
          lastUpdateTime: "2023-03-14T08:02:09Z"
          message: ReplicaSet "echo-server-65dcc57996" has successfully progressed.
          reason: NewReplicaSetAvailable
          status: "True"
          type: Progressing
      observedGeneration: 1
      readyReplicas: 2
      replicas: 2
      updatedReplicas: 2
- clusterName: kubeadmiral-member-3
  collectedFields:
    metadata:
      creationTimestamp: "2023-03-14T08:02:05Z"
    spec:
      replicas: 2
    status:
      availableReplicas: 2
      conditions:
        - lastTransitionTime: "2023-03-14T08:02:13Z"
          lastUpdateTime: "2023-03-14T08:02:13Z"
          message: Deployment has minimum availability.
          reason: MinimumReplicasAvailable
          status: "True"
          type: Available
        - lastTransitionTime: "2023-03-14T08:02:05Z"
          lastUpdateTime: "2023-03-14T08:02:13Z"
          message: ReplicaSet "echo-server-65dcc57996" has successfully progressed.
          reason: NewReplicaSetAvailable
          status: "True"
          type: Progressing
      observedGeneration: 1
      readyReplicas: 2
      replicas: 2
      updatedReplicas: 2
```

🎉 We have successfully propagated a deployment using KubeAdmiral.

### 6. Delete the files created in this example

```console
$ rm test-deployment.yaml test-policy.yaml
```

