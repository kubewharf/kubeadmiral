# Quickstart

This guide will cover:

* [Installation](#installation): starting a Kubernetes cluster and installing `KubeAdmiral` on it.
* [Managing Kubernetes resources](#managing-kubernetes-resources): basic example of propagating a Deployment resource.
* [Uninstallation](#uninstallation): cleaning up the local kind clusters related to `KubeAdmiral`.

## Installation

If you wish to see how `KubeAdmiral` works, you can easily start a cluster that includes the `KubeAdmiral` control plane on your local machine.

### Prerequisites

Ensure that the following programs are installed:

* [Go](https://go.dev/) version v1.19+
* [Kind](https://kind.sigs.k8s.io/) version v0.14.0+
* [Kubectl](https://github.com/kubernetes/kubectl) version v0.20.15+

### 1. Clone the KubeAdmiral repo to your machine

```console
$ git clone https://github.com/kubewharf/kubeadmiral
```

### 2. Change to the kubeadmiral directory

```console
$ cd kubeadmiral
```

### 3. Bootstrap clusters and install KubeAdmiral control plane

```console
$ make local-up
```

This command does the following tasks:

1. Starts a meta cluster using Kind.
2. Installs the KubeAdmiral control-plane components on meta cluster.
3. Starts 3 member clusters using Kind.
4. Bootstraps the joining of the 3 member clusters.
5. Exports the cluster kubeconfigs to `$HOME/.kube/kubeadmiral`

If everything went well in the previous steps, we will see the following messages:

```console
Your local KubeAdmiral has been deployed successfully!

To start using your KubeAdmiral, run:
  export KUBECONFIG=$HOME/.kube/kubeadmiral/kubeadmiral.config
  
To observe the status of KubeAdmiral control-plane components, run:
  export KUBECONFIG=$HOME/.kube/kubeadmiral/meta.config

To inspect your member clusters, run one of the following:
  export KUBECONFIG=$HOME/.kube/kubeadmiral/member-1.config
  export KUBECONFIG=$HOME/.kube/kubeadmiral/member-2.config
  export KUBECONFIG=$HOME/.kube/kubeadmiral/member-3.config
```

There are three types of kubeconfig: 
- `kubeadmiral.config` is the main entrypoint of a KubeAdmiral instance. KubeAdmiral considers resources created in this api-server for propagation;
- `meta.config` is only used for debugging KubeAdmiral installation with the meta cluster;
- `member-{1,2,3}.config` are used for inspecting the member clusters.

We will mainly be working with the kubeadmiral control-plane for the rest of this guide. Before proceeding, go ahead and switch to the kubeadmiral cluster:

```console
$ export KUBECONFIG=$HOME/.kube/kubeadmiral/kubeadmiral.config
```


### 4. Wait for member clusters to be joined

The KubeAdmiral controller-manager will now handle the joining of the member clusters. Afterwards, we should expect to see the following:

```console
$ kubectl get fcluster

NAME                   READY   JOINED   AGE
kubeadmiral-member-1   True    True     1m
kubeadmiral-member-2   True    True     1m
kubeadmiral-member-3   True    True     1m
```

**Note: The clusters should be both `READY` and `JOINED`.**

🎉 The KubeAdmiral control plane and member clusters are now ready to be used.

To learn how to join a new cluster to an existing KubeAdmiral control plane, please refer to [this guide](./cluster-joining.md).

## Managing Kubernetes resources

The most common usage of KubeAdmiral is to manage Kubernetes resources across multiple clusters using a single unified API.

This section describes how to propagate a Deployment to multiple member clusters, and view their individual statuses using KubeAdmiral.

<!-- TODO: link to docs once available -->
<!-- For more advanced tutorials on resource management, you may refer to our [docs](). -->

### 1. Ensure you are still using the host cluster kubeconfig

```console
$ export KUBECONFIG=$HOME/.kube/kubeadmiral/kubeadmiral.config
```

### 2. Create the deployment

```console
$ kubectl create -f - <<EOF
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

$ kubectl get deploy echo-server
NAME          READY   UP-TO-DATE   AVAILABLE   AGE
echo-server   0/6     0            0           3s
```

### 3. Create a new propagation policy

```console
$ kubectl create -f - <<EOF
apiVersion: core.kubeadmiral.io/v1alpha1
kind: PropagationPolicy
metadata:
  name: policy-all-clusters
spec:
  schedulingMode: Divide
  clusterSelector: {}
EOF

$ # pp is a shortname for propagationpolicy
$ kubectl get pp policy-all-clusters
NAME                  AGE
policy-all-clusters   10s
```

The newly created propagation policy will propagate resources to all clusters.

<!-- TODO: link to docs once available -->
<!-- To learn more about how to use propagation policies, refer to our [docs](). -->

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
echo-server   6/6     6            6           26s
```

We can also view the status of the deployments in each member cluster by checking its `CollectedStatus` object.

```console
$ # Usually the name consists of the resource name and its GroupResource (e.g. echo-server-deployments.apps),
$ # but sometimes there may be a hash suffix to avoid duplication.
$ # We can use `get collectedstatus -l apps/v1=Deployment` to get all `CollectedStatus`es for `apps/v1.Deployment`s.
$ kubectl get collectedstatus -l apps/v1=Deployment | grep echo-server
echo-server-deployments.apps       57s

$ kubectl get collectedstatus echo-server-deployments.apps -oyaml
apiVersion: core.kubeadmiral.io/v1alpha1
clusters:
- cluster: kubeadmiral-member-1
  collectedFields:
    metadata:
      creationTimestamp: "2023-11-23T11:09:04Z"
    spec:
      replicas: 2
    status:
      availableReplicas: 2
      conditions:
      - lastTransitionTime: "2023-11-23T11:09:10Z"
        lastUpdateTime: "2023-11-23T11:09:10Z"
        message: Deployment has minimum availability.
        reason: MinimumReplicasAvailable
        status: "True"
        type: Available
      - lastTransitionTime: "2023-11-23T11:09:04Z"
        lastUpdateTime: "2023-11-23T11:09:12Z"
        message: ReplicaSet "echo-server-65dcc57996" has successfully progressed.
        reason: NewReplicaSetAvailable
        status: "True"
        type: Progressing
      observedGeneration: 1
      readyReplicas: 2
      replicas: 2
      updatedReplicas: 2
- cluster: kubeadmiral-member-2
  collectedFields:
    metadata:
      creationTimestamp: "2023-11-23T11:09:04Z"
    spec:
      replicas: 2
    status:
      availableReplicas: 2
      conditions:
      - lastTransitionTime: "2023-11-23T11:09:09Z"
        lastUpdateTime: "2023-11-23T11:09:09Z"
        message: Deployment has minimum availability.
        reason: MinimumReplicasAvailable
        status: "True"
        type: Available
      - lastTransitionTime: "2023-11-23T11:09:04Z"
        lastUpdateTime: "2023-11-23T11:09:09Z"
        message: ReplicaSet "echo-server-65dcc57996" has successfully progressed.
        reason: NewReplicaSetAvailable
        status: "True"
        type: Progressing
      observedGeneration: 1
      readyReplicas: 2
      replicas: 2
      updatedReplicas: 2
- cluster: kubeadmiral-member-3
  collectedFields:
    metadata:
      creationTimestamp: "2023-11-23T11:09:04Z"
    spec:
      replicas: 2
    status:
      availableReplicas: 2
      conditions:
      - lastTransitionTime: "2023-11-23T11:09:09Z"
        lastUpdateTime: "2023-11-23T11:09:09Z"
        message: Deployment has minimum availability.
        reason: MinimumReplicasAvailable
        status: "True"
        type: Available
      - lastTransitionTime: "2023-11-23T11:09:04Z"
        lastUpdateTime: "2023-11-23T11:09:10Z"
        message: ReplicaSet "echo-server-65dcc57996" has successfully progressed.
        reason: NewReplicaSetAvailable
        status: "True"
        type: Progressing
      observedGeneration: 1
      readyReplicas: 2
      replicas: 2
      updatedReplicas: 2
kind: CollectedStatus
lastUpdateTime: "2023-11-23T11:08:48Z"
metadata:
  annotations:
    kubeadmiral.io/latest-replicaset-digests: '[]'
  creationTimestamp: "2023-11-23T11:08:48Z"
  generation: 6
  labels:
    apps/v1: Deployment
    kubeadmiral.io/propagation-policy-name: policy-all-clusters
  name: echo-server-deployments.apps
  namespace: default
  ownerReferences:
  - apiVersion: core.kubeadmiral.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: FederatedObject
    name: echo-server-deployments.apps
    uid: a29498af-ad56-484e-92ee-5a967d595d2b
  resourceVersion: "8333679"
  uid: 9f1b06b5-5070-45f7-bcdc-4b8e1fa01226
```

🎉 We have successfully propagated a deployment using KubeAdmiral.

## Uninstallation

You can use the following command to clean up the local kind clusters related with `KubeAdmiral`. This command does the following tasks:

1. Deletes the local kind clusters which name includes `kubeadmiral`.
2. Deletes the local kubeconfig files associated with the above clusters.

```console
$ make clean-local-cluster
```
