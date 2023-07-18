//go:build exclude
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
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/status"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/adoption"
	"github.com/kubewharf/kubeadmiral/pkg/util/managedlabel"
)

const IndexRolloutPlans = "federation_placement_rollout"

// FederatedResourceForDispatch is the subset of the FederatedResource
// interface required for dispatching operations to managed resources.
type FederatedResourceForDispatch interface {
	TargetName() common.QualifiedName
	TargetGVK() schema.GroupVersionKind
	TargetGVR() schema.GroupVersionResource
	TypeConfig() *fedcorev1a1.FederatedTypeConfig
	// Object returns the federated object.
	Object() fedcorev1a1.GenericFederatedObject
	VersionForCluster(clusterName string) (string, error)
	ObjectForCluster(clusterName string) (*unstructured.Unstructured, error)
	ApplyOverrides(obj *unstructured.Unstructured, clusterName string) error
	RecordError(errorCode string, err error)
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
	d.unmanagedDispatcher = newUnmanagedDispatcher(d.dispatcher, d, fedResource.TargetGVR(), fedResource.TargetName())
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
		keyedLogger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)
		d.recordEvent(clusterName, op, "Creating")

		obj, err := d.fedResource.ObjectForCluster(clusterName)
		if err != nil {
			return d.recordOperationError(ctx, fedcorev1a1.ComputeResourceFailed, clusterName, op, err)
		}

		err = d.fedResource.ApplyOverrides(obj, clusterName)
		if err != nil {
			return d.recordOperationError(ctx, fedcorev1a1.ApplyOverridesFailed, clusterName, op, err)
		}

		recordPropagatedLabelsAndAnnotations(obj)

		ctxWithTimeout, cancel := context.WithTimeout(ctx, d.dispatcher.timeout)
		defer cancel()

		keyedLogger.V(1).Info("Creating target object in cluster")
		obj, err = client.Resource(d.fedResource.TargetGVR()).Namespace(obj.GetNamespace()).Create(
			ctx, obj, metav1.CreateOptions{},
		)
		if err == nil {
			version := util.ObjectVersion(obj)
			d.recordVersion(clusterName, version)
			return true
		}

		// TODO Figure out why attempting to create a namespace that
		// already exists indicates ServerTimeout instead of AlreadyExists.
		alreadyExists := apierrors.IsAlreadyExists(err) ||
			d.fedResource.TargetGVK() == corev1.SchemeGroupVersion.WithKind(common.NamespaceKind) && apierrors.IsServerTimeout(err)
		if !alreadyExists {
			return d.recordOperationError(ctx, fedcorev1a1.CreationFailed, clusterName, op, err)
		}

		// Attempt to update the existing resource to ensure that it
		// is labeled as a managed resource.
		obj, err = client.Resource(d.fedResource.TargetGVR()).Namespace(obj.GetNamespace()).Get(
			ctx, obj.GetName(), metav1.GetOptions{},
		)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retrieve object potentially requiring adoption")
			return d.recordOperationError(ctx, fedcorev1a1.RetrievalFailed, clusterName, op, wrappedErr)
		}

		if d.skipAdoptingResources {
			return d.recordOperationError(
				ctx,
				fedcorev1a1.AlreadyExists,
				clusterName,
				op,
				errors.Errorf("Resource pre-exist in cluster"),
			)
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
		return true
	})
}

func (d *managedDispatcherImpl) Update(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured) {
	d.RecordStatus(clusterName, fedcorev1a1.UpdateTimedOut)

	d.dispatcher.incrementOperationsInitiated()
	const op = "update"
	go d.dispatcher.clusterOperation(ctx, clusterName, op, func(client dynamic.Interface) bool {
		keyedLogger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)
		if managedlabel.IsExplicitlyUnmanaged(clusterObj) {
			err := errors.Errorf(
				"Unable to manage the object which has label %s: %s",
				managedlabel.ManagedByKubeAdmiralLabelKey,
				managedlabel.UnmanagedByKubeAdmiralLabelValue,
			)
			return d.recordOperationError(ctx, fedcorev1a1.ManagedLabelFalse, clusterName, op, err)
		}

		obj, err := d.fedResource.ObjectForCluster(clusterName)
		if err != nil {
			return d.recordOperationError(ctx, fedcorev1a1.ComputeResourceFailed, clusterName, op, err)
		}

		err = d.fedResource.ApplyOverrides(obj, clusterName)
		if err != nil {
			return d.recordOperationError(ctx, fedcorev1a1.ApplyOverridesFailed, clusterName, op, err)
		}

		recordPropagatedLabelsAndAnnotations(obj)

		err = RetainOrMergeClusterFields(d.fedResource.TargetGVK(), obj, clusterObj)
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retain fields")
			return d.recordOperationError(ctx, fedcorev1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
		}

		err = retainReplicas(obj, clusterObj, d.fedResource.Object(), d.fedResource.TypeConfig())
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retain replicas")
			return d.recordOperationError(ctx, fedcorev1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
		}

		version, err := d.fedResource.VersionForCluster(clusterName)
		if err != nil {
			return d.recordOperationError(ctx, fedcorev1a1.VersionRetrievalFailed, clusterName, op, err)
		}

		if !util.ObjectNeedsUpdate(obj, clusterObj, version, d.fedResource.TypeConfig()) {
			// Resource is current, we still record version in dispatcher
			// so that federated status can be set with cluster resource generation
			d.recordVersion(clusterName, version)
			return true
		}

		// Only record an event if the resource is not current
		d.recordEvent(clusterName, op, "Updating")

		keyedLogger.V(1).Info("Updating target object in cluster")
		obj, err = client.Resource(d.fedResource.TargetGVR()).Namespace(obj.GetNamespace()).Update(
			ctx, obj, metav1.UpdateOptions{},
		)
		if err != nil {
			return d.recordOperationError(ctx, fedcorev1a1.UpdateFailed, clusterName, op, err)
		}
		d.setResourcesUpdated()
		version = util.ObjectVersion(obj)
		d.recordVersion(clusterName, version)
		return true
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
	return false
}

func (d *managedDispatcherImpl) recordError(ctx context.Context, clusterName, operation string, err error) {
	targetName := d.unmanagedDispatcher.targetNameForCluster(clusterName)
	args := []interface{}{operation, d.fedResource.TargetGVR().String(), targetName, clusterName}
	eventType := fmt.Sprintf("%sInClusterFailed", strings.Replace(strings.Title(operation), " ", "", -1))
	eventErr := errors.Wrapf(err, "Failed to "+eventTemplate, args...)
	logger := klog.FromContext(ctx)
	logger.Error(eventErr, "event", eventType, "Operation failed with error")
	d.fedResource.RecordError(eventType, eventErr)
	d.metrics.Rate("member_operation_error", 1, []stats.Tag{
		{Name: "cluster", Value: clusterName},
		{Name: "operation", Value: operation},
		{Name: "reason", Value: string(apierrors.ReasonForError(err))},
	}...)
}

func (d *managedDispatcherImpl) recordEvent(clusterName, operation, operationContinuous string) {
	targetName := d.unmanagedDispatcher.targetNameForCluster(clusterName)
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
		GenerationMap:    util.ConvertVersionMapToGenerationMap(d.versionMap),
		ResourcesUpdated: d.resourcesUpdated,
	}
}
