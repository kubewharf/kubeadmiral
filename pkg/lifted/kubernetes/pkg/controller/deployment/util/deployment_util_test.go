/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

/*
This file is lifted from k8s.io/kubernetes/pkg/controller/deployment/util/deployment_util.go
*/

//nolint:all
package util

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func newDControllerRef(d *apps.Deployment) *metav1.OwnerReference {
	isController := true
	return &metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       d.GetName(),
		UID:        d.GetUID(),
		Controller: &isController,
	}
}

// generateRS creates a replica set, with the input deployment's template as its template
func generateRS(deployment apps.Deployment) apps.ReplicaSet {
	template := deployment.Spec.Template.DeepCopy()
	return apps.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			UID:             randomUID(),
			Name:            string(randomUID()),
			Labels:          template.Labels,
			OwnerReferences: []metav1.OwnerReference{*newDControllerRef(&deployment)},
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: new(int32),
			Template: *template,
			Selector: &metav1.LabelSelector{MatchLabels: template.Labels},
		},
	}
}

func randomUID() types.UID {
	return types.UID(strconv.FormatInt(rand.Int63(), 10))
}

// generateDeployment creates a deployment, with the input image as its template
func generateDeployment(image string) apps.Deployment {
	podLabels := map[string]string{"name": image}
	terminationSec := int64(30)
	enableServiceLinks := v1.DefaultEnableServiceLinks
	return apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        image,
			Annotations: make(map[string]string),
		},
		Spec: apps.DeploymentSpec{
			Replicas: func(i int32) *int32 { return &i }(1),
			Selector: &metav1.LabelSelector{MatchLabels: podLabels},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:                   image,
							Image:                  image,
							ImagePullPolicy:        v1.PullAlways,
							TerminationMessagePath: v1.TerminationMessagePathDefault,
						},
					},
					DNSPolicy:                     v1.DNSClusterFirst,
					TerminationGracePeriodSeconds: &terminationSec,
					RestartPolicy:                 v1.RestartPolicyAlways,
					SecurityContext:               &v1.PodSecurityContext{},
					EnableServiceLinks:            &enableServiceLinks,
				},
			},
		},
	}
}

func generatePodTemplateSpec(name, nodeName string, annotations, labels map[string]string) v1.PodTemplateSpec {
	return v1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
	}
}

func TestEqualIgnoreHash(t *testing.T) {
	tests := []struct {
		Name           string
		former, latter v1.PodTemplateSpec
		expected       bool
	}{
		{
			"Same spec, same labels",
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1", "something": "else"}),
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1", "something": "else"}),
			true,
		},
		{
			"Same spec, only pod-template-hash label value is different",
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1", "something": "else"}),
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-2", "something": "else"}),
			true,
		},
		{
			"Same spec, the former doesn't have pod-template-hash label",
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{"something": "else"}),
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-2", "something": "else"}),
			true,
		},
		{
			"Same spec, the label is different, the former doesn't have pod-template-hash label, same number of labels",
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{"something": "else"}),
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-2"}),
			false,
		},
		{
			"Same spec, the label is different, the latter doesn't have pod-template-hash label, same number of labels",
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1"}),
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{"something": "else"}),
			false,
		},
		{
			"Same spec, the label is different, and the pod-template-hash label value is the same",
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1"}),
			generatePodTemplateSpec("foo", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1", "something": "else"}),
			false,
		},
		{
			"Different spec, same labels",
			generatePodTemplateSpec("foo", "foo-node", map[string]string{"former": "value"}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1", "something": "else"}),
			generatePodTemplateSpec("foo", "foo-node", map[string]string{"latter": "value"}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1", "something": "else"}),
			false,
		},
		{
			"Different spec, different pod-template-hash label value",
			generatePodTemplateSpec("foo-1", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-1", "something": "else"}),
			generatePodTemplateSpec("foo-2", "foo-node", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-2", "something": "else"}),
			false,
		},
		{
			"Different spec, the former doesn't have pod-template-hash label",
			generatePodTemplateSpec("foo-1", "foo-node-1", map[string]string{}, map[string]string{"something": "else"}),
			generatePodTemplateSpec("foo-2", "foo-node-2", map[string]string{}, map[string]string{apps.DefaultDeploymentUniqueLabelKey: "value-2", "something": "else"}),
			false,
		},
		{
			"Different spec, different labels",
			generatePodTemplateSpec("foo", "foo-node-1", map[string]string{}, map[string]string{"something": "else"}),
			generatePodTemplateSpec("foo", "foo-node-2", map[string]string{}, map[string]string{"nothing": "else"}),
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			runTest := func(t1, t2 *v1.PodTemplateSpec, reversed bool) {
				reverseString := ""
				if reversed {
					reverseString = " (reverse order)"
				}
				// Run
				equal := EqualIgnoreHash(t1, t2)
				if equal != test.expected {
					t.Errorf("%q%s: expected %v", test.Name, reverseString, test.expected)
					return
				}
				if t1.Labels == nil || t2.Labels == nil {
					t.Errorf("%q%s: unexpected labels becomes nil", test.Name, reverseString)
				}
			}

			runTest(&test.former, &test.latter, false)
			// Test the same case in reverse order
			runTest(&test.latter, &test.former, true)
		})
	}
}

func TestFindNewReplicaSet(t *testing.T) {
	now := metav1.Now()
	later := metav1.Time{Time: now.Add(time.Minute)}

	deployment := generateDeployment("nginx")
	newRS := generateRS(deployment)
	newRS.Labels[apps.DefaultDeploymentUniqueLabelKey] = "hash"
	newRS.CreationTimestamp = later

	newRSDup := generateRS(deployment)
	newRSDup.Labels[apps.DefaultDeploymentUniqueLabelKey] = "different-hash"
	newRSDup.CreationTimestamp = now

	oldDeployment := generateDeployment("nginx")
	oldDeployment.Spec.Template.Spec.Containers[0].Name = "nginx-old-1"
	oldRS := generateRS(oldDeployment)
	oldRS.Status.FullyLabeledReplicas = *(oldRS.Spec.Replicas)

	tests := []struct {
		Name       string
		deployment apps.Deployment
		rsList     []*apps.ReplicaSet
		expected   *apps.ReplicaSet
	}{
		{
			Name:       "Get new ReplicaSet with the same template as Deployment spec but different pod-template-hash value",
			deployment: deployment,
			rsList:     []*apps.ReplicaSet{&newRS, &oldRS},
			expected:   &newRS,
		},
		{
			Name:       "Get the oldest new ReplicaSet when there are more than one ReplicaSet with the same template",
			deployment: deployment,
			rsList:     []*apps.ReplicaSet{&newRS, &oldRS, &newRSDup},
			expected:   &newRSDup,
		},
		{
			Name:       "Get nil new ReplicaSet",
			deployment: deployment,
			rsList:     []*apps.ReplicaSet{&oldRS},
			expected:   nil,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			if rs := FindNewReplicaSet(&test.deployment, test.rsList); !reflect.DeepEqual(rs, test.expected) {
				t.Errorf("In test case %q, expected %#v, got %#v", test.Name, test.expected, rs)
			}
		})
	}
}



func newString(s string) *string {
	return &s
}
