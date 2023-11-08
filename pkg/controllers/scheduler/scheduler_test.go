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

package scheduler

import (
	"testing"

	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func TestGetSchedulingUnit(t *testing.T) {
	g := gomega.NewWithT(t)

	fedObj := fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Placements: []fedcorev1a1.PlacementWithController{
				{
					Controller: "test-controller",
					Placement: []fedcorev1a1.ClusterReference{
						{Cluster: "cluster-1"},
						{Cluster: "cluster-2"},
						{Cluster: "cluster-3"},
					},
				},
			},
		},
	}

	template := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       common.DeploymentKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Labels: map[string]string{
				"foo": "bar",
			},
			Annotations: map[string]string{
				"baz": "qux",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(10),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "test",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "test",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "nginx",
						},
					},
				},
			},
		},
	}

	policy := fedcorev1a1.PropagationPolicy{
		Spec: fedcorev1a1.PropagationPolicySpec{
			SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
			AutoMigration: &fedcorev1a1.AutoMigration{
				KeepUnschedulableReplicas: false,
			},
			ReschedulePolicy: &fedcorev1a1.ReschedulePolicy{
				ReplicaRescheduling: &fedcorev1a1.ReplicaRescheduling{
					AvoidDisruption: false,
				},
			},
		},
	}

	rawJSON, err := json.Marshal(&template)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	fedObj.Spec.Template.Raw = rawJSON

	typeConfig := &fedcorev1a1.FederatedTypeConfig{
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			SourceType: fedcorev1a1.APIResource{
				Group:      "apps",
				Version:    "v1",
				Kind:       "Deployment",
				PluralName: "deployments",
				Scope:      "Namespaced",
			},
			PathDefinition: fedcorev1a1.PathDefinition{
				ReplicasSpec: "spec.replicas",
			},
		},
	}

	su, err := schedulingUnitForFedObject(typeConfig, &fedObj, &policy, nil)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	g.Expect(su).To(gomega.Equal(&framework.SchedulingUnit{
		GroupVersion: schema.GroupVersion{Group: "apps", Version: "v1"},
		Kind:         "Deployment",
		Resource:     "deployments",
		Namespace:    "default",
		Name:         "test",
		Labels: map[string]string{
			"foo": "bar",
		},
		Annotations: map[string]string{
			"baz": "qux",
		},
		ResourceRequest: framework.Resource{},
		CurrentClusters: map[string]*int64{},
		AutoMigration: &framework.AutoMigrationSpec{
			Info:                      nil,
			KeepUnschedulableReplicas: false,
		},
		SchedulingMode:  fedcorev1a1.SchedulingModeDuplicate,
		StickyCluster:   false,
		AvoidDisruption: false,
	}))
}

