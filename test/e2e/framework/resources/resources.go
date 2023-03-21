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

package resources

import (
	"flag"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1b1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/pointer"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

var DefaultPodImage = "ealen/echo-server:latest"

func init() {
	flag.StringVar(
		&DefaultPodImage,
		"default-pod-image",
		"ealen/echo-server:latest",
		"The base pod image to use when creating test resource.",
	)
}

func GetSimpleDeployment(baseName string) *appsv1.Deployment {
	name := fmt.Sprintf("%s-%s", baseName, rand.String(12))

	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       common.DeploymentKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: DefaultPodImage,
						},
					},
				},
			},
		},
	}
}

func IsDeploymentProgressing(deployment *appsv1.Deployment) bool {
	for _, c := range deployment.Status.Conditions {
		if c.Type != appsv1.DeploymentProgressing {
			continue
		}

		return c.Status == corev1.ConditionTrue
	}

	return false
}

func GetSimpleDaemonset() *appsv1.DaemonSet {
	return nil
}

func GetSimpleStatefulset() *appsv1.StatefulSet {
	return nil
}

func GetSimpleJob(baseName string) *batchv1.Job {
	name := fmt.Sprintf("%s-%s", baseName, rand.String(12))

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       common.JobKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "main",
							Image:   DefaultPodImage,
							Command: []string{"sleep", "2"},
						},
					},
				},
			},
		},
	}
}

func IsJobComplete(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func GetSimpleCronJob(baseName string) *batchv1b1.CronJob {
	name := fmt.Sprintf("%s-%s", baseName, rand.String(12))

	return &batchv1b1.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       common.CronJobKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: batchv1b1.CronJobSpec{
			Schedule:          "* * * * *",
			ConcurrencyPolicy: "",
			JobTemplate: batchv1b1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:    "main",
									Image:   DefaultPodImage,
									Command: []string{"sleep", "2"},
								},
							},
						},
					},
				},
			},
		},
	}
}

func IsCronJobScheduledOnce(cronJob *batchv1b1.CronJob) bool {
	return cronJob.Status.LastScheduleTime != nil && !cronJob.Status.LastScheduleTime.IsZero()
}

func GetSimpleService() *corev1.Service {
	return nil
}

func GetSimpleIngress() *networkingv1.Ingress {
	return nil
}

func GetSimpleStorageClass() *storagev1.StorageClass {
	return nil
}

func GetSimplePersistentVolume() *corev1.PersistentVolume {
	return nil
}

func GetSimplePersistentVolumeClaim() *corev1.PersistentVolumeClaim {
	return nil
}

func GetSimpleRole() *rbacv1.Role {
	return nil
}

func GetSimpleRoleBinding() *rbacv1.RoleBinding {
	return nil
}

func GetSimpleClusterRole() *rbacv1.ClusterRole {
	return nil
}

func GetSimpleClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return nil
}
