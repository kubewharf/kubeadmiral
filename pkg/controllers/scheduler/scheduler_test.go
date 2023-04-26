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
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func TestGetSchedulingUnit(t *testing.T) {
	g := gomega.NewWithT(t)

	fedObj := fedtypesv1a1.GenericObjectWithPlacements{
		TypeMeta: metav1.TypeMeta{
			APIVersion: fedtypesv1a1.SchemeGroupVersion.String(),
			Kind:       "FederatedDeployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: fedtypesv1a1.GenericSpecWithPlacements{
			Placements: []fedtypesv1a1.PlacementWithController{
				{
					Controller: "test-controller",
					Placement: fedtypesv1a1.Placement{
						Clusters: []fedtypesv1a1.GenericClusterReference{
							{Name: "cluster-1"},
							{Name: "cluster-2"},
							{Name: "cluster-3"},
						},
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
			ReplicaRescheduling: &fedcorev1a1.ReplicaRescheduling{
				AvoidDisruption: false,
			},
		},
	}

	fedObjUns, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&fedObj)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	templateUns, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&template)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	err = unstructured.SetNestedMap(fedObjUns, templateUns, common.TemplatePath...)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	typeConfig := &fedcorev1a1.FederatedTypeConfig{
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			TargetType: fedcorev1a1.APIResource{
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

	su, err := schedulingUnitForFedObject(typeConfig, &unstructured.Unstructured{Object: fedObjUns}, &policy)
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
					StickyCluster: true,
					ClusterSelector: map[string]string{
						"label": "value1",
					},
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
					StickyCluster: true,
					ClusterSelector: map[string]string{
						"label": "value1",
					},
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
					StickyCluster:  true,
					ClusterSelector: map[string]string{
						"label": "value1",
					},
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
					StickyCluster: true,
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
					Placements: []fedcorev1a1.Placement{
						{
							ClusterName: "cluster1",
						},
					},
				},
			},
			annotations: map[string]string{
				PlacementsAnnotations: `[
					{
						"clusterName": "cluster1",
						"preferences": {
							"minReplicas": 5,
							"maxReplicas": 10,
							"weight": 2
						}
					},
					{
						"clusterName": "cluster2",
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

			obj := &unstructured.Unstructured{Object: make(map[string]interface{})}
			obj.SetAnnotations(test.annotations)
			err = unstructured.SetNestedMap(obj.Object, make(map[string]interface{}), common.TemplatePath...)
			g.Expect(err).NotTo(gomega.HaveOccurred())

			typeConfig := &fedcorev1a1.FederatedTypeConfig{
				Spec: fedcorev1a1.FederatedTypeConfigSpec{
					PathDefinition: fedcorev1a1.PathDefinition{
						ReplicasSpec: "spec.replicas",
					},
				},
			}
			su, err := schedulingUnitForFedObject(typeConfig, obj, test.policy)
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
	tests := map[string]struct {
		policy           fedcorev1a1.GenericPropagationPolicy
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
					TargetType: fedcorev1a1.APIResource{
						Group:   test.gvk.Group,
						Version: test.gvk.Version,
						Kind:    test.gvk.Kind,
					},
					PathDefinition: fedcorev1a1.PathDefinition{
						ReplicasSpec: test.replicasSpecPath,
					},
				},
			}
			obj := &unstructured.Unstructured{Object: make(map[string]interface{})}
			err := unstructured.SetNestedMap(obj.Object, make(map[string]interface{}), common.TemplatePath...)
			g.Expect(err).NotTo(gomega.HaveOccurred())
			su, err := schedulingUnitForFedObject(typeConfig, obj, test.policy)
			g.Expect(err).NotTo(gomega.HaveOccurred())
			g.Expect(su.SchedulingMode).To(gomega.Equal(test.expectedResult))
		})
	}
}
