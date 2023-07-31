package scheduler

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/annotation"
)

var (
	dpFTC = &v1alpha1.FederatedTypeConfig{
		Spec: v1alpha1.FederatedTypeConfigSpec{
			PathDefinition: v1alpha1.PathDefinition{
				ReplicasSpec: "spec.replicas",
			},
			SourceType: v1alpha1.APIResource{
				Group:      "apps",
				Version:    "v1",
				Kind:       "Deployment",
				PluralName: "deployments",
				Scope:      "Namespaced",
			},
		},
	}
	fdp = &v1alpha1.FederatedObject{
		Spec: v1alpha1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{Raw: []byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"annotations":{"deployment.kubernetes.io/revision":"1"},"labels":{},"name":"nginx-deployment","namespace":"default"},"spec":{"progressDeadlineSeconds":600,"replicas":2,"revisionHistoryLimit":10,"selector":{"matchLabels":{"app":"nginx"}},"strategy":{"rollingUpdate":{"maxSurge":"25%","maxUnavailable":"25%"},"type":"RollingUpdate"},"template":{"metadata":{"creationTimestamp":null,"labels":{"app":"nginx"}},"spec":{"containers":[{"image":"nginx:1.14.2","imagePullPolicy":"IfNotPresent","name":"nginx","ports":[{"containerPort":80,"protocol":"TCP"}],"resources":{},"terminationMessagePath":"/dev/termination-log","terminationMessagePolicy":"File"}],"dnsPolicy":"ClusterFirst","restartPolicy":"Always","schedulerName":"default-scheduler","securityContext":{},"serviceAccount":"wy","serviceAccountName":"wy","terminationGracePeriodSeconds":30}}}}`)},
		},
	}

	saFTC = &v1alpha1.FederatedTypeConfig{
		Spec: v1alpha1.FederatedTypeConfigSpec{
			SourceType: v1alpha1.APIResource{
				Group:      "",
				Version:    "v1",
				Kind:       "ServiceAccount",
				PluralName: "serviceaccounts",
				Scope:      "Namespaced",
			},
		},
	}
	fsa = &v1alpha1.FederatedObject{
		Spec: v1alpha1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{Raw: []byte(`{"apiVersion":"v1","automountServiceAccountToken":false,"kind":"ServiceAccount","metadata":{"annotations":{},"labels":{},"name":"wy","namespace":"default"}}`)},
		},
	}
)

func Test_isClusterTriggerChanged(t *testing.T) {
	tests := []struct {
		name        string
		newClusters []keyValue[string, string]
		oldClusters []keyValue[string, string]
		want        bool
	}{
		{"new 0, old 0", nil, nil, false},
		{"new 0, old 1", nil, []keyValue[string, string]{{"", ""}}, true},
		{"new 1, old 0", []keyValue[string, string]{{"", ""}}, nil, false},
		{"new 1, old 1, unchanged", []keyValue[string, string]{{"", ""}}, []keyValue[string, string]{{"", ""}}, false},
		{"new 1, old 1, value changed", []keyValue[string, string]{{"1", "1"}}, []keyValue[string, string]{{"1", ""}}, true},
		{"new 1, old 1, key changed", []keyValue[string, string]{{"2", "1"}}, []keyValue[string, string]{{"1", "1"}}, true},
		{"new 2, old 1, unchanged 1", []keyValue[string, string]{{"1", "1"}, {"2", "2"}}, []keyValue[string, string]{{"1", "1"}}, false},
		{"new 2, old 1, unchanged 2", []keyValue[string, string]{{"1", "1"}, {"2", "2"}}, []keyValue[string, string]{{"2", "2"}}, false},
		{"new 2, old 1, value1 changed", []keyValue[string, string]{{"1", "1"}, {"2", "2"}}, []keyValue[string, string]{{"1", "2"}}, true},
		{"new 2, old 1, value2 changed", []keyValue[string, string]{{"1", "1"}, {"2", "2"}}, []keyValue[string, string]{{"2", "1"}}, true},
		{"new 2, old 1, key changed", []keyValue[string, string]{{"1", "1"}, {"2", "2"}}, []keyValue[string, string]{{"3", "3"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClusterTriggerChanged(tt.newClusters, tt.oldClusters); got != tt.want {
				t.Errorf("isClusterTriggerChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_schedulingTriggers_updateAnnotationsIfTriggersChanged(t *testing.T) {
	type Status struct {
		policyName       string
		reschedulePolicy *v1alpha1.ReschedulePolicy

		clusterNames        []string
		clusterLabels       []map[string]string
		clusterTaints       [][]corev1.Taint
		clusterApiResources [][]v1alpha1.APIResource
	}

	tests := []struct {
		name string

		withReplicas           bool
		withOldTrigger         bool
		oldStatus              Status
		withOldDeferredReasons bool
		oldDeferredReasons     string

		newStatus Status

		wantTriggersChanged   bool
		wantAnnotationChanged bool
		wantErr               bool
	}{
		// TODO: Add test cases.
		{
			name:                   "dp, new schedule",
			withReplicas:           true,
			withOldTrigger:         false,
			oldStatus:              Status{},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName:          "pp1",
				reschedulePolicy:    &v1alpha1.ReschedulePolicy{DisableRescheduling: true},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   true,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, change pp",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &v1alpha1.ReschedulePolicy{DisableRescheduling: true},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName:          "pp1",
				reschedulePolicy:    &v1alpha1.ReschedulePolicy{DisableRescheduling: true},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   true,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, change pp PolicyContentChanged from true to false",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					PolicyContentChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, change pp PolicyContentChanged from true to false, with unchanged deferred reasons",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: true,
			oldDeferredReasons:     "policyContentChanged: false",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					PolicyContentChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: false,
			wantErr:               false,
		},
		{
			name:           "dp, change pp PolicyContentChanged from false to true",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					PolicyContentChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   true,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, join clusters, with ClusterJoined",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterJoined: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterJoined: true,
				}},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   true,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, join clusters, without ClusterJoined",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterJoined: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterJoined: false,
				}},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, join clusters with unchanged deferred reasons, without ClusterJoined",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterJoined: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: true,
			oldDeferredReasons:     "clusterJoined: false",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterJoined: false,
				}},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: false,
			wantErr:               false,
		},
		{
			name:           "dp, change cluster labels, with ClusterLabelsChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterLabelsChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterLabelsChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   true,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, change cluster labels, without ClusterLabelsChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterLabelsChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterLabelsChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, change cluster labels with unchanged deferred reasons, without ClusterLabelsChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterLabelsChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: true,
			oldDeferredReasons:     "clusterLabelsChanged: false",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterLabelsChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: false,
			wantErr:               false,
		},
		{
			name:           "dp, change cluster apiresources, with ClusterAPIResourcesChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterAPIResourcesChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterAPIResourcesChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged:   true,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, change cluster apiresources, without ClusterAPIResourcesChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, change cluster apiresources with unchanged deferred reasons, without ClusterAPIResourcesChanged",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			withOldDeferredReasons: true,
			oldDeferredReasons:     "clusterAPIResourcesChanged: false",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: false,
			wantErr:               false,
		},
		{
			name:           "dp, change pp contents, join cluster, change labels and apiresources, disabled all triggers",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{}, ReplicaRescheduling: &v1alpha1.ReplicaRescheduling{}},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, change pp, join cluster, change labels and apiresources, with unchanged deferred reasons, disabled all triggers",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       []map[string]string{{"foo": "bar"}},
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "A", Version: "v1", PluralName: "as"}}},
			},
			withOldDeferredReasons: true,
			oldDeferredReasons:     "policyContentChanged: false;clusterLabelsChanged: false;clusterAPIResourcesChanged: false;clusterJoined: false",
			newStatus: Status{
				policyName:          "pp0",
				reschedulePolicy:    &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{}, ReplicaRescheduling: &v1alpha1.ReplicaRescheduling{}},
				clusterNames:        []string{"cluster1", "cluster2"},
				clusterLabels:       []map[string]string{{"bar": "foo"}},
				clusterTaints:       nil,
				clusterApiResources: [][]v1alpha1.APIResource{{{Kind: "B", Version: "v1", PluralName: "bs"}}},
			},
			wantTriggersChanged:   false,
			wantAnnotationChanged: false,
			wantErr:               false,
		},
		{
			name:           "dp, change cluster taints",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       [][]corev1.Taint{{{Key: "foo", Value: "bar", Effect: corev1.TaintEffectNoSchedule}}},
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					ClusterAPIResourcesChanged: false,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       [][]corev1.Taint{{{Key: "foo", Value: "bar", Effect: corev1.TaintEffectNoExecute}}},
				clusterApiResources: nil,
			},
			wantTriggersChanged:   true,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
		{
			name:           "dp, remove clusters",
			withReplicas:   true,
			withOldTrigger: true,
			oldStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster1"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			withOldDeferredReasons: false,
			oldDeferredReasons:     "",
			newStatus: Status{
				policyName: "pp0",
				reschedulePolicy: &v1alpha1.ReschedulePolicy{Trigger: &v1alpha1.RescheduleTrigger{
					PolicyContentChanged: true,
				}},
				clusterNames:        []string{"cluster2"},
				clusterLabels:       nil,
				clusterTaints:       nil,
				clusterApiResources: nil,
			},
			wantTriggersChanged:   true,
			wantAnnotationChanged: true,
			wantErr:               false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ftc, fedObj := generateFTCAndFedObj(tt.withReplicas)
			if tt.withOldTrigger {
				oldPolicy := generatePolicy(tt.oldStatus.policyName, tt.oldStatus.reschedulePolicy)
				oldClusters := generateFClusters(tt.oldStatus.clusterNames, tt.oldStatus.clusterLabels, tt.oldStatus.clusterTaints, tt.oldStatus.clusterApiResources)
				oldTrigger, err := computeSchedulingTrigger(ftc, fedObj, oldPolicy, oldClusters)
				if err != nil {
					t.Errorf("computeSchedulingTrigger() unexpected err: %v", err)
					return
				}
				oldTriggerText, err := oldTrigger.JsonMarshal()
				if err != nil {
					t.Errorf("JsonMarshal() unexpected err: %v", err)
					return
				}
				_, err = annotation.AddAnnotation(fedObj, SchedulingTriggersAnnotation, oldTriggerText)
				if err != nil {
					t.Errorf("AddAnnotation() unexpected err: %v", err)
					return
				}
			}
			if tt.withOldDeferredReasons {
				_, err := annotation.AddAnnotation(fedObj, SchedulingDeferredReasonsAnnotation, tt.oldDeferredReasons)
				if err != nil {
					t.Errorf("AddAnnotation() unexpected err: %v", err)
					return
				}
			}

			newPolicy := generatePolicy(tt.newStatus.policyName, tt.newStatus.reschedulePolicy)
			newClusters := generateFClusters(tt.newStatus.clusterNames, tt.newStatus.clusterLabels, tt.newStatus.clusterTaints, tt.newStatus.clusterApiResources)
			newTrigger, err := computeSchedulingTrigger(ftc, fedObj, newPolicy, newClusters)
			if err != nil {
				t.Errorf("computeSchedulingTrigger() unexpected err: %v", err)
				return
			}

			gotTriggersChanged, gotAnnotationChanged, err := newTrigger.updateAnnotationsIfTriggersChanged(fedObj, newPolicy)
			if (err != nil) != tt.wantErr {
				t.Errorf("updateAnnotationsIfTriggersChanged() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if gotTriggersChanged != tt.wantTriggersChanged {
				t.Errorf("updateAnnotationsIfTriggersChanged() gotTriggersChanged = %v, want = %v", gotTriggersChanged, tt.wantTriggersChanged)
			}
			if gotAnnotationChanged != tt.wantAnnotationChanged {
				t.Errorf("updateAnnotationsIfTriggersChanged() gotAnnotationChanged = %v, want = %v", gotAnnotationChanged, tt.wantAnnotationChanged)
			}
		})
	}
}

func generateFTCAndFedObj(withReplicas bool) (ftc *v1alpha1.FederatedTypeConfig, fedObj v1alpha1.GenericFederatedObject) {
	if !withReplicas {
		return saFTC.DeepCopy(), fsa.DeepCopy()
	}
	return dpFTC.DeepCopy(), fdp.DeepCopy()
}

func generatePolicy(name string, reschedulePolicy *v1alpha1.ReschedulePolicy) v1alpha1.GenericPropagationPolicy {
	return &v1alpha1.PropagationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.PropagationPolicySpec{ReschedulePolicy: reschedulePolicy},
	}
}

func generateFClusters(
	names []string,
	labels []map[string]string,
	taints [][]corev1.Taint,
	apiResources [][]v1alpha1.APIResource,
) []*v1alpha1.FederatedCluster {
	clusters := make([]*v1alpha1.FederatedCluster, 0, len(names))
	for i, name := range names {
		cluster := &v1alpha1.FederatedCluster{
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
