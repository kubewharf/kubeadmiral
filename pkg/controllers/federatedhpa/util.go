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

package federatedhpa

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/follower"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
)

const (
	PropagationPolicyKind        = "PropagationPolicy"
	ClusterPropagationPolicyKind = "ClusterPropagationPolicy"

	FedHPANotWorkReason = common.DefaultPrefix + "fed-hpa-not-work-reason"

	EventReasonUpdateHPASourceObject = "UpdateHPASourceObject"
	EventReasonUpdateHPAFedObject    = "UpdateHPAFedObject"
)

type Resource struct {
	gvk       schema.GroupVersionKind
	namespace string
	name      string
}

func (r Resource) QualifiedName() common.QualifiedName {
	return common.QualifiedName{
		Namespace: r.namespace,
		Name:      r.name,
	}
}

func fedObjectToSourceObjectResource(object metav1.Object) (Resource, error) {
	fedObj, _ := object.(fedcorev1a1.GenericFederatedObject)
	metadata, err := fedObj.GetSpec().GetTemplateMetadata()
	if err != nil {
		return Resource{}, err
	}
	return Resource{
		name:      metadata.GetName(),
		namespace: metadata.GetNamespace(),
		gvk:       metadata.GroupVersionKind(),
	}, nil
}

func policyObjectToResource(object metav1.Object) Resource {
	if cpp, ok := object.(*fedcorev1a1.ClusterPropagationPolicy); ok {
		return Resource{
			name:      cpp.GetName(),
			namespace: cpp.GetNamespace(),
			gvk:       cpp.GroupVersionKind(),
		}
	}

	if pp, ok := object.(*fedcorev1a1.PropagationPolicy); ok {
		return Resource{
			name:      pp.GetName(),
			namespace: pp.GetNamespace(),
			gvk:       pp.GroupVersionKind(),
		}
	}
	return Resource{}
}

func generateFederationHPANotWorkReason(isPropagationPolicyExist, isPropagationPolicyDividedMode bool) string {
	var reasons []string
	if !isPropagationPolicyExist {
		reasons = append(reasons, "PropagationPolicy is not exist.")
	}
	if isPropagationPolicyExist && !isPropagationPolicyDividedMode {
		reasons = append(reasons, "PropagationPolicy is not divide.")
	}

	return fmt.Sprintf("%v", reasons)
}

func generateDistributedHPANotWorkReason(
	isPropagationPolicyExist,
	isPropagationPolicyDuplicateMode,
	isPropagationPolicyFollowerEnabled,
	isWorkloadRetainReplicas,
	isHPAFollowTheWorkload bool,
) string {
	var reasons []string
	if !isPropagationPolicyExist {
		reasons = append(reasons, "PropagationPolicy is not exist.")
	}
	if !isPropagationPolicyDuplicateMode {
		reasons = append(reasons, "PropagationPolicy is not Duplicate.")
	}
	if !isPropagationPolicyFollowerEnabled {
		reasons = append(reasons, "PropagationPolicy follower is not enable.")
	}
	if !isWorkloadRetainReplicas {
		reasons = append(reasons, "Workload is not retain replicas.")
	}
	if !isHPAFollowTheWorkload {
		reasons = append(reasons, "Hpa is not follow the workload.")
	}

	return fmt.Sprintf("%v", reasons)
}

func isHPAFTCAnnoChanged(lastObserved, latest *fedcorev1a1.FederatedTypeConfig) bool {
	return lastObserved.GetAnnotations()[common.HPAScaleTargetRefPath] != latest.GetAnnotations()[common.HPAScaleTargetRefPath]
}

func isPropagationPolicyExist(pp fedcorev1a1.GenericPropagationPolicy) bool {
	return pp != nil
}

func isPropagationPolicyDividedMode(pp fedcorev1a1.GenericPropagationPolicy) bool {
	return pp != nil && pp.GetSpec().SchedulingMode == fedcorev1a1.SchedulingModeDivide
}

func isPropagationPolicyDuplicateMode(pp fedcorev1a1.GenericPropagationPolicy) bool {
	return pp != nil && pp.GetSpec().SchedulingMode == fedcorev1a1.SchedulingModeDuplicate
}

func isPropagationPolicyFollowerEnabled(pp fedcorev1a1.GenericPropagationPolicy) bool {
	return !pp.GetSpec().DisableFollowerScheduling
}

func isHPAFollowTheWorkload(ctx context.Context, hpaUns *unstructured.Unstructured, fedWorkload fedcorev1a1.GenericFederatedObject) bool {
	logger := klog.FromContext(ctx)
	if hpaUns.GetAnnotations()[common.DisableFollowingAnnotation] == common.AnnotationValueTrue {
		return false
	}

	followersFromAnnotation, err := follower.GetFollowersFromAnnotation(fedWorkload)
	if err != nil {
		logger.Error(err, "Failed to get followers from annotation")
		return false
	}

	return followersFromAnnotation.Has(follower.FollowerReference{
		Name:      hpaUns.GetName(),
		Namespace: hpaUns.GetNamespace(),
		GroupKind: schema.GroupKind{
			Group: hpaUns.GroupVersionKind().Group,
			Kind:  hpaUns.GroupVersionKind().Kind,
		},
	})
}

