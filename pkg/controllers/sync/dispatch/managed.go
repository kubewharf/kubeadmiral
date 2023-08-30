/*
Copyright 2019 The Kubernetes Authors.

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

package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/propagatedversion"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/status"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/stats/metrics"
	"github.com/kubewharf/kubeadmiral/pkg/util/adoption"
	"github.com/kubewharf/kubeadmiral/pkg/util/managedlabel"
)

const (
	IndexRolloutPlans    = "federation_placement_rollout"
	clusterOperationOk   = "ok"
	clusterOperationFail = "fail"
)

// FederatedResourceForDispatch is the subset of the FederatedResource
// interface required for dispatching operations to managed resources.
type FederatedResourceForDispatch interface {
	// TargetName returns the name of the resource's target object.
	TargetName() common.QualifiedName
	// TargetGVK returns the resource's target group/version/kind.
	TargetGVK() schema.GroupVersionKind
	// TargetGVR returns the resource's target group/version/resource.
	TargetGVR() schema.GroupVersionResource
	// TypeConfig returns the FederatedTypeConfig for the resource's target type.
	TypeConfig() *fedcorev1a1.FederatedTypeConfig
	// Object returns the underlying FederatedObject or ClusterFederatedObject
	// as a GenericFederatedObject.
	Object() fedcorev1a1.GenericFederatedObject
	// VersionForCluster returns the resource's last propagated version for the given cluster.
	VersionForCluster(clusterName string) (string, error)
	// ObjectForCluster returns the resource's desired object for the given cluster.
	ObjectForCluster(clusterName string) (*unstructured.Unstructured, error)
	// ApplyOverrides applies cluster-specific overrides to the given object.
	ApplyOverrides(obj *unstructured.Unstructured, clusterName string) error
	// RecordError records an error for the resource.
	RecordError(errorCode string, err error)
	// RecordEvent records an event for the resource.
	RecordEvent(reason, messageFmt string, args ...interface{})
}

// ManagedDispatcher dispatches operations to member clusters for resources
// managed by a federated resource.
type ManagedDispatcher interface {
	UnmanagedDispatcher

	Create(ctx context.Context, clusterName string)
	Update(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured)
	VersionMap() map[string]string
	CollectedStatus() status.CollectedPropagationStatus
	RecordClusterError(propStatus fedcorev1a1.PropagationStatusType, clusterName string, err error)
	RecordStatus(clusterName string, propStatus fedcorev1a1.PropagationStatusType)
}

type managedDispatcherImpl struct {
	sync.RWMutex

	dispatcher            *operationDispatcherImpl
	unmanagedDispatcher   *unmanagedDispatcherImpl
	fedResource           FederatedResourceForDispatch
	versionMap            map[string]string
	statusMap             status.PropagationStatusMap
	skipAdoptingResources bool

	// Track when resource updates are performed to allow indicating
	// when a change was last propagated to member clusters.
	resourcesUpdated bool

	metrics stats.Metrics
}

func NewManagedDispatcher(
	clientAccessor clientAccessorFunc,
	fedResource FederatedResourceForDispatch,
	skipAdoptingResources bool,
	metrics stats.Metrics,
) ManagedDispatcher {
	d := &managedDispatcherImpl{
		fedResource:           fedResource,
		versionMap:            make(map[string]string),
		statusMap:             make(status.PropagationStatusMap),
		skipAdoptingResources: skipAdoptingResources,
		metrics:               metrics,
	}
	d.dispatcher = newOperationDispatcher(clientAccessor, d)
	d.unmanagedDispatcher = newUnmanagedDispatcher(d.dispatcher, d, fedResource.TargetGVR(), fedResource.TargetName(), metrics)
	return d
}

func (d *managedDispatcherImpl) Wait() (bool, error) {
	ok, err := d.dispatcher.Wait()
	if err != nil {
		return ok, err
	}

	// Transition the status of clusters that still have a default
	// timed out status.
	d.RLock()
	defer d.RUnlock()
	// Transition timed out status for this set to ok.
	okTimedOut := sets.NewString(
		string(fedcorev1a1.CreationTimedOut),
		string(fedcorev1a1.UpdateTimedOut),
	)
	for key, value := range d.statusMap {
		propStatus := string(value)
		if okTimedOut.Has(propStatus) {
			d.statusMap[key] = fedcorev1a1.ClusterPropagationOK
		} else if propStatus == string(fedcorev1a1.DeletionTimedOut) {
			// If deletion was successful, then assume the resource is
			// pending garbage collection.
			d.statusMap[key] = fedcorev1a1.WaitingForRemoval
		} else if propStatus == string(fedcorev1a1.LabelRemovalTimedOut) {
			// If label removal was successful, the resource is
			// effectively unmanaged for the cluster even though it
			// still may be cached.
			delete(d.statusMap, key)
		}
	}
	return ok, nil
}

func (d *managedDispatcherImpl) Create(ctx context.Context, clusterName string) {
	// Default the status to an operation-specific timeout.  Otherwise
	// when a timeout occurs it won't be possible to determine which
	// operation timed out.  The timeout status will be cleared by
	// Wait() if a timeout does not occur.
	d.RecordStatus(clusterName, fedcorev1a1.CreationTimedOut)

	d.dispatcher.incrementOperationsInitiated()
	const op = "create"
	go d.dispatcher.clusterOperation(ctx, clusterName, op, func(client dynamic.Interface) bool {
		startTime := time.Now()
		result := true
		defer func() {
			metricResult := clusterOperationOk
			if !result {
				metricResult = clusterOperationFail
			}
			d.metrics.Duration(metrics.DispatchOperationDuration, startTime,
				stats.Tag{Name: "namespace", Value: d.fedResource.TargetName().Namespace},
				stats.Tag{Name: "name", Value: d.fedResource.TargetName().Name},
				stats.Tag{Name: "group", Value: d.fedResource.TargetGVK().Group},
				stats.Tag{Name: "version", Value: d.fedResource.TargetGVK().Version},
				stats.Tag{Name: "kind", Value: d.fedResource.TargetGVK().Kind},
				stats.Tag{Name: "operation", Value: op},
				stats.Tag{Name: "cluster", Value: clusterName},
				stats.Tag{Name: "result", Value: metricResult},
			)
		}()
		keyedLogger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)
		d.recordEvent(clusterName, op, "Creating")

		obj, err := d.fedResource.ObjectForCluster(clusterName)
		if err != nil {
			result = d.recordOperationError(ctx, fedcorev1a1.ComputeResourceFailed, clusterName, op, err)
			return result
		}

		err = d.fedResource.ApplyOverrides(obj, clusterName)
		if err != nil {
			result = d.recordOperationError(ctx, fedcorev1a1.ApplyOverridesFailed, clusterName, op, err)
			return result
		}

		recordPropagatedLabelsAndAnnotations(obj)

		ctxWithTimeout, cancel := context.WithTimeout(ctx, d.dispatcher.timeout)
		defer cancel()

		keyedLogger.V(1).Info("Creating target object in cluster")
		createdObj, createErr := client.Resource(d.fedResource.TargetGVR()).Namespace(obj.GetNamespace()).Create(
			ctxWithTimeout, obj, metav1.CreateOptions{},
		)
		if createErr == nil {
			version := propagatedversion.ObjectVersion(createdObj)
			d.recordVersion(clusterName, version)
			return result
		}

		// If the object exists, we may attempt to adopt the resource. We check if the object exists by examining the
		// returned error.
		var needsExistenceRecheck bool
		switch {
		case apierrors.IsAlreadyExists(createErr):
			needsExistenceRecheck = false
		case d.fedResource.TargetGVK() == corev1.SchemeGroupVersion.WithKind(common.NamespaceKind) && apierrors.IsServerTimeout(createErr):
			// For namespaces, ServerTimeout is returned if object exists. We differentiate with the subsequent GET.
			needsExistenceRecheck = true
		case d.fedResource.TargetGVK() == corev1.SchemeGroupVersion.WithKind(common.ServiceKind) && apierrors.IsInvalid(createErr):
			// For services, Invalid is returned if the object exists. We differentiate with the subsequent GET.
			needsExistenceRecheck = true
		default:
			return d.recordOperationError(ctxWithTimeout, fedcorev1a1.CreationFailed, clusterName, op, createErr)
		}

		// Attempt to update the existing resource to ensure that it
		// is labeled as a managed resource.
		obj, getErr := client.Resource(d.fedResource.TargetGVR()).Namespace(obj.GetNamespace()).Get(
			ctxWithTimeout, obj.GetName(), metav1.GetOptions{},
		)
		if needsExistenceRecheck && apierrors.IsNotFound(getErr) {
			// The previous creation error should not be an AlreadyExists error, skip trying to adopt.
			// Note: there is a chance that the previous error is still an AlreadyExists error and the object was
			// simply deleted in between the 2 requests.
			return d.recordOperationError(ctxWithTimeout, fedcorev1a1.CreationFailed, clusterName, op, createErr)
		} else if getErr != nil {
			wrappedErr := errors.Wrapf(getErr, "failed to retrieve object potentially requiring adoption")
			return d.recordOperationError(ctxWithTimeout, fedcorev1a1.RetrievalFailed, clusterName, op, wrappedErr)
		}

		if d.skipAdoptingResources {
			result = d.recordOperationError(
				ctxWithTimeout,
				fedcorev1a1.AlreadyExists,
				clusterName,
				op,
				errors.Errorf("Resource pre-exist in cluster"),
			)
			return result
		}

		d.recordError(
			ctxWithTimeout,
			clusterName,
			op,
			errors.Errorf("An update will be attempted instead of a creation due to an existing resource"),
		)
		if !managedlabel.HasManagedLabel(obj) {
			// If the object was not managed by us, mark it as adopted.
			adoption.AddAdoptedAnnotation(obj)
		}
		d.Update(ctx, clusterName, obj)
		return result
	})
}

func (d *managedDispatcherImpl) Update(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured) {
	d.RecordStatus(clusterName, fedcorev1a1.UpdateTimedOut)

	d.dispatcher.incrementOperationsInitiated()
	const op = "update"
	go d.dispatcher.clusterOperation(ctx, clusterName, op, func(client dynamic.Interface) bool {
		startTime := time.Now()
		result := true
		defer func() {
			metricResult := clusterOperationOk
			if !result {
				metricResult = clusterOperationFail
			}
			d.metrics.Duration(metrics.DispatchOperationDuration, startTime,
				stats.Tag{Name: "namespace", Value: d.fedResource.TargetName().Namespace},
				stats.Tag{Name: "name", Value: d.fedResource.TargetName().Name},
				stats.Tag{Name: "group", Value: d.fedResource.TargetGVK().Group},
				stats.Tag{Name: "version", Value: d.fedResource.TargetGVK().Version},
				stats.Tag{Name: "kind", Value: d.fedResource.TargetGVK().Kind},
				stats.Tag{Name: "operation", Value: op},
				stats.Tag{Name: "cluster", Value: clusterName},
				stats.Tag{Name: "result", Value: metricResult},
			)
		}()
		keyedLogger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)
		if managedlabel.IsExplicitlyUnmanaged(clusterObj) {
			err := errors.Errorf(
				"Unable to manage the object which has label %s: %s",
				managedlabel.ManagedByKubeAdmiralLabelKey,
				managedlabel.UnmanagedByKubeAdmiralLabelValue,
			)
			result = d.recordOperationError(ctx, fedcorev1a1.ManagedLabelFalse, clusterName, op, err)
			return result
		}

		obj, err := d.fedResource.ObjectForCluster(clusterName)
		if err != nil {
			result = d.recordOperationError(ctx, fedcorev1a1.ComputeResourceFailed, clusterName, op, err)
			return result
		}

		err = d.fedResource.ApplyOverrides(obj, clusterName)
		if err != nil {
			result = d.recordOperationError(ctx, fedcorev1a1.ApplyOverridesFailed, clusterName, op, err)
			return result
		}

		recordPropagatedLabelsAndAnnotations(obj)

		err = RetainOrMergeClusterFields(d.fedResource.TargetGVK(), obj, clusterObj)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retain fields")
			result = d.recordOperationError(ctx, fedcorev1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
			return result
		}

		err = retainReplicas(obj, clusterObj, d.fedResource.Object(), d.fedResource.TypeConfig().Spec.PathDefinition.ReplicasSpec)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retain replicas")
			result = d.recordOperationError(ctx, fedcorev1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
			return result
		}

		version, err := d.fedResource.VersionForCluster(clusterName)
		if err != nil {
			result = d.recordOperationError(ctx, fedcorev1a1.VersionRetrievalFailed, clusterName, op, err)
			return result
		}

		if !propagatedversion.ObjectNeedsUpdate(obj, clusterObj, version, d.fedResource.TypeConfig()) {
			// Resource is current, we still record version in dispatcher
			// so that federated status can be set with cluster resource generation
			d.recordVersion(clusterName, version)
			return result
		}

		// Only record an event if the resource is not current
		d.recordEvent(clusterName, op, "Updating")

		keyedLogger.V(1).Info("Updating target object in cluster")
		obj, err = client.Resource(d.fedResource.TargetGVR()).Namespace(obj.GetNamespace()).Update(
			ctx, obj, metav1.UpdateOptions{},
		)
		if err != nil {
			result = d.recordOperationError(ctx, fedcorev1a1.UpdateFailed, clusterName, op, err)
			return result
		}
		d.setResourcesUpdated()
		version = propagatedversion.ObjectVersion(obj)
		d.recordVersion(clusterName, version)
		return result
	})
}

func (d *managedDispatcherImpl) Delete(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured) {
	d.RecordStatus(clusterName, fedcorev1a1.DeletionTimedOut)

	d.unmanagedDispatcher.Delete(ctx, clusterName, clusterObj)
}

func (d *managedDispatcherImpl) RemoveManagedLabel(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured) {
	d.RecordStatus(clusterName, fedcorev1a1.LabelRemovalTimedOut)

	d.unmanagedDispatcher.RemoveManagedLabel(ctx, clusterName, clusterObj)
}

func (d *managedDispatcherImpl) RecordClusterError(
	propStatus fedcorev1a1.PropagationStatusType,
	clusterName string,
	err error,
) {
	d.fedResource.RecordError(string(propStatus), err)
	d.RecordStatus(clusterName, propStatus)
}

func (d *managedDispatcherImpl) RecordStatus(clusterName string, propStatus fedcorev1a1.PropagationStatusType) {
	d.Lock()
	defer d.Unlock()
	d.statusMap[clusterName] = propStatus
}

func (d *managedDispatcherImpl) recordOperationError(
	ctx context.Context,
	propStatus fedcorev1a1.PropagationStatusType,
	clusterName, operation string,
	err error,
) bool {
	d.recordError(ctx, clusterName, operation, err)
	d.RecordStatus(clusterName, propStatus)
	d.metrics.Counter(metrics.DispatchOperationErrorTotal, 1,
		stats.Tag{Name: "namespace", Value: d.fedResource.TargetName().Namespace},
		stats.Tag{Name: "name", Value: d.fedResource.TargetName().Name},
		stats.Tag{Name: "group", Value: d.fedResource.TargetGVK().Group},
		stats.Tag{Name: "version", Value: d.fedResource.TargetGVK().Version},
		stats.Tag{Name: "kind", Value: d.fedResource.TargetGVK().Kind},
		stats.Tag{Name: "operation", Value: operation},
		stats.Tag{Name: "cluster", Value: clusterName},
		stats.Tag{Name: "error_type", Value: string(propStatus)},
	)
	return false
}

func (d *managedDispatcherImpl) recordError(ctx context.Context, clusterName, operation string, err error) {
	targetName := d.unmanagedDispatcher.targetName
	args := []interface{}{operation, d.fedResource.TargetGVR().String(), targetName, clusterName}
	eventType := fmt.Sprintf("%sInClusterFailed", strings.Replace(strings.Title(operation), " ", "", -1))
	eventErr := errors.Wrapf(err, "Failed to "+eventTemplate, args...)
	logger := klog.FromContext(ctx)
	logger.Error(eventErr, "event", eventType, "Operation failed with error")
	d.fedResource.RecordError(eventType, eventErr)
	d.metrics.Counter(metrics.MemberOperationError, 1, []stats.Tag{
		{Name: "cluster", Value: clusterName},
		{Name: "operation", Value: operation},
		{Name: "reason", Value: string(apierrors.ReasonForError(err))},
	}...)
}

func (d *managedDispatcherImpl) recordEvent(clusterName, operation, operationContinuous string) {
	targetName := d.unmanagedDispatcher.targetName
	args := []interface{}{operationContinuous, d.fedResource.TargetGVR().String(), targetName, clusterName}
	eventType := fmt.Sprintf("%sInCluster", strings.Replace(strings.Title(operation), " ", "", -1))
	d.fedResource.RecordEvent(eventType, eventTemplate, args...)
}

func (d *managedDispatcherImpl) VersionMap() map[string]string {
	d.RLock()
	defer d.RUnlock()
	versionMap := make(map[string]string)
	for key, value := range d.versionMap {
		versionMap[key] = value
	}
	return versionMap
}

func (d *managedDispatcherImpl) recordVersion(clusterName, version string) {
	d.Lock()
	defer d.Unlock()
	d.versionMap[clusterName] = version
}

func (d *managedDispatcherImpl) setResourcesUpdated() {
	d.Lock()
	defer d.Unlock()
	d.resourcesUpdated = true
}

func (d *managedDispatcherImpl) CollectedStatus() status.CollectedPropagationStatus {
	d.RLock()
	defer d.RUnlock()
	statusMap := make(status.PropagationStatusMap)
	for key, value := range d.statusMap {
		statusMap[key] = value
	}
	return status.CollectedPropagationStatus{
		StatusMap:        statusMap,
		GenerationMap:    propagatedversion.ConvertVersionMapToGenerationMap(d.versionMap),
		ResourcesUpdated: d.resourcesUpdated,
	}
}
