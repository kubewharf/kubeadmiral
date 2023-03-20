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
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"

	"github.com/kubewharf/kubeadmiral/test/e2e/framework"
	"github.com/kubewharf/kubeadmiral/test/e2e/framework/resources"
)

func getJobWithoutManualSelector(baseName string) *batchv1.Job {
	return resources.GetSimpleJob(baseName)
}

func getJobWithManualSelector(baseName string) *batchv1.Job {
	job := resources.GetSimpleJob(baseName)

	jobLabels := map[string]string{
		"kubeadmiral-e2e": baseName,
	}

	job.Spec.ManualSelector = pointer.Bool(true)
	job.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: jobLabels,
	}
	job.Spec.Template.Labels = jobLabels

	return job
}

var _ = ginkgo.Context("Job Propagation", resourcePropagationTestLabel, func() {
	testCases := []struct {
		name       string
		jobFactory func(string) *batchv1.Job
	}{
		{
			name:       "Without manual selector",
			jobFactory: getJobWithoutManualSelector,
		},
		{
			name:       "With manual selector",
			jobFactory: getJobWithManualSelector,
		},
	}

	f := framework.NewFramework(
		"job-propagation",
		framework.FrameworkOptions{CreateNamespace: true},
	)

	for _, testCase := range testCases {
		ginkgo.Context(testCase.name, func() {
			resourcePropagationTest(
				f,
				&resourcePropagationTestConfig[*batchv1.Job]{
					gvr:           batchv1.SchemeGroupVersion.WithResource("jobs"),
					objectFactory: testCase.jobFactory,
					clientGetter: func(client kubernetes.Interface, namespace string) resourceClient[*batchv1.Job] {
						return client.BatchV1().Jobs(namespace)
					},
					isPropagatedResourceWorking: func(
						_ kubernetes.Interface,
						_ dynamic.Interface,
						job *batchv1.Job) (bool, error) {
						return resources.IsJobComplete(job), nil
					},
				},
			)
		})
	}
})
