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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/annotation"
)

var (
	dpFTC = &fedcorev1a1.FederatedTypeConfig{
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			PathDefinition: fedcorev1a1.PathDefinition{
				ReplicasSpec: "spec.replicas",
			},
			SourceType: fedcorev1a1.APIResource{
				Group:      "apps",
				Version:    "v1",
				Kind:       "Deployment",
				PluralName: "deployments",
				Scope:      "Namespaced",
			},
		},
	}
	fdp = &fedcorev1a1.FederatedObject{
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{Raw: []byte(`
{
    "apiVersion": "apps/v1",
    "kind": "Deployment",
    "metadata": {
        "annotations": {
            "deployment.kubernetes.io/revision": "1"
        },
        "name": "nginx-deployment",
        "namespace": "default"
    },
    "spec": {
        "progressDeadlineSeconds": 600,
        "replicas": 2,
        "revisionHistoryLimit": 10,
        "selector": {
            "matchLabels": {
                "app": "nginx"
            }
        },
        "strategy": {
            "rollingUpdate": {
                "maxSurge": "25%",
                "maxUnavailable": "25%"
            },
            "type": "RollingUpdate"
        },
        "template": {
            "metadata": {
                "creationTimestamp": null,
                "labels": {
                    "app": "nginx"
                }
            },
            "spec": {
                "containers": [
                    {
                        "image": "nginx:1.14.2",
                        "imagePullPolicy": "IfNotPresent",
                        "name": "nginx",
                        "ports": [
                            {
                                "containerPort": 80,
                                "protocol": "TCP"
                            }
                        ],
                        "resources": {},
                        "terminationMessagePath": "/dev/termination-log",
                        "terminationMessagePolicy": "File"
                    }
                ],
                "dnsPolicy": "ClusterFirst",
                "restartPolicy": "Always",
                "schedulerName": "default-scheduler",
                "securityContext": {},
                "serviceAccount": "test",
                "serviceAccountName": "test",
                "terminationGracePeriodSeconds": 30
            }
        }
    }
}
`)},
		},
	}

	saFTC = &fedcorev1a1.FederatedTypeConfig{
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			SourceType: fedcorev1a1.APIResource{
				Group:      "",
				Version:    "v1",
				Kind:       "ServiceAccount",
				PluralName: "serviceaccounts",
				Scope:      "Namespaced",
			},
		},
	}
	fsa = &fedcorev1a1.FederatedObject{
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{Raw: []byte(`
{
    "apiVersion": "v1",
    "automountServiceAccountToken": false,
    "kind": "ServiceAccount",
    "metadata": {
        "annotations": {},
        "labels": {},
        "name": "test",
        "namespace": "default"
    }
}
`)},
		},
	}
)