func (f *FederatedHPAController) isHPAType(resourceGVK schema.GroupVersionKind) bool {
	ftc, exists := f.informerManager.GetResourceFTC(resourceGVK)
	if !exists {
		return false
	}

	// HPA gvk has already been stored
	if _, ok := f.scaleTargetRefMapping[resourceGVK]; ok {
		return true
	}

	if path, ok := ftc.Annotations[common.HPAScaleTargetRefPath]; ok {
		f.scaleTargetRefMapping[resourceGVK] = path
		return true
	} else {
		delete(f.scaleTargetRefMapping, resourceGVK)
		return false
	}
}

func isWorkloadRetainReplicas(fedObj metav1.Object) bool {
	return fedObj.GetAnnotations()[common.RetainReplicasAnnotation] == common.AnnotationValueTrue
}

func scaleTargetRefToResource(hpaUns *unstructured.Unstructured, scaleTargetRef string) (Resource, error) {
	fieldVal, found, err := unstructured.NestedFieldCopy(hpaUns.Object, strings.Split(scaleTargetRef, ".")...)
	if err != nil || !found {
		if err != nil {
			return Resource{}, errors.New(fmt.Sprintf("%s: %s", scaleTargetRef, err.Error()))
		} else {
			return Resource{}, errors.New(fmt.Sprintf("%s: not found", scaleTargetRef))
		}
	}
	fieldValMap := fieldVal.(map[string]interface{})

	// TODO: does it work for all types？
	var targetResource autoscalingv1.CrossVersionObjectReference
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(fieldValMap, &targetResource); err != nil {
		return Resource{}, err
	}

	gv, err := schema.ParseGroupVersion(targetResource.APIVersion)
	if err != nil {
		return Resource{}, err
	}

	return Resource{
		name:      targetResource.Name,
		gvk:       schema.GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: targetResource.Kind},
		namespace: hpaUns.GetNamespace(),
	}, nil
}

func getPropagationPolicyResourceFromFedWorkload(workload fedcorev1a1.GenericFederatedObject) *Resource {
	if policyName, exists := workload.GetLabels()[scheduler.PropagationPolicyNameLabel]; exists {
		return &Resource{
			gvk: schema.GroupVersionKind{
				Group:   fedcorev1a1.SchemeGroupVersion.Group,
				Version: fedcorev1a1.SchemeGroupVersion.Version,
				Kind:    PropagationPolicyKind,
			},
			name:      policyName,
			namespace: workload.GetNamespace(),
		}
	}

	if policyName, exists := workload.GetLabels()[scheduler.ClusterPropagationPolicyNameLabel]; exists {
		return &Resource{
			gvk: schema.GroupVersionKind{
				Group:   fedcorev1a1.SchemeGroupVersion.Group,
				Version: fedcorev1a1.SchemeGroupVersion.Version,
				Kind:    ClusterPropagationPolicyKind,
			},
			name: policyName,
		}
	}

	return nil
}

func addHPALabel(uns *unstructured.Unstructured, key, value string) bool {
	labels := uns.GetLabels()
	if labels == nil {
		labels = make(map[string]string, 1)
	}
	if oldValue, ok := labels[key]; ok && oldValue == value {
		return false
	}

	labels[key] = value
	uns.SetLabels(labels)

	return true
}

func removeHPALabel(uns *unstructured.Unstructured, key string) bool {
	labels := uns.GetLabels()
	if _, ok := labels[key]; !ok {
		return false
	}

	delete(labels, key)
	uns.SetLabels(labels)

	return true
}

func addFedHPAPendingController(
	ctx context.Context,
	fedObject fedcorev1a1.GenericFederatedObject,
	ftc *fedcorev1a1.FederatedTypeConfig,
) (bool, error) {
	logger := klog.FromContext(ctx)

	pendControllers, err := pendingcontrollers.GetPendingControllers(fedObject)
	if err != nil {
		logger.Error(err, "Failed to get pending controllers")
		return false, err
	}

	for _, controllers := range pendControllers {
		for _, controller := range controllers {
			if controller == PrefixedFederatedHPAControllerName {
				return false, nil
			}
		}
	}

	// TODO: By default, fed-hpa controller is the first controller.
	// 	Otherwise, this code needs to be modified.
	_, err = pendingcontrollers.SetPendingControllers(fedObject, ftc.GetControllers())
	if err != nil {
		logger.Error(err, "Failed to set pending controllers")
		return false, err
	}

	return true, nil
}

func removePendingController(
	ctx context.Context,
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
) (bool, error) {
	logger := klog.FromContext(ctx)

	updated, err := pendingcontrollers.UpdatePendingControllers(
		fedObject,
		PrefixedFederatedHPAControllerName,
		false,
		ftc.GetControllers(),
	)
	if err != nil {
		logger.Error(err, "Failed to update pending controllers")
		return false, err
	}

	return updated, nil
}

func addFedHPANotWorkReasonAnno(uns *unstructured.Unstructured, key string, value string) bool {
	annotations := uns.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	if oldValue, ok := annotations[key]; ok && oldValue == value {
		return false
	}

	annotations[key] = value
	uns.SetAnnotations(annotations)

	return true
}

func removeFedHPANotWorkReasonAnno(uns *unstructured.Unstructured, key string) bool {
	annotations := uns.GetAnnotations()
	if _, exists := annotations[key]; !exists {
		return false
	}

	delete(annotations, key)
	uns.SetAnnotations(annotations)

	return true
}
