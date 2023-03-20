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
	batchv1b1 "k8s.io/api/batch/v1beta1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/resources"
)

var _ = ginkgo.Describe("CronJob Propagation", resourcePropagationTestLabel, func() {
	f := framework.NewFramework("cronjob-propagation", framework.FrameworkOptions{CreateNamespace: true})

	resourcePropagationTest(
		f,
		&resourcePropagationTestConfig[*batchv1b1.CronJob]{
			gvr:           batchv1b1.SchemeGroupVersion.WithResource("jobs"),
			objectFactory: resources.GetSimpleCronJob,
			clientGetter: func(client kubernetes.Interface, namespace string) resourceClient[*batchv1b1.CronJob] {
				return client.BatchV1beta1().CronJobs(namespace)
			},
			isPropagatedResourceWorking: func(
				_ kubernetes.Interface,
				_ dynamic.Interface,
				cronjob *batchv1b1.CronJob,
			) (bool, error) {
				return resources.IsCronJobScheduledOnce(cronjob), nil
			},
		},
	)
})
