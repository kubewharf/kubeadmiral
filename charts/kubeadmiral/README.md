## Installation KubeAdmiral by Helm Chart

### Prerequisites

Make sure the following tools are installed in the environment before installing KubeAdmiral:

- Kubernetes cluster version v1.20.15+
- [Helm](https://helm.sh/) version v3+
- [Kubectl](https://github.com/kubernetes/kubectl) version v0.20.15+

### Installation steps

If you already have a Kubernetes cluster, you can install the KubeAdmiral control plane on your cluster using the helm chart. To install KubeAdmiral, follow these steps:

1.Get the Chart package for KubeAdmiral and install it:

Get the Chart package locally and install it.

```Bash
$ git clone https://github.com/kubewharf/kubeadmiral

$ cd kubeadmiral

$ helm install kubeadmiral -n kubeadmiral-system --create-namespace --dependency-update ./charts/kubeadmiral
```

2.Wait and check if the package has been installed successfully

Use your Kubernetes cluster kubeconfig to see if the following components of KubeAdmiral have been successfully running:

```Bash
$ kubectl get pods -n kubeadmiral-system

NAME                                                  READY   STATUS    RESTARTS      AGE
etcd-0                                                1/1     Running   0             13h
kubeadmiral-apiserver-5767cd4f56-gvnqq                1/1     Running   0             13h
kubeadmiral-controller-manager-5f598574c9-zjmf9       1/1     Running   0             13h
kubeadmiral-hpa-aggregator-59ccd7b484-phbr6           2/2     Running   0             13h
kubeadmiral-kube-controller-manager-6bd7dcf67-2zpqw   1/1     Running   2 (13h ago)   13h
```

3.Export the kubeconfig of KubeAdmiral

After executing the following command, the kubeconfig for connecting to KubeAdmiral will be exported to the kubeadmiral-kubeconfig file.

> Note that the address in the kubeconfig is set to the internal service address of KubeAdmiral-apiserver:

```Bash
$ kubectl get secret -n kubeadmiral-system kubeadmiral-kubeconfig-secret -o jsonpath={.data.kubeconfig} | base64 -d > kubeadmiral-kubeconfig
```

If you specified an external address when installing KubeAdmiral, we will automatically generate a kubeconfig using the external address. You can export it to the external-kubeadmiral-kubeconfig file by running the following command:

```Bash
$ kubectl get secret -n kubeadmiral-system kubeadmiral-kubeconfig-secret -o jsonpath={.data.external-kubeconfig} | base64 -d > external-kubeadmiral-kubeconfig
```

### Uninstallation steps

Uninstall the KubeAdmiral Helm chart in the kubeadmiral-system namespace:

```Bash
$ helm uninstall -n kubeadmiral-system kubeadmiral 
```

This command will delete all Kubernetes resources associated with the Chart:

> Note: The following permissions and namespace resources are relied on when installing and uninstalling helmchart, so they cannot be deleted automatically and require you to clean them up manually.

```Bash
$ kubectl delete clusterrole kubeadmiral-pre-install-job

$ kubectl delete clusterrolebinding kubeadmiral-pre-install-job

$ kubectl delete ns kubeadmiral-system
```

### Configuration parameters

| Name                                          | Description                                                  | Default Value                                                |
| --------------------------------------------- | ------------------------------------------------------------ | ------------------------------------------------------------ |
| clusterDomain                                 | Default cluster domain of Kubernetes cluster                 | "cluster.local"                                              |
| etcd.image.name                               | Image name used by KubeAdmiral etcd                          | "registry.k8s.io/etcd:3.4.13-0"                              |
| etcd.image.pullPolicy                         | Pull mode of etcd image                                      | "IfNotPresent"                                               |
| etcd.certHosts                                | Hosts accessible with etcd certificate                       | ["kubernetes.default.svc", ".etcd.{{ .Release.Namespace }}.svc.{{ .Values.clusterDomain }}", "*.{{ .Release.Namespace }}.svc.{{ .Values.clusterDomain }}", "*.{{ .Release.Namespace }}.svc", "localhost", "127.0.0.1"] |
| apiServer.image.name                          | Image name of kubeadmiral-apiserver                          | "registry.k8s.io/kube-apiserver:v1.20.15"                    |
| apiServer.image.pullPolicy                    | Pull mode of kubeadmiral-apiserver image                     | "IfNotPresent"                                               |
| apiServer.certHosts                           | Hosts supported by kubeadmiral-apiserver certificate         | ["kubernetes.default.svc", ".etcd.{{ .Release.Namespace }}.svc.{{ .Values.clusterDomain }}", "*.{{ .Release.Namespace }}.svc.{{ .Values.clusterDomain }}", "*.{{ .Release.Namespace }}.svc", "localhost", "127.0.0.1"] |
| apiServer.hostNetwork                         | Deploy kubeadmiral-apiserver with hostNetwork. If there are multiple kubeadmirals in one cluster, you'd better set it to "false" | "false"                                                      |
| apiServer.serviceType                         | Service type of kubeadmiral-apiserver                        | "ClusterIP"                                                  |
| apiServer.externalIP                          | Exposed IP of kubeadmiral-apiserver. If you want to expose the apiserver to the outside, you can set this field, which will write the external IP into the certificate and generate a kubeconfig with the external IP. | ""                                                           |
| apiServer.nodePort                            | Node port used for the 'apiserver'. This will take effect when 'apiServer.serviceType' is set to 'NodePort'. If no port is specified, a node port will be automatically assigned. | 0                                                            |
| apiServer.certHosts                           | Hosts supported by the kubeadmiral-apiserver certificate     | ["kubernetes.default.svc", ".etcd.{{ .Release.Namespace }}.svc.{{ .Values.clusterDomain }}", "*.{{ .Release.Namespace }}.svc.{{ .Values.clusterDomain }}", "*.{{ .Release.Namespace }}.svc", "localhost", "127.0.0.1", "{{ .Values.apiServer.externalIP }}"] |
| kubeControllerManager.image.name              | Image name of kube-controller-manager                        | "registry.k8s.io/kube-controller-manager:v1.20.15"           |
| kubeControllerManager.image.pullPolicy        | Pull mode of kube-controller-manager image                   | "IfNotPresent"                                               |
| kubeControllerManager.controllers             | Controllers that kube-controller-manager component needs to start | "namespace,garbagecollector"                                 |
| kubeadmiralControllerManager.image.name       | Image name of kubeadmiral-controller-manager                 | "docker.io/kubewharf/kubeadmiral-controller-manager:v1.0.0"  |
| kubeadmiralControllerManager.image.pullPolicy | Pull mode of kubeadmiral-controller-manager image            | "IfNotPresent"                                               |
| kubeadmiralControllerManager.extraCommandArgs | Additional startup parameters of kubeadmiral-controller-manager | {}                                                           |
| kubeadmiralHpaAggregator.image.name           | Image name of kubeadmiral-hpa-aggregator                     | "docker.io/kubewharf/kubeadmiral-hpa-aggregator:v1.0.0"      |
| kubeadmiralHpaAggregator.image.pullPolicy     | Pull mode of kubeadmiral-hpa-aggregator image                | "IfNotPresent"                                               |
| kubeadmiralHpaAggregator.extraCommandArgs     | Additional startup parameters of kubeadmiral-hpa-aggregator  | {}                                                           |
| installTools.cfssl.image.name                 | cfssl image name for KubeAdmiral installer                   | "docker.io/cfssl/cfssl:latest"                               |
| installTools.cfssl.image.pullPolicy           | cfssl image pull policy                                      | "IfNotPresent"                                               |
| installTools.kubectl.image.name               | kubectl image name for KubeAdmiral installer                 | "docker.io/bitnami/kubectl:1.22.10"                          |
| installTools.kubectl.image.pullPolicy         | kubectl image pull policy                                    | "IfNotPresent"                                               |