func Test_isClusterTriggerChanged(t *testing.T) {
	tests := []struct {
		name        string
		newClusters map[string]string
		oldClusters map[string]string
		want        bool
	}{
		{"new 0, old 0", nil, nil, false},
		{"new 0, old 1", nil, map[string]string{"": ""}, true},
		{"new 1, old 0", map[string]string{"": ""}, nil, false},
		{"new 1, old 1, unchanged", map[string]string{"": ""}, map[string]string{"": ""}, false},
		{"new 1, old 1, value changed", map[string]string{"1": "1"}, map[string]string{"1": ""}, true},
		{"new 1, old 1, key changed", map[string]string{"2": "1"}, map[string]string{"1": "1"}, true},
		{"new 2, old 1, unchanged 1", map[string]string{"1": "1", "2": "2"}, map[string]string{"1": "1"}, false},
		{"new 2, old 1, unchanged 2", map[string]string{"1": "1", "2": "2"}, map[string]string{"2": "2"}, false},
		{"new 2, old 1, value1 changed", map[string]string{"1": "1", "2": "2"}, map[string]string{"1": "2"}, true},
		{"new 2, old 1, value2 changed", map[string]string{"1": "1", "2": "2"}, map[string]string{"2": "1"}, true},
		{"new 2, old 1, key changed", map[string]string{"1": "1", "2": "2"}, map[string]string{"3": "3"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClusterTriggerChanged(tt.newClusters, tt.oldClusters); got != tt.want {
				t.Errorf("isClusterTriggerChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_computeSchedulingAnnotations(t *testing.T) {
	type Status struct {
		policyName       string
		reschedulePolicy *fedcorev1a1.ReschedulePolicy

		clusterNames        []string
		clusterLabels       []map[string]string
		clusterTaints       [][]corev1.Taint
		clusterAPIResources [][]fedcorev1a1.APIResource
	}

	tests := []struct {
		name string

		withReplicas   bool
		withOldTrigger bool
		oldStatus      Status
		newStatus      Status

		wantTriggersChanged bool
		wantDeferredReasons string
		wantErr             bool
	}{
		{
			name:           "dp, new schedule",
			withReplicas:   true,
			withOldTrigger: false,
			oldStatus:      Status{},
			newStatus: Status{
				policyName:          "pp1",
				reschedulePolicy:    &fedcorev1a1.ReschedulePolicy{DisableRescheduling: true},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: true,
			wantDeferredReasons: "",
			wantErr:             false,
		},
		{
			name:           "dp, change pp",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &fedcorev1a1.ReschedulePolicy{DisableRescheduling: true},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName:          "pp1",
				reschedulePolicy:    &fedcorev1a1.ReschedulePolicy{DisableRescheduling: true},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: true,
			wantDeferredReasons: "",
			wantErr:             false,
		},
		{
			name:           "dp, change pp PolicyContentChanged from true to false",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					PolicyContentChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: false,
			wantDeferredReasons: "policyContentChanged:false",
			wantErr:             false,
		},
		{
			name:           "dp, change pp PolicyContentChanged from false to true",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					PolicyContentChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: true,
			wantDeferredReasons: "",
			wantErr:             false,
		},
		{
			name:           "dp, join clusters, with ClusterJoined",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterJoined: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterJoined: true,
				}},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: true,
			wantDeferredReasons: "",
			wantErr:             false,
		},
		{
			name:           "dp, join clusters, without ClusterJoined",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterJoined: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterJoined: false,
				}},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: false,
			wantDeferredReasons: "clusterJoined:false",
			wantErr:             false,
		},
		{
			name:           "dp, change cluster labels, with ClusterLabelsChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterLabelsChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterLabelsChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: true,
			wantDeferredReasons: "",
			wantErr:             false,
		},
		{
			name:           "dp, change cluster labels, without ClusterLabelsChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterLabelsChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterLabelsChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: false,
			wantDeferredReasons: "clusterLabelsChanged:false",
			wantErr:             false,
		},
		{
			name:           "dp, change cluster apiresources, with ClusterAPIResourcesChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterAPIResourcesChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterAPIResourcesChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged: true,
			wantDeferredReasons: "",
			wantErr:             false,
		},
		{
			name:           "dp, change cluster apiresources, without ClusterAPIResourcesChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged: false,
			wantDeferredReasons: "clusterAPIResourcesChanged:false",
			wantErr:             false,
		},
		{
			name:           "dp, change pp contents, join cluster, change labels and apiresources, disableRescheduling",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{
					DisableRescheduling: true,
				},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged: false,
			wantDeferredReasons: "disableRescheduling:true",
			wantErr:             false,
		},
		{
			name:           "dp, change pp contents, join cluster, change labels and apiresources, empty triggers",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{
					ReplicaRescheduling: &fedcorev1a1.ReplicaRescheduling{},
				},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged: false,
			wantDeferredReasons: "rescheduleWhen:nil",
			wantErr:             false,
		},
		{
			name:           "dp, change pp, join cluster, change labels and apiresources, with unchanged deferred reasons, disabled all triggers",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{
					Trigger:             &fedcorev1a1.RescheduleTrigger{},
					ReplicaRescheduling: &fedcorev1a1.ReplicaRescheduling{},
				},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterAPIResources: [][]fedcorev1a1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged: false,
			wantDeferredReasons: "policyContentChanged:false; clusterLabelsChanged:false; clusterAPIResourcesChanged:false; clusterJoined:false",
			wantErr:             false,
		},
		{
			name:           "dp, change cluster taints",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       [][]corev1.Taint{{{Key: "foo", Value: "bar", Effect: corev1.TaintEffectNoSchedule}}},
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       [][]corev1.Taint{{{Key: "foo", Value: "bar", Effect: corev1.TaintEffectNoExecute}}},
				clusterAPIResources: nil,
			},
			wantTriggersChanged: true,
			wantDeferredReasons: "",
			wantErr:             false,
		},
		{
			name:           "dp, remove clusters",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &fedcorev1a1.ReschedulePolicy{Trigger: &fedcorev1a1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster2"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterAPIResources: nil,
			},
			wantTriggersChanged: true,
			wantDeferredReasons: "",
			wantErr:             false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ftc, fedObj := generateFTCAndFedObj(tt.withReplicas)
			if tt.withOldTrigger {
				oldPolicy := generatePolicy(tt.oldStatus.policyName, tt.oldStatus.reschedulePolicy)
				oldClusters := generateFClusters(
					tt.oldStatus.clusterNames,
					tt.oldStatus.clusterLabels,
					tt.oldStatus.clusterTaints,
					tt.oldStatus.clusterAPIResources,
				)
				oldTrigger, err := computeSchedulingTriggers(ftc, fedObj, oldPolicy, oldClusters)
				if err != nil {
					t.Errorf("computeSchedulingTriggers() unexpected err: %v", err)
					return
				}
				oldTriggerText, err := oldTrigger.Marshal()
				if err != nil {
					t.Errorf("Marshal() unexpected err: %v", err)
					return
				}
				_, err = annotation.AddAnnotation(fedObj, SchedulingTriggersAnnotation, oldTriggerText)
				if err != nil {
					t.Errorf("AddAnnotation() unexpected err: %v", err)
					return
				}
			}

			newPolicy := generatePolicy(tt.newStatus.policyName, tt.newStatus.reschedulePolicy)
			newClusters := generateFClusters(
				tt.newStatus.clusterNames,
				tt.newStatus.clusterLabels,
				tt.newStatus.clusterTaints,
				tt.newStatus.clusterAPIResources,
			)
			newTrigger, err := computeSchedulingTriggers(ftc, fedObj, newPolicy, newClusters)
			if err != nil {
				t.Errorf("computeSchedulingTriggers() unexpected err: %v", err)
				return
			}

			_, deferredReasons, triggersChanged, err := computeSchedulingAnnotations(context.TODO(), newTrigger, fedObj, newPolicy)
			if (err != nil) != tt.wantErr {
				t.Errorf("computeSchedulingAnnotations() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if triggersChanged != tt.wantTriggersChanged {
				t.Errorf("computeSchedulingAnnotations() triggersChanged = %v, want = %v",
					triggersChanged, tt.wantTriggersChanged)
			}
			if deferredReasons != tt.wantDeferredReasons {
				t.Errorf("computeSchedulingAnnotations() deferredReasons = %v, want = %v",
					deferredReasons, tt.wantDeferredReasons)
			}
		})
	}
}

func generateFTCAndFedObj(
	withReplicas bool,
) (ftc *fedcorev1a1.FederatedTypeConfig, fedObj fedcorev1a1.GenericFederatedObject) {
	if !withReplicas {
		return saFTC.DeepCopy(), fsa.DeepCopy()
	}
	return dpFTC.DeepCopy(), fdp.DeepCopy()
}

func generatePolicy(
	name string,
	reschedulePolicy *fedcorev1a1.ReschedulePolicy,
) fedcorev1a1.GenericPropagationPolicy {
	return &fedcorev1a1.PropagationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: fedcorev1a1.PropagationPolicySpec{ReschedulePolicy: reschedulePolicy},
	}
}

func generateFClusters(
	names []string,
	labels []map[string]string,
	taints [][]corev1.Taint,
	apiResources [][]fedcorev1a1.APIResource,
) []*fedcorev1a1.FederatedCluster {
	clusters := make([]*fedcorev1a1.FederatedCluster, 0, len(names))
	for i, name := range names {
		cluster := &fedcorev1a1.FederatedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}
		if i < len(labels) {
			cluster.Labels = labels[i]
		}
		if i < len(taints) {
			cluster.Spec.Taints = taints[i]
		}
		if i < len(apiResources) {
			cluster.Status.APIResourceTypes = apiResources[i]
		}
		clusters = append(clusters, cluster)
	}
	return clusters
}
