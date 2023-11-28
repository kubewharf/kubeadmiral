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
	"fmt"
	"reflect"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler/framework"
)

func TestMatchedPolicyKey(t *testing.T) {
	testCases := map[string]struct {
		objectNamespace string
		ppLabelValue    *string
		cppLabelValue   *string

		expectedPolicyFound     bool
		expectedPolicyName      string
		expectedPolicyNamespace string
	}{
		"namespaced object does not reference any policy": {
			objectNamespace:     "default",
			expectedPolicyFound: false,
		},
		"cluster-scoped object does not reference any policy": {
			expectedPolicyFound: false,
		},
		"namespaced object references pp in same namespace": {
			objectNamespace:         "default",
			ppLabelValue:            pointer.String("pp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "pp1",
			expectedPolicyNamespace: "default",
		},
		"namespaced object references cpp": {
			objectNamespace:         "default",
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
		"namespaced object references both pp and cpp": {
			objectNamespace:         "default",
			ppLabelValue:            pointer.String("pp1"),
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "pp1",
			expectedPolicyNamespace: "default",
		},
		"cluster-scoped object references pp": {
			ppLabelValue:        pointer.String("pp1"),
			expectedPolicyFound: false,
		},
		"cluster-scoped object references cpp": {
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
		"cluster-scoped object references both pp and cpp": {
			ppLabelValue:            pointer.String("pp1"),
			cppLabelValue:           pointer.String("cpp1"),
			expectedPolicyFound:     true,
			expectedPolicyName:      "cpp1",
			expectedPolicyNamespace: "",
		},
	}

	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: make(map[string]interface{})}
			object.SetNamespace(testCase.objectNamespace)
			labels := map[string]string{}
			if testCase.ppLabelValue != nil {
				labels[PropagationPolicyNameLabel] = *testCase.ppLabelValue
			}
			if testCase.cppLabelValue != nil {
				labels[ClusterPropagationPolicyNameLabel] = *testCase.cppLabelValue
			}
			object.SetLabels(labels)

			policy, found := GetMatchedPolicyKey(object)
			if found != testCase.expectedPolicyFound {
				t.Fatalf("found = %v, but expectedPolicyFound = %v", found, testCase.expectedPolicyFound)
			}

			if !found {
				return
			}

			if policy.Name != testCase.expectedPolicyName {
				t.Fatalf("policyName = %s, but expectedPolicyName = %s", policy.Name, testCase.expectedPolicyName)
			}
			if policy.Namespace != testCase.expectedPolicyNamespace {
				t.Fatalf("policyNamespace = %v, but expectedPolicyNamespace = %v", policy.Namespace, testCase.expectedPolicyNamespace)
			}
		})
	}
}

func generateFedObj(workload *unstructured.Unstructured) *fedcorev1a1.FederatedObject {
	rawTargetTemplate, _ := workload.MarshalJSON()
	return &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Generation: 1,
		},
		Spec: fedcorev1a1.GenericFederatedObjectSpec{
			Template: apiextensionsv1.JSON{Raw: rawTargetTemplate},
		},
	}
}

