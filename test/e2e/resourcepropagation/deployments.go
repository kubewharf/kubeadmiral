/*
Copyright 2023 The KubeAdmiral Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resourcepropagation

import (
	"github.com/onsi/ginkgo/v2"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/resources"
)

var _ = ginkgo.Describe("Deployment Propagation", func() {
	f := framework.NewFramework("deployment-propagation", framework.FrameworkOptions{CreateNamespace: true})

	ginkgo.It("Should succeed", resourcePropagationTestLabel, func(ctx ginkgo.SpecContext) {
		resourcePropagationTest(
			f,
			&resourcePropagationTestConfig[*appsv1.Deployment]{
				gvr:           appsv1.SchemeGroupVersion.WithResource("deployments"),
				gvk:           appsv1.SchemeGroupVersion.WithKind("Deployment"),
				objectFactory: resources.GetSimpleDeployment,
				clientGetter: func(client kubernetes.Interface, namespace string) resourceClient[*appsv1.Deployment] {
					return client.AppsV1().Deployments(namespace)
				},
				isPropagatedResourceWorking: func(
					_ kubernetes.Interface,
					_ dynamic.Interface,
					deployment *appsv1.Deployment,
				) (bool, error) {
					return resources.IsDeploymentProgressing(deployment), nil
				},
				statusCollection: &resourceStatusCollectionTestConfig{
					path: "status",
				},
			},
			ctx,
		)
	})
})