func TestGetSchedulingUnitWithAnnotationOverrides(t *testing.T) {
	tests := []struct {
		name           string
		policy         fedcorev1a1.GenericPropagationPolicy
		annotations    map[string]string
		expectedResult *framework.SchedulingUnit
	}{
		{
			name: "scheduling mode override",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					SchedulingMode: fedcorev1a1.SchedulingModeDivide,
					ClusterSelector: map[string]string{
						"label": "value1",
					},
				},
			},
			annotations: map[string]string{
				SchedulingModeAnnotation: string(fedcorev1a1.SchedulingModeDuplicate),
			},
			expectedResult: &framework.SchedulingUnit{
				SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
				ClusterSelector: map[string]string{
					"label": "value1",
				},
			},
		},
		{
			name: "sticky cluster override",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					ClusterSelector: map[string]string{
						"label": "value1",
					},
					ReschedulePolicy: &fedcorev1a1.ReschedulePolicy{DisableRescheduling: true},
				},
			},
			annotations: map[string]string{
				StickyClusterAnnotation: "false",
			},
			expectedResult: &framework.SchedulingUnit{
				SchedulingMode: DefaultSchedulingMode,
				StickyCluster:  false,
				ClusterSelector: map[string]string{
					"label": "value1",
				},
			},
		},
		{
			name: "Cluster selector override",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					ClusterSelector: map[string]string{
						"label": "value1",
					},
					ReschedulePolicy: &fedcorev1a1.ReschedulePolicy{DisableRescheduling: true},
				},
			},
			annotations: map[string]string{
				ClusterSelectorAnnotations: "{\"override\": \"label\"}",
			},
			expectedResult: &framework.SchedulingUnit{
				SchedulingMode: DefaultSchedulingMode,
				StickyCluster:  true,
				ClusterSelector: map[string]string{
					"override": "label",
				},
			},
		},
		{
			name: "cluster affinity override",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
					ClusterSelector: map[string]string{
						"label": "value1",
					},
					ReschedulePolicy: &fedcorev1a1.ReschedulePolicy{DisableRescheduling: true},
				},
			},
			annotations: map[string]string{
				AffinityAnnotations: `{
					"clusterAffinity": {
						"requiredDuringSchedulingIgnoredDuringExecution": {
							"clusterSelectorTerms": [
								{
									"matchExpressions": [
										{
											"key": "test",
											"operator": "In",
											"values": ["value1", "value2"]
										}
									]
								}
							]
						}
					}
				}`,
			},
			expectedResult: &framework.SchedulingUnit{
				SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
				StickyCluster:  true,
				ClusterSelector: map[string]string{
					"label": "value1",
				},
				Affinity: &framework.Affinity{
					ClusterAffinity: &framework.ClusterAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &framework.ClusterSelector{
							ClusterSelectorTerms: []fedcorev1a1.ClusterSelectorTerm{
								{
									MatchExpressions: []fedcorev1a1.ClusterSelectorRequirement{
										{
											Key:      "test",
											Operator: fedcorev1a1.ClusterSelectorOpIn,
											Values:   []string{"value1", "value2"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "Tolerations override",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					ClusterSelector: map[string]string{
						"label": "value1",
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      "test",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoExecute,
						},
					},
					ReschedulePolicy: &fedcorev1a1.ReschedulePolicy{DisableRescheduling: true},
				},
			},
			annotations: map[string]string{
				TolerationsAnnotations: "[{\"key\": \"override\", \"operator\": \"Exists\", \"effect\": \"NoSchedule\"}]",
			},
			expectedResult: &framework.SchedulingUnit{
				SchedulingMode: DefaultSchedulingMode,
				StickyCluster:  true,
				ClusterSelector: map[string]string{
					"label": "value1",
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "override",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
			},
		},
		{
			name: "Max clusters override",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					ClusterSelector: map[string]string{
						"label": "value1",
					},
					MaxClusters: pointer.Int64(5),
				},
			},
			annotations: map[string]string{
				MaxClustersAnnotations: "10",
			},
			expectedResult: &framework.SchedulingUnit{
				SchedulingMode: DefaultSchedulingMode,
				ClusterSelector: map[string]string{
					"label": "value1",
				},
				MaxClusters: pointer.Int64(10),
			},
		},
		{
			name: "Placements override",
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					ClusterSelector: map[string]string{
						"label": "value1",
					},
					MaxClusters: pointer.Int64(5),
					Placements: []fedcorev1a1.DesiredPlacement{
						{
							Cluster: "cluster1",
						},
					},
				},
			},
			annotations: map[string]string{
				PlacementsAnnotations: `[
					{
						"cluster": "cluster1",
						"preferences": {
							"minReplicas": 5,
							"maxReplicas": 10,
							"weight": 2
						}
					},
					{
						"cluster": "cluster2",
						"preferences": {
							"minReplicas": 2,
							"weight": 1
						}
					}
				]`,
			},
			expectedResult: &framework.SchedulingUnit{
				SchedulingMode: DefaultSchedulingMode,
				ClusterSelector: map[string]string{
					"label": "value1",
				},
				MaxClusters: pointer.Int64(5),
				ClusterNames: map[string]struct{}{
					"cluster1": {},
					"cluster2": {},
				},
				MinReplicas: map[string]int64{
					"cluster1": 5,
					"cluster2": 2,
				},
				MaxReplicas: map[string]int64{
					"cluster1": 10,
				},
				Weights: map[string]int64{
					"cluster1": 2,
					"cluster2": 1,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := gomega.NewWithT(t)

			var err error

			testObj := &appsv1.Deployment{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Deployment",
					APIVersion: "apps/v1",
				},
			}
			rawJSON, err := json.Marshal(testObj)
			g.Expect(err).ToNot(gomega.HaveOccurred())

			obj := &fedcorev1a1.FederatedObject{}
			obj.SetAnnotations(test.annotations)
			obj.Spec.Template.Raw = rawJSON

			typeConfig := &fedcorev1a1.FederatedTypeConfig{
				Spec: fedcorev1a1.FederatedTypeConfigSpec{
					PathDefinition: fedcorev1a1.PathDefinition{
						ReplicasSpec: "spec.replicas",
					},
				},
			}
			su, err := schedulingUnitForFedObject(typeConfig, obj, test.policy, nil)
			g.Expect(err).NotTo(gomega.HaveOccurred())

			// override fields we don't want to test
			su.GroupVersion = test.expectedResult.GroupVersion
			su.Kind = test.expectedResult.Kind
			su.Resource = test.expectedResult.Resource
			su.Name = test.expectedResult.Name
			su.Namespace = test.expectedResult.Namespace
			su.Labels = test.expectedResult.Labels
			su.Annotations = test.expectedResult.Annotations
			su.DesiredReplicas = test.expectedResult.DesiredReplicas
			su.CurrentClusters = test.expectedResult.CurrentClusters
			su.ResourceRequest = test.expectedResult.ResourceRequest
			su.AvoidDisruption = test.expectedResult.AvoidDisruption

			g.Expect(su).To(gomega.Equal(test.expectedResult))
		})
	}
}