func Test_validateMigrationConfig(t *testing.T) {
	sourceObject := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"spec": map[string]interface{}{
				"replicas": int64(3),
			},
		},
	}
	ftc := &fedcorev1a1.FederatedTypeConfig{
		Spec: fedcorev1a1.FederatedTypeConfigSpec{
			PathDefinition: fedcorev1a1.PathDefinition{
				ReplicasSpec: "spec.replicas",
			},
		},
	}

	tests := []struct {
		name            string
		sourceObj       *unstructured.Unstructured
		migrationConfig *framework.MigrationConfig
		wantErr         error
	}{
		{
			name:      "migration config is empty",
			sourceObj: sourceObject,
			migrationConfig: &framework.MigrationConfig{
				ReplicasMigrations: []framework.ReplicasMigration{},
				WorkloadMigrations: []framework.WorkloadMigration{},
			},
			wantErr: nil,
		},
		{
			name:      "migration config is correct",
			sourceObj: sourceObject,
			migrationConfig: &framework.MigrationConfig{
				ReplicasMigrations: []framework.ReplicasMigration{
					{
						Cluster:         "cluster1",
						LimitedCapacity: pointer.Int64(1),
					},
				},
				WorkloadMigrations: []framework.WorkloadMigration{
					{
						Cluster:    "cluster2",
						ValidUntil: &metav1.Time{Time: time.Now()},
					},
				},
			},
			wantErr: nil,
		},
		{
			name:      "cluster is empty",
			sourceObj: sourceObject,
			migrationConfig: &framework.MigrationConfig{
				ReplicasMigrations: []framework.ReplicasMigration{
					{
						Cluster: "",
					},
				},
			},
			wantErr: fmt.Errorf("cluster cannot be empty"),
		},
		{
			name:      "limitedCapacity is empty",
			sourceObj: sourceObject,
			migrationConfig: &framework.MigrationConfig{
				ReplicasMigrations: []framework.ReplicasMigration{
					{
						Cluster: "cluster1",
					},
				},
			},
			wantErr: fmt.Errorf("limitedCapacity cannot be empty"),
		},
		{
			name:      "limitedCapacity is less than zero",
			sourceObj: sourceObject,
			migrationConfig: &framework.MigrationConfig{
				ReplicasMigrations: []framework.ReplicasMigration{
					{
						Cluster:         "cluster1",
						LimitedCapacity: pointer.Int64(-1),
					},
				},
			},
			wantErr: fmt.Errorf("limitedCapacity cannot be less than zero"),
		},
		{
			name:      "duplicate cluster in workloadMigration and replicasMigration",
			sourceObj: sourceObject,
			migrationConfig: &framework.MigrationConfig{
				WorkloadMigrations: []framework.WorkloadMigration{
					{
						Cluster:    "cluster1",
						ValidUntil: &metav1.Time{Time: time.Now()},
					},
				},
				ReplicasMigrations: []framework.ReplicasMigration{
					{
						Cluster:         "cluster1",
						LimitedCapacity: pointer.Int64(1),
					},
				},
			},
			wantErr: fmt.Errorf("multiple migration configurations cannot be configured for the same cluster"),
		},
		{
			name:      "duplicate cluster in workloadMigration",
			sourceObj: sourceObject,
			migrationConfig: &framework.MigrationConfig{
				WorkloadMigrations: []framework.WorkloadMigration{
					{
						Cluster:    "cluster1",
						ValidUntil: &metav1.Time{Time: time.Now()},
					},
					{
						Cluster:    "cluster1",
						ValidUntil: &metav1.Time{Time: time.Now()},
					},
				},
			},
			wantErr: fmt.Errorf("multiple migration configurations cannot be configured for the same cluster"),
		},
		{
			name:      "duplicate cluster in replicasMigration",
			sourceObj: sourceObject,
			migrationConfig: &framework.MigrationConfig{
				ReplicasMigrations: []framework.ReplicasMigration{
					{
						Cluster:         "cluster1",
						LimitedCapacity: pointer.Int64(1),
					},
					{
						Cluster:         "cluster1",
						LimitedCapacity: pointer.Int64(1),
					},
				},
			},
			wantErr: fmt.Errorf("multiple migration configurations cannot be configured for the same cluster"),
		},
		{
			name: "non-replica resource uses replicasMigration",
			sourceObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"spec":       map[string]interface{}{},
				},
			},
			migrationConfig: &framework.MigrationConfig{
				ReplicasMigrations: []framework.ReplicasMigration{
					{
						Cluster:         "cluster1",
						LimitedCapacity: pointer.Int64(2),
					},
				},
			},
			wantErr: fmt.Errorf("workload does not support the replica migration configurations"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateMigrationConfig(test.migrationConfig, generateFedObj(test.sourceObj), ftc)
			if !reflect.DeepEqual(err, test.wantErr) {
				t.Errorf("validateMigrationConfig(): want %v, got %v", test.wantErr, err)
			}
		})
	}
}

