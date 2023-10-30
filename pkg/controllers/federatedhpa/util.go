package federatedhpa

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	v2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/follower"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/pendingcontrollers"
	"github.com/kubewharf/kubeadmiral/pkg/util/worker"
)

const (
	PropagationPolicyKind        = "PropagationPolicy"
	ClusterPropagationPolicyKind = "ClusterPropagationPolicy"

	HPAScaleTargetRefPath = "hpa.kubeadmiral.io/scale-target-ref-path"

	HPAEnableKey = "fed-hpa-enabled"

	EventReasonUpdateHPASourceObject = "UpdateHPASourceObject"
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

func isHPAFTCAnnoChanged(lastObserved, latest *fedcorev1a1.FederatedTypeConfig) bool {
	return lastObserved.GetAnnotations()[HPAScaleTargetRefPath] != latest.GetAnnotations()[HPAScaleTargetRefPath]
}

func isPPExist(pp fedcorev1a1.GenericPropagationPolicy) bool {
	return pp != nil
}

func isPPDivided(pp fedcorev1a1.GenericPropagationPolicy) bool {
	return pp.GetSpec().SchedulingMode == fedcorev1a1.SchedulingModeDivide
}

func isPPFollowerEnabled(pp fedcorev1a1.GenericPropagationPolicy) bool {
	return !pp.GetSpec().DisableFollowerScheduling
}

func isHPAFollowTheWorkload(
	ctx context.Context,
	hpaUns *unstructured.Unstructured,
	fedWorkload fedcorev1a1.GenericFederatedObject,
) bool {
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

func (f *FederatedHPAController) isHPAType(fo metav1.Object) bool {
	if federatedObject, ok := fo.(*fedcorev1a1.FederatedObject); !ok {
		return false
	} else {
		metadata, _ := federatedObject.Spec.GetTemplateMetadata()
		ftc, exists := f.informerManager.GetResourceFTC(metadata.GroupVersionKind())
		if !exists {
			return false
		}

		// HPA gvk already been stored
		if _, ok := f.scaleTargetRefMapping[ftc.GetSourceTypeGVK()]; ok {
			return true
		}

		if path, ok := ftc.Annotations[HPAScaleTargetRefPath]; ok {
			f.scaleTargetRefMapping[ftc.GetSourceTypeGVK()] = path
			return true
		} else {
			delete(f.scaleTargetRefMapping, ftc.GetSourceTypeGVK())
			return false
		}
	}
}

func isWorkloadRetainReplicas(fedObj metav1.Object) bool {
	return fedObj.GetAnnotations()[common.RetainReplicasAnnotation] == common.AnnotationValueTrue
}

func scaleTargetRefToResource(
	hpaUns *unstructured.Unstructured,
	scaleTargetRef string,
) (Resource, error) {
	fieldVal, found, err := unstructured.NestedFieldCopy(hpaUns.Object, strings.Split(scaleTargetRef, ".")...)
	if err != nil || !found {
		if err != nil {
			return Resource{}, errors.New(fmt.Sprintf("%s: %s", scaleTargetRef, err.Error()))
		} else {
			return Resource{}, errors.New(fmt.Sprintf("%s: not found", scaleTargetRef))
		}
	}
	fieldValMap := fieldVal.(map[string]interface{})

	// todo: does it work for all types？
	var targetResource v2.CrossVersionObjectReference
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

func getPPResourceFromFedWorkload(workload fedcorev1a1.GenericFederatedObject) *Resource {
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

	return nil
}

func (f *FederatedHPAController) addFedHPALabel(
	ctx context.Context,
	uns *unstructured.Unstructured,
	gvr schema.GroupVersionResource,
	key, value string,
) worker.Result {
	logger := klog.FromContext(ctx)

	labels := uns.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	if oldValue, ok := labels[key]; ok && oldValue == value {
		return worker.StatusAllOK
	}

	labels[key] = value
	uns.SetLabels(labels)

	logger.V(1).Info("Adding fed hpa label of source object")
	_, err := f.dynamicClient.Resource(gvr).Namespace(uns.GetNamespace()).
		UpdateStatus(ctx, uns, metav1.UpdateOptions{})
	if err != nil {
		errMsg := "Failed to add fed hpa annotation"
		logger.Error(err, errMsg)
		f.eventRecorder.Eventf(uns, corev1.EventTypeWarning, EventReasonUpdateHPASourceObject,
			errMsg+" %v, err: %v, retry later", key, err)
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (f *FederatedHPAController) removeFedHPALabel(
	ctx context.Context,
	uns *unstructured.Unstructured,
	gvr schema.GroupVersionResource,
	key string,
) worker.Result {
	logger := klog.FromContext(ctx)

	labels := uns.GetLabels()
	if labels == nil {
		return worker.StatusAllOK
	}
	if _, ok := labels[key]; !ok {
		return worker.StatusAllOK
	}

	delete(labels, key)
	uns.SetLabels(labels)

	logger.V(1).Info("Removing fed hpa label of source object")
	_, err := f.dynamicClient.Resource(gvr).Namespace(uns.GetNamespace()).
		UpdateStatus(ctx, uns, metav1.UpdateOptions{})
	if err != nil {
		errMsg := "Failed to remove fed hpa label"
		logger.Error(err, errMsg)
		f.eventRecorder.Eventf(uns, corev1.EventTypeWarning, EventReasonUpdateHPASourceObject,
			errMsg+" %v, err: %v, retry later", key, err)
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (f *FederatedHPAController) addFedHPAPendingController(
	ctx context.Context,
	fedObject fedcorev1a1.GenericFederatedObject,
) worker.Result {
	logger := klog.FromContext(ctx)

	pendControllers, err := pendingcontrollers.GetPendingControllers(fedObject)
	if err != nil {
		logger.Error(err, "Failed to get pending controllers")
		return worker.StatusError
	}

	var hpaControllerExist bool
	for _, controllers := range pendControllers {
		for _, controller := range controllers {
			if controller == PrefixedFederatedHPAControllerName {
				hpaControllerExist = true
				break
			}
		}
	}

	if hpaControllerExist {
		return worker.StatusAllOK
	}

	if pendControllers == nil {
		pendControllers = [][]string{{PrefixedFederatedHPAControllerName}}
	} else {
		pendControllers = append(pendControllers, []string{PrefixedFederatedHPAControllerName})
	}

	_, err = pendingcontrollers.SetPendingControllers(fedObject, pendControllers)
	if err != nil {
		logger.Error(err, "Failed to set pending controllers")
		return worker.StatusError
	}

	logger.V(1).Info("Adding pending controller")
	if _, err = fedobjectadapters.Update(
		ctx,
		f.fedClient.CoreV1alpha1(),
		fedObject,
		metav1.UpdateOptions{},
	); err != nil {
		errMsg := "Failed to add pending controller"
		logger.Error(err, errMsg)
		f.eventRecorder.Eventf(fedObject, corev1.EventTypeWarning, EventReasonUpdateHPASourceObject,
			errMsg+" %v, err: %v, retry later", fedObject, err)
		if apierrors.IsConflict(err) {
			return worker.StatusConflict
		}
		return worker.StatusError
	}

	return worker.StatusAllOK
}

func (f *FederatedHPAController) removePendingController(
	ctx context.Context,
	ftc *fedcorev1a1.FederatedTypeConfig,
	fedObject fedcorev1a1.GenericFederatedObject,
) worker.Result {
	logger := klog.FromContext(ctx)

	updated, err := pendingcontrollers.UpdatePendingControllers(
		fedObject,
		PrefixedFederatedHPAControllerName,
		false,
		ftc.GetControllers(),
	)
	if err != nil {
		logger.Error(err, "Failed to update pending controllers")
		return worker.StatusError
	}

	if updated {
		logger.V(1).Info("Removing pending controller")
		if _, err = fedobjectadapters.Update(
			ctx,
			f.fedClient.CoreV1alpha1(),
			fedObject,
			metav1.UpdateOptions{},
		); err != nil {
			errMsg := "Failed to remove pending controller"
			logger.Error(err, errMsg)
			f.eventRecorder.Eventf(fedObject, corev1.EventTypeWarning, EventReasonUpdateHPASourceObject,
				errMsg+" %v, err: %v, retry later", fedObject, err)
			if apierrors.IsConflict(err) {
				return worker.StatusConflict
			}
			return worker.StatusError
		}
	}

	return worker.StatusAllOK
}