func TestSchedulingMode(t *testing.T) {
	deploymentObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deployment",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(10),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	deploymentUns := &unstructured.Unstructured{Object: deploymentObj}

	statefulSetObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "statefulset",
			Namespace: "default",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: pointer.Int32(10),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	statefulSetUns := &unstructured.Unstructured{Object: statefulSetObj}

	tests := map[string]struct {
		policy           fedcorev1a1.GenericPropagationPolicy
		obj              *unstructured.Unstructured
		gvk              schema.GroupVersionKind
		replicasSpecPath string
		expectedResult   fedcorev1a1.SchedulingMode
	}{
		"deployments should be able to use divide mode": {
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					SchedulingMode: fedcorev1a1.SchedulingModeDivide,
				},
			},
			obj:              deploymentUns,
			replicasSpecPath: "spec.replicas",
			gvk:              appsv1.SchemeGroupVersion.WithKind("Deployment"),
			expectedResult:   fedcorev1a1.SchedulingModeDivide,
		},
		"deployments should be able to use duplicate mode": {
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					SchedulingMode: fedcorev1a1.SchedulingModeDuplicate,
				},
			},
			obj:              deploymentUns,
			replicasSpecPath: "spec.replicas",
			gvk:              appsv1.SchemeGroupVersion.WithKind("Deployment"),
			expectedResult:   fedcorev1a1.SchedulingModeDuplicate,
		},
		"scheduling mode should fall back to Duplicate if there is no replicasSpecPath": {
			policy: &fedcorev1a1.PropagationPolicy{
				Spec: fedcorev1a1.PropagationPolicySpec{
					SchedulingMode: fedcorev1a1.SchedulingModeDivide,
				},
			},
			obj:              statefulSetUns,
			replicasSpecPath: "",
			gvk:              appsv1.SchemeGroupVersion.WithKind("StatefulSet"),
			expectedResult:   fedcorev1a1.SchedulingModeDuplicate,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			typeConfig := &fedcorev1a1.FederatedTypeConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "<ftc-name>",
				},
				Spec: fedcorev1a1.FederatedTypeConfigSpec{
					SourceType: fedcorev1a1.APIResource{
						Group:   test.gvk.Group,
						Version: test.gvk.Version,
						Kind:    test.gvk.Kind,
					},
					PathDefinition: fedcorev1a1.PathDefinition{
						ReplicasSpec: test.replicasSpecPath,
					},
				},
			}
			obj := &fedcorev1a1.FederatedObject{}
			rawJSON, err := json.Marshal(test.obj)
			g.Expect(err).NotTo(gomega.HaveOccurred())
			obj.Spec.Template.Raw = rawJSON

			su, err := schedulingUnitForFedObject(typeConfig, obj, test.policy, nil)
			g.Expect(err).NotTo(gomega.HaveOccurred())
			g.Expect(su.SchedulingMode).To(gomega.Equal(test.expectedResult))
		})
	}
}