func Test_convertToCustomMigrationInfo(t *testing.T) {
	timeNow := time.Now()
	type args struct {
		migrationConfig *framework.MigrationConfig
	}
	tests := []struct {
		name string
		args args
		want *framework.CustomMigrationInfo
	}{
		{
			name: "replicasMigrations and workloadMigration both exist",
			args: args{
				migrationConfig: &framework.MigrationConfig{
					ReplicasMigrations: []framework.ReplicasMigration{
						{
							Cluster:         "cluster1",
							LimitedCapacity: pointer.Int64(1),
						},
						{
							Cluster:         "cluster2",
							LimitedCapacity: pointer.Int64(2),
						},
					},
					WorkloadMigrations: []framework.WorkloadMigration{
						{
							Cluster:    "cluster3",
							ValidUntil: &metav1.Time{Time: timeNow},
						},
					},
				},
			},
			want: &framework.CustomMigrationInfo{
				LimitedCapacity: map[string]int64{
					"cluster1": 1,
					"cluster2": 2,
				},
				UnavailableClusters: []framework.UnavailableCluster{
					{
						Cluster:    "cluster3",
						ValidUntil: metav1.NewTime(timeNow),
					},
				},
			},
		},
		{
			name: "items in workloadMigrations are the same but not ordered",
			args: args{
				migrationConfig: &framework.MigrationConfig{
					WorkloadMigrations: []framework.WorkloadMigration{
						{
							Cluster:    "cluster2",
							ValidUntil: &metav1.Time{Time: timeNow},
						},
						{
							Cluster:    "cluster1",
							ValidUntil: &metav1.Time{Time: timeNow},
						},
					},
				},
			},
			want: &framework.CustomMigrationInfo{
				UnavailableClusters: []framework.UnavailableCluster{
					{
						Cluster:    "cluster1",
						ValidUntil: metav1.NewTime(timeNow),
					},
					{
						Cluster:    "cluster2",
						ValidUntil: metav1.NewTime(timeNow),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotCustomMigrationInfo := convertToCustomMigrationInfo(tt.args.migrationConfig); !reflect.DeepEqual(gotCustomMigrationInfo, tt.want) {
				t.Errorf("convertToCustomMigrationInfo() = %v, want %v", gotCustomMigrationInfo, tt.want)
			}
		})
	}
}

func Test_getCustomMigrationInfoBytes(t *testing.T) {
	tests := []struct {
		name                    string
		sourceObj               *unstructured.Unstructured
		ftc                     *fedcorev1a1.FederatedTypeConfig
		migrationConfigBytes    string
		wantCustomMigrationInfo string
	}{
		{
			name: "get internal annotation",
			sourceObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"spec": map[string]interface{}{
						"replicas": int64(3),
					},
				},
			},
			ftc: &fedcorev1a1.FederatedTypeConfig{
				Spec: fedcorev1a1.FederatedTypeConfigSpec{
					PathDefinition: fedcorev1a1.PathDefinition{
						ReplicasSpec: "spec.replicas",
					},
				},
			},
			migrationConfigBytes: "{\"replicasMigrations\":[{\"cluster\":\"cluster1\",\"limitedCapacity\":1}," +
				"{\"cluster\":\"cluster2\",\"limitedCapacity\":2}],\"workloadMigrations\":[{\"cluster\":" +
				"\"cluster3\",\"validUntil\":\"2023-11-13T06:32:46Z\"}]}",
			wantCustomMigrationInfo: "{\"limitedCapacity\":{\"cluster1\":1,\"cluster2\":2},\"unavailableClusters\":" +
				"[{\"cluster\":\"cluster3\",\"validUntil\":\"2023-11-13T06:32:46Z\"}]}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if customMigrationInfo, _ := getCustomMigrationInfoBytes(tt.migrationConfigBytes,
				generateFedObj(tt.sourceObj), tt.ftc); !reflect.DeepEqual(customMigrationInfo, tt.wantCustomMigrationInfo) {
				t.Errorf("getCustomMigrationInfoBytes() = (%v), want (%v)",
					customMigrationInfo, tt.wantCustomMigrationInfo)
			}
		})
	}
}
