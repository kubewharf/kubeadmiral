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

package follower

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

func TestGetFollowersFromPod(t *testing.T) {
	// Pod template containing all possible followers that can be inferred.
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			ServiceAccountName: "Spec.ServiceAccountName",
			Containers: []corev1.Container{{
				EnvFrom: []corev1.EnvFromSource{{
					ConfigMapRef: &corev1.ConfigMapEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "Spec.Containers[*].EnvFrom[*].ConfigMapRef",
						},
					},
				}, {
					SecretRef: &corev1.SecretEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "Spec.Containers[*].EnvFrom[*].SecretRef",
						},
					},
				}},
				Env: []corev1.EnvVar{{
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "Spec.Containers[*].Env[*].ValueFrom.ConfigMapKeyRef",
							},
						},
					},
				}, {
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "Spec.Containers[*].Env[*].ValueFrom.SecretKeyRef",
							},
						},
					},
				}},
			}},
			EphemeralContainers: []corev1.EphemeralContainer{{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{
					EnvFrom: []corev1.EnvFromSource{{
						ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "Spec.EphemeralContainers[*].EphemeralContainerCommon.EnvFrom[*].ConfigMapRef",
							},
						},
					}, {
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "Spec.EphemeralContainers[*].EphemeralContainerCommon.EnvFrom[*].SecretRef",
							},
						},
					}},
					Env: []corev1.EnvVar{{
						ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "Spec.EphemeralContainers[*].EphemeralContainerCommon.Env[*].ValueFrom.ConfigMapKeyRef",
								},
							},
						},
					}, {
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "Spec.EphemeralContainers[*].EphemeralContainerCommon.Env[*].ValueFrom.SecretKeyRef",
								},
							},
						},
					}},
				},
			}},
			InitContainers: []corev1.Container{{
				EnvFrom: []corev1.EnvFromSource{{
					ConfigMapRef: &corev1.ConfigMapEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "Spec.InitContainers[*].EnvFrom[*].ConfigMapRef",
						},
					},
				}, {
					SecretRef: &corev1.SecretEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "Spec.InitContainers[*].EnvFrom[*].SecretRef",
						},
					},
				}},
				Env: []corev1.EnvVar{{
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "Spec.InitContainers[*].Env[*].ValueFrom.ConfigMapKeyRef",
							},
						},
					},
				}, {
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "Spec.InitContainers[*].Env[*].ValueFrom.SecretKeyRef",
							},
						},
					},
				}},
			}},
			ImagePullSecrets: []corev1.LocalObjectReference{{
				Name: "Spec.ImagePullSecrets",
			}},
			Volumes: []corev1.Volume{{
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{{
							ConfigMap: &corev1.ConfigMapProjection{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "Spec.Volumes[*].VolumeSource.Projected.Sources[*].ConfigMap",
								},
							},
						}},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.ConfigMap",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					AzureFile: &corev1.AzureFileVolumeSource{
						SecretName: "Spec.Volumes[*].VolumeSource.AzureFile.SecretName",
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					CephFS: &corev1.CephFSVolumeSource{
						SecretRef: &corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.CephFS.SecretRef",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					Cinder: &corev1.CinderVolumeSource{
						SecretRef: &corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.Cinder.SecretRef",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					FlexVolume: &corev1.FlexVolumeSource{
						SecretRef: &corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.FlexVolume.SecretRef",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{{
							Secret: &corev1.SecretProjection{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "Spec.Volumes[*].VolumeSource.Projected.Sources[*].Secret",
								},
							},
						}},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					RBD: &corev1.RBDVolumeSource{
						SecretRef: &corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.RBD.SecretRef",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "Spec.Volumes[*].VolumeSource.Secret.SecretName",
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "Spec.Volumes[*].VolumeSource.Secret",
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					ScaleIO: &corev1.ScaleIOVolumeSource{
						SecretRef: &corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.ScaleIO.SecretRef",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					ISCSI: &corev1.ISCSIVolumeSource{
						SecretRef: &corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.ISCSI.SecretRef",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					StorageOS: &corev1.StorageOSVolumeSource{
						SecretRef: &corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.StorageOS.SecretRef",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					CSI: &corev1.CSIVolumeSource{
						NodePublishSecretRef: &corev1.LocalObjectReference{
							Name: "Spec.Volumes[*].VolumeSource.CSI.NodePublishSecretRef",
						},
					},
				},
			}, {
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "Spec.Volumes[*].VolumeSource.PersistentVolumeClaim",
					},
				},
			}},
		},
	}

	expectedNamesByGK := map[schema.GroupKind]sets.Set[string]{
		{Group: "", Kind: "ConfigMap"}: sets.New(
			"Spec.Containers[*].EnvFrom[*].ConfigMapRef",
			"Spec.Containers[*].Env[*].ValueFrom.ConfigMapKeyRef",
			"Spec.EphemeralContainers[*].EphemeralContainerCommon.EnvFrom[*].ConfigMapRef",
			"Spec.EphemeralContainers[*].EphemeralContainerCommon.Env[*].ValueFrom.ConfigMapKeyRef",
			"Spec.InitContainers[*].EnvFrom[*].ConfigMapRef",
			"Spec.InitContainers[*].Env[*].ValueFrom.ConfigMapKeyRef",
			"Spec.Volumes[*].VolumeSource.Projected.Sources[*].ConfigMap",
			"Spec.Volumes[*].VolumeSource.ConfigMap",
		),
		{Group: "", Kind: "Secret"}: sets.New(
			"Spec.Containers[*].EnvFrom[*].SecretRef",
			"Spec.Containers[*].Env[*].ValueFrom.SecretKeyRef",
			"Spec.EphemeralContainers[*].EphemeralContainerCommon.EnvFrom[*].SecretRef",
			"Spec.EphemeralContainers[*].EphemeralContainerCommon.Env[*].ValueFrom.SecretKeyRef",
			"Spec.ImagePullSecrets",
			"Spec.InitContainers[*].EnvFrom[*].SecretRef",
			"Spec.InitContainers[*].Env[*].ValueFrom.SecretKeyRef",
			"Spec.Volumes[*].VolumeSource.AzureFile.SecretName",
			"Spec.Volumes[*].VolumeSource.CephFS.SecretRef",
			"Spec.Volumes[*].VolumeSource.Cinder.SecretRef",
			"Spec.Volumes[*].VolumeSource.FlexVolume.SecretRef",
			"Spec.Volumes[*].VolumeSource.Projected.Sources[*].Secret",
			"Spec.Volumes[*].VolumeSource.RBD.SecretRef",
			"Spec.Volumes[*].VolumeSource.Secret",
			"Spec.Volumes[*].VolumeSource.Secret.SecretName",
			"Spec.Volumes[*].VolumeSource.ScaleIO.SecretRef",
			"Spec.Volumes[*].VolumeSource.ISCSI.SecretRef",
			"Spec.Volumes[*].VolumeSource.StorageOS.SecretRef",
			"Spec.Volumes[*].VolumeSource.CSI.NodePublishSecretRef",
		),
		{Group: "", Kind: "PersistentVolumeClaim"}: sets.New(
			"Spec.Volumes[*].VolumeSource.PersistentVolumeClaim",
		),
		{Group: "", Kind: "ServiceAccount"}: sets.New(
			"Spec.ServiceAccountName",
		),
	}

	namespace := "default"

	expectedFollowers := sets.New[FollowerReference]()
	for gk, expectedNames := range expectedNamesByGK {
		for expectedName := range expectedNames {
			expectedFollowers.Insert(FollowerReference{
				GroupKind: gk,
				Namespace: namespace,
				Name:      expectedName,
			})
		}
	}

	followers := getFollowersFromPod("default", &pod)

	assert := assert.New(t)
	assert.Equal(expectedFollowers, followers)
}

func Test_skipSync(t *testing.T) {
	federatedObject := &fedcorev1a1.FederatedObject{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{common.DryRunAnnotation: common.AnnotationValueTrue},
		},
	}
	assert.True(t, skipSync(federatedObject))
	federatedObject.GetAnnotations()[common.DryRunAnnotation] = common.AnnotationValueFalse
	assert.False(t, skipSync(federatedObject))
	federatedObject.GetAnnotations()[common.DryRunAnnotation] = common.AnnotationValueTrue
	federatedObject.GetStatus().Clusters = []fedcorev1a1.PropagationStatus{
		{
			Cluster: "cluster1",
		},
	}
	assert.False(t, skipSync(federatedObject))
}
