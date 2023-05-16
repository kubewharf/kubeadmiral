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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/status"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/controllers/util/unstructured"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
)

const IndexRolloutPlans = "federation_placement_rollout"

// FederatedResourceForDispatch is the subset of the FederatedResource
// interface required for dispatching operations to managed resources.
type FederatedResourceForDispatch interface {
	TargetName() common.QualifiedName
	TargetKind() string
	TargetGVK() schema.GroupVersionKind
	TypeConfig() *fedcorev1a1.FederatedTypeConfig
	// Replicas is the number of replicas specified in the federated object.
	Replicas() (*int64, error)
	// Object returns the federated object.
	Object() *unstructured.Unstructured
	VersionForCluster(clusterName string) (string, error)
	ObjectForCluster(clusterName string) (*unstructured.Unstructured, error)
	ApplyOverrides(
		obj *unstructured.Unstructured,
		clusterName string,
		otherOverrides fedtypesv1a1.OverridePatches,
	) error
	RecordError(errorCode string, err error)
	RecordEvent(reason, messageFmt string, args ...interface{})
	ReplicasOverrideForCluster(clusterName string) (int32, bool, error)
	TotalReplicas(clusterNames sets.String) (int32, error)
}

// ManagedDispatcher dispatches operations to member clusters for resources
// managed by a federated resource.
type ManagedDispatcher interface {
	UnmanagedDispatcher

	Create(clusterName string)
	Update(clusterName string, clusterObj *unstructured.Unstructured)
	VersionMap() map[string]string
	CollectedStatus() status.CollectedPropagationStatus
	RecordClusterError(propStatus fedtypesv1a1.PropagationStatus, clusterName string, err error)
	RecordStatus(clusterName string, propStatus fedtypesv1a1.PropagationStatus)
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
	rolloutPlans     util.RolloutPlans

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
	d.unmanagedDispatcher = newUnmanagedDispatcher(d.dispatcher, d, fedResource.TargetGVK(), fedResource.TargetName())
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
		string(fedtypesv1a1.CreationTimedOut),
		string(fedtypesv1a1.UpdateTimedOut),
	)
	for key, value := range d.statusMap {
		propStatus := string(value)
		if okTimedOut.Has(propStatus) {
			d.statusMap[key] = fedtypesv1a1.ClusterPropagationOK
		} else if propStatus == string(fedtypesv1a1.DeletionTimedOut) {
			// If deletion was successful, then assume the resource is
			// pending garbage collection.
			d.statusMap[key] = fedtypesv1a1.WaitingForRemoval
		} else if propStatus == string(fedtypesv1a1.LabelRemovalTimedOut) {
			// If label removal was successful, the resource is
			// effectively unmanaged for the cluster even though it
			// still may be cached.
			delete(d.statusMap, key)
		}
	}
	return ok, nil
}

// Deprecated: this method is not used and outdated, but should be kept to reintegrate rollout planner in the future
func (d *managedDispatcherImpl) Dispatch(targetGetter targetAccessorFunc, clusters []*fedcorev1a1.FederatedCluster,
	selectedClusterNames sets.String, rolloutPlanEnabled bool,
) {
	clusterObjs := make(map[string]*unstructured.Unstructured)
	toDelete := sets.String{}
	for _, cluster := range clusters {
		clusterName := cluster.Name
		selectedCluster := selectedClusterNames.Has(clusterName)

		if !util.IsClusterReady(&cluster.Status) {
			if selectedCluster {
				// Cluster state only needs to be reported in resource
				// status for clusters selected for placement.
				err := errors.New("Cluster not ready")
				d.RecordClusterError(fedtypesv1a1.ClusterNotReady, clusterName, err)
			}
			continue
		}

		clusterObj, err := targetGetter(clusterName)
		if err != nil {
			wrappedErr := errors.Wrap(err, "Failed to retrieve cached cluster object")
			d.RecordClusterError(fedtypesv1a1.CachedRetrievalFailed, clusterName, wrappedErr)
			continue
		}

		// Resource should not exist in the named cluster
		if !selectedCluster {
			if clusterObj == nil {
				// Resource does not exist in the cluster
				continue
			}
			if clusterObj.GetDeletionTimestamp() != nil {
				// Resource is marked for deletion
				d.RecordStatus(clusterName, fedtypesv1a1.WaitingForRemoval)
				continue
			}
			toDelete.Insert(clusterName)
		}

		clusterObjs[clusterName] = clusterObj
	}

	// skip rollout plan for hpa and daemonset
	if rolloutPlanEnabled {
		retain, err := checkRetainReplicas(d.fedResource.Object())
		if err != nil {
			d.fedResource.RecordError("CheckRetainReplicasFailed", err)
			return
		}
		if retain {
			rolloutPlanEnabled = false
		}
	}
	if rolloutPlanEnabled {
		clusterPlans, err := d.planRolloutProcess(clusterObjs, selectedClusterNames, toDelete)
		if err != nil {
			d.fedResource.RecordError(string(fedtypesv1a1.PlanRolloutFailed), err)
		}
		for clusterName, clusterObj := range clusterObjs {
			var planned bool
			var plan util.RolloutPlan
			if clusterPlans != nil {
				if p, ok := clusterPlans[clusterName]; ok && p != nil {
					planned = true
					plan = *p
				}
			}

			if !planned {
				if clusterObj != nil {
					// dispatch without updating template
					d.PatchAndKeepTemplate(clusterName, clusterObj, true)
				}
				continue
			}
			if toDelete.Has(clusterName) && (plan.Replicas == nil || plan.Replicas != nil && *plan.Replicas == 0) {
				d.Delete(clusterName)
				continue
			}
			if clusterObj == nil {
				d.Create(clusterName)
				continue
			}
			if plan.OnlyPatchReplicas && plan.Replicas != nil {
				d.PatchAndKeepTemplate(clusterName, clusterObj, false)
				continue
			}
			d.Update(clusterName, clusterObj)
		}
		return
	}

	d.emitRolloutStatus(clusterObjs, selectedClusterNames, nil)
	for clusterName, clusterObj := range clusterObjs {
		if toDelete.Has(clusterName) {
			d.Delete(clusterName)
			continue
		}

		// TODO Consider waiting until the result of resource
		// creation has reached the target store before attempting
		// subsequent operations.  Otherwise the object won't be found
		// but an add operation will fail with AlreadyExists.
		if clusterObj == nil {
			d.Create(clusterName)
		} else {
			d.Update(clusterName, clusterObj)
		}
	}
}

func (d *managedDispatcherImpl) planRolloutProcess(clusterObjs map[string]*unstructured.Unstructured,
	selectedClusterNames, toDelete sets.String,
) (util.RolloutPlans, error) {
	var (
		r        = d.fedResource
		key      = r.TargetName().String()
		gvk      = r.TargetGVK()
		planner  *util.RolloutPlanner
		plans    util.RolloutPlans
		replicas int32
		err      error
	)

	defer func() {
		if err != nil {
			klog.V(4).Infof("Generate rollout plans for %s %s met error: %v", gvk, key, err)
		} else {
			klog.V(4).Infof("Generate rollout plans for %s %s: %v. Current status: %s", gvk, key, plans, planner)
		}
		SendRolloutPlansToES(planner, plans, r.Object(), err)
		d.emitRolloutStatus(clusterObjs, selectedClusterNames, planner)
	}()

	if gvk != appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind) {
		err = errors.Errorf("Unsupported target type for rollout plan: %s", gvk)
		return nil, err
	}
	if replicas, err = r.TotalReplicas(selectedClusterNames); err != nil {
		return nil, err
	}
	if planner, err = util.NewRolloutPlanner(key, r.TypeConfig(), r.Object(), replicas); err != nil {
		return nil, err
	}
	for clusterName, clusterObj := range clusterObjs {
		var desiredReplicas int32
		if !toDelete.Has(clusterName) {
			var dr int32
			if dr, _, err = r.ReplicasOverrideForCluster(clusterName); err != nil {
				return nil, err
			}
			desiredReplicas = dr
		}
		if err = planner.RegisterTarget(clusterName, clusterObj, desiredReplicas); err != nil {
			err = errors.Wrap(err, "Failed to register target in "+clusterName)
			return nil, err
		}
	}
	plans = planner.Plan()
	d.rolloutPlans = plans
	return plans, nil
}

func (d *managedDispatcherImpl) Create(clusterName string) {
	// Default the status to an operation-specific timeout.  Otherwise
	// when a timeout occurs it won't be possible to determine which
	// operation timed out.  The timeout status will be cleared by
	// Wait() if a timeout does not occur.
	d.RecordStatus(clusterName, fedtypesv1a1.CreationTimedOut)

	d.dispatcher.incrementOperationsInitiated()
	const op = "create"
	go d.dispatcher.clusterOperation(clusterName, op, func(client generic.Client) bool {
		d.recordEvent(clusterName, op, "Creating")

		obj, err := d.fedResource.ObjectForCluster(clusterName)
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.ComputeResourceFailed, clusterName, op, err)
		}

		err = d.fedResource.ApplyOverrides(obj, clusterName, d.rolloutOverrides(clusterName))
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.ApplyOverridesFailed, clusterName, op, err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), d.dispatcher.timeout)
		defer cancel()

		err = client.Create(ctx, obj)
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
			return d.recordOperationError(fedtypesv1a1.CreationFailed, clusterName, op, err)
		}

		// Attempt to update the existing resource to ensure that it
		// is labeled as a managed resource.
		err = client.Get(ctx, obj, obj.GetNamespace(), obj.GetName())
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retrieve object potentially requiring adoption")
			return d.recordOperationError(fedtypesv1a1.RetrievalFailed, clusterName, op, wrappedErr)
		}

		if d.skipAdoptingResources {
			return d.recordOperationError(
				fedtypesv1a1.AlreadyExists,
				clusterName,
				op,
				errors.Errorf("Resource pre-exist in cluster"),
			)
		}

		d.recordError(
			clusterName,
			op,
			errors.Errorf("An update will be attempted instead of a creation due to an existing resource"),
		)
		if !util.HasManagedLabel(obj) {
			// If the object was not managed by us, mark it as adopted.
			annotation.AddAnnotation(obj, util.AdoptedAnnotation, common.AnnotationValueTrue)
		}
		d.Update(clusterName, obj)
		return true
	})
}

func (d *managedDispatcherImpl) Update(clusterName string, clusterObj *unstructured.Unstructured) {
	d.RecordStatus(clusterName, fedtypesv1a1.UpdateTimedOut)

	d.dispatcher.incrementOperationsInitiated()
	const op = "update"
	go d.dispatcher.clusterOperation(clusterName, op, func(client generic.Client) bool {
		if util.IsExplicitlyUnmanaged(clusterObj) {
			err := errors.Errorf(
				"Unable to manage the object which has label %s: %s",
				util.ManagedByKubeFedLabelKey,
				util.UnmanagedByKubeFedLabelValue,
			)
			return d.recordOperationError(fedtypesv1a1.ManagedLabelFalse, clusterName, op, err)
		}

		obj, err := d.fedResource.ObjectForCluster(clusterName)
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.ComputeResourceFailed, clusterName, op, err)
		}

		err = RetainOrMergeClusterFields(d.fedResource.TargetGVK(), obj, clusterObj, d.fedResource.Object())
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retain fields")
			return d.recordOperationError(fedtypesv1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
		}

		err = d.fedResource.ApplyOverrides(obj, clusterName, d.rolloutOverrides(clusterName))
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.ApplyOverridesFailed, clusterName, op, err)
		}

		err = retainReplicas(obj, clusterObj, d.fedResource.Object(), d.fedResource.TypeConfig())
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retain replicas")
			return d.recordOperationError(fedtypesv1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
		}

		if d.fedResource.TargetGVK() == appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind) {
			err = setLastReplicasetName(obj, clusterObj)
			if err != nil {
				wrappedErr := errors.Wrapf(err, "failed to set last replicaset name")
				return d.recordOperationError(fedtypesv1a1.SetLastReplicasetNameFailed, clusterName, op, wrappedErr)
			}
		}

		version, err := d.fedResource.VersionForCluster(clusterName)
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.VersionRetrievalFailed, clusterName, op, err)
		}

		if !util.ObjectNeedsUpdate(obj, clusterObj, version, d.fedResource.TypeConfig()) {
			// Resource is current, we still record version in dispatcher
			// so that federated status can be set with cluster resource generation
			d.recordVersion(clusterName, version)
			return true
		}

		// Only record an event if the resource is not current
		d.recordEvent(clusterName, op, "Updating")

		err = client.Update(context.Background(), obj)
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.UpdateFailed, clusterName, op, err)
		}
		klog.V(4).Infof("debug - Updated %s %s/%s in %s", obj.GetKind(), obj.GetNamespace(), obj.GetName(), clusterName)
		d.setResourcesUpdated()
		version = util.ObjectVersion(obj)
		d.recordVersion(clusterName, version)
		return true
	})
}

func (d *managedDispatcherImpl) Delete(clusterName string) {
	d.RecordStatus(clusterName, fedtypesv1a1.DeletionTimedOut)

	d.unmanagedDispatcher.Delete(clusterName)
}

func (d *managedDispatcherImpl) PatchAndKeepTemplate(
	clusterName string,
	clusterObj *unstructured.Unstructured,
	keepRolloutSettings bool,
) {
	d.RecordStatus(clusterName, fedtypesv1a1.UpdateTimedOut)

	d.dispatcher.incrementOperationsInitiated()
	const op = "update"
	go d.dispatcher.clusterOperation(clusterName, op, func(client generic.Client) bool {
		if util.IsExplicitlyUnmanaged(clusterObj) {
			err := errors.Errorf(
				"Unable to manage the object which has label %s: %s",
				util.ManagedByKubeFedLabelKey,
				util.UnmanagedByKubeFedLabelValue,
			)
			return d.recordOperationError(fedtypesv1a1.ManagedLabelFalse, clusterName, op, err)
		}

		obj, err := d.fedResource.ObjectForCluster(clusterName)
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.ComputeResourceFailed, clusterName, op, err)
		}

		err = RetainOrMergeClusterFields(d.fedResource.TargetGVK(), obj, clusterObj, d.fedResource.Object())
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retain fields")
			return d.recordOperationError(fedtypesv1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
		}

		err = d.fedResource.ApplyOverrides(obj, clusterName, d.rolloutOverrides(clusterName))
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.ApplyOverridesFailed, clusterName, op, err)
		}

		err = retainReplicas(obj, clusterObj, d.fedResource.Object(), d.fedResource.TypeConfig())
		if err != nil {
			wrappedErr := errors.Wrapf(err, "failed to retain replicas")
			return d.recordOperationError(fedtypesv1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
		}

		if d.fedResource.TargetGVK() == appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind) {
			if err = retainTemplate(obj, clusterObj, d.fedResource.TypeConfig(), keepRolloutSettings); err != nil {
				wrappedErr := errors.Wrapf(err, "failed to retain template")
				return d.recordOperationError(fedtypesv1a1.FieldRetentionFailed, clusterName, op, wrappedErr)
			}
			if err = setLastReplicasetName(obj, clusterObj); err != nil {
				wrappedErr := errors.Wrapf(err, "failed to set last replicaset name")
				return d.recordOperationError(fedtypesv1a1.SetLastReplicasetNameFailed, clusterName, op, wrappedErr)
			}
		}

		version, err := d.fedResource.VersionForCluster(clusterName)
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.VersionRetrievalFailed, clusterName, op, err)
		}

		if !util.ObjectNeedsUpdate(obj, clusterObj, version, d.fedResource.TypeConfig()) {
			// Resource is current, we still record version in dispatcher
			// so that federated status can be set with cluster resource generation
			d.recordVersion(clusterName, version)
			return true
		}

		// Only record an event if the resource is not current
		d.recordEvent(clusterName, op, "Updating")

		err = client.Update(context.Background(), obj)
		if err != nil {
			return d.recordOperationError(fedtypesv1a1.UpdateFailed, clusterName, op, err)
		}
		klog.V(4).
			Infof("debug - Updated and kept template %s %s/%s in %s", obj.GetKind(), obj.GetNamespace(), obj.GetName(), clusterName)
		d.setResourcesUpdated()
		version = util.ObjectVersion(obj)
		d.recordVersion(clusterName, version)
		return true
	})
}

func (d *managedDispatcherImpl) RemoveManagedLabel(clusterName string, clusterObj *unstructured.Unstructured) {
	d.RecordStatus(clusterName, fedtypesv1a1.LabelRemovalTimedOut)

	d.unmanagedDispatcher.RemoveManagedLabel(clusterName, clusterObj)
}

func (d *managedDispatcherImpl) RecordClusterError(
	propStatus fedtypesv1a1.PropagationStatus,
	clusterName string,
	err error,
) {
	d.fedResource.RecordError(string(propStatus), err)
	d.RecordStatus(clusterName, propStatus)
}

func (d *managedDispatcherImpl) RecordStatus(clusterName string, propStatus fedtypesv1a1.PropagationStatus) {
	d.Lock()
	defer d.Unlock()
	d.statusMap[clusterName] = propStatus
}

func (d *managedDispatcherImpl) recordOperationError(
	propStatus fedtypesv1a1.PropagationStatus,
	clusterName, operation string,
	err error,
) bool {
	d.recordError(clusterName, operation, err)
	d.RecordStatus(clusterName, propStatus)
	return false
}

func (d *managedDispatcherImpl) recordError(clusterName, operation string, err error) {
	targetName := d.unmanagedDispatcher.targetNameForCluster(clusterName)
	args := []interface{}{operation, d.fedResource.TargetKind(), targetName, clusterName}
	eventType := fmt.Sprintf("%sInClusterFailed", strings.Replace(strings.Title(operation), " ", "", -1))
	eventErr := errors.Wrapf(err, "Failed to "+eventTemplate, args...)
	klog.Infof("%s with error %s", eventType, eventErr)
	d.fedResource.RecordError(eventType, eventErr)
	d.metrics.Rate("member_operation_error", 1, []stats.Tag{
		{Name: "cluster", Value: clusterName},
		{Name: "operation", Value: operation},
		{Name: "reason", Value: string(apierrors.ReasonForError(err))},
	}...)
}

func (d *managedDispatcherImpl) recordEvent(clusterName, operation, operationContinuous string) {
	targetName := d.unmanagedDispatcher.targetNameForCluster(clusterName)
	args := []interface{}{operationContinuous, d.fedResource.TargetKind(), targetName, clusterName}
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

func (d *managedDispatcherImpl) rolloutOverrides(clusterName string) fedtypesv1a1.OverridePatches {
	if d.rolloutPlans == nil {
		return fedtypesv1a1.OverridePatches{}
	}
	return d.rolloutPlans.GetRolloutOverrides(clusterName)
}

// emitRolloutStatus temporarily emit status metrics during rollout for observation
func (d *managedDispatcherImpl) emitRolloutStatus(
	clusterObjs map[string]*unstructured.Unstructured,
	selectedClusterNames sets.String,
	planner *util.RolloutPlanner,
) {
	r := d.fedResource
	deployName := r.TargetName().Name
	if r.TargetGVK() != appsv1.SchemeGroupVersion.WithKind(common.DeploymentKind) {
		return
	}

	fedClusterName := "fed"
	fedTags := []stats.Tag{
		{Name: "dp", Value: deployName},
		{Name: "cluster", Value: fedClusterName},
		{Name: "ismember", Value: "false"},
	}

	// settings
	if planner == nil {
		replicas, err := r.TotalReplicas(selectedClusterNames)
		if err != nil {
			klog.Errorf("Skip rollout metrics: failed to get replicas: %v", err)
			return
		}
		pathPrefix := []string{common.SpecField, common.TemplateField}
		maxSurgePath := append(pathPrefix, util.MaxSurgePathSlice...)
		maxUnavailablePath := append(pathPrefix, util.MaxUnavailablePathSlice...)
		maxSurge, maxUnavailable, err := util.RetrieveFencepost(r.Object(), maxSurgePath, maxUnavailablePath, replicas)
		if err != nil {
			klog.Errorf("Skip rollout metrics: failed to get maxSurge and maxUnavailable: %v", err)
			return
		}
		_ = d.metrics.Store("sync.rollout.maxSurge", maxSurge, fedTags...)
		_ = d.metrics.Store("sync.rollout.maxUnavailable", maxUnavailable, fedTags...)
		_ = d.metrics.Store("sync.rollout.minAvailable", replicas-maxUnavailable, fedTags...)
	} else {
		_ = d.metrics.Store("sync.rollout.maxSurge", planner.MaxSurge, fedTags...)
		_ = d.metrics.Store("sync.rollout.maxUnavailable", planner.MaxUnavailable, fedTags...)
		_ = d.metrics.Store("sync.rollout.minAvailable", planner.Replicas-planner.MaxUnavailable, fedTags...)

		for _, t := range planner.Targets {
			clusterName := t.ClusterName
			tags := []stats.Tag{{Name: "dp", Value: deployName}, {Name: "cluster", Value: clusterName}, {Name: "ismember", Value: "true"}}
			_ = d.metrics.Store("sync.rollout.maxSurge", t.Status.MaxSurge, tags...)
			_ = d.metrics.Store("sync.rollout.maxUnavailable", t.Status.MaxUnavailable, tags...)
		}
	}

	// status
	var unavailable, surge, available int64
	for clusterName, clusterObj := range clusterObjs {
		if clusterObj == nil {
			continue
		}
		tags := []stats.Tag{
			{Name: "dp", Value: deployName},
			{Name: "cluster", Value: clusterName},
			{Name: "ismember", Value: "true"},
		}
		if u, ok, err := unstructured.NestedInt64(clusterObj.Object, "status", "unavailableReplicas"); err == nil &&
			ok {
			_ = d.metrics.Store("sync.rollout.unavailable", u, tags...)
			unavailable += u
		}
		if r, ok, err := unstructured.NestedInt64(clusterObj.Object, "status", "replicas"); err == nil && ok {
			if r0, err := utilunstructured.GetInt64FromPath(clusterObj, d.fedResource.TypeConfig().Spec.PathDefinition.ReplicasSpec, nil); err == nil &&
				r0 != nil {
				s := r - *r0
				_ = d.metrics.Store("sync.rollout.surge", s, tags...)
				surge += s
			}
		}
		if a, ok, err := unstructured.NestedInt64(clusterObj.Object, "status", "availableReplicas"); err == nil && ok {
			_ = d.metrics.Store("sync.rollout.available", a, tags...)
			available += a
		}
	}
	_ = d.metrics.Store("sync.rollout.surge", surge, fedTags...)
	_ = d.metrics.Store("sync.rollout.unavailable", unavailable, fedTags...)
	_ = d.metrics.Store("sync.rollout.available", available, fedTags...)
}

func SendRolloutPlansToES(
	planner *util.RolloutPlanner,
	plans util.RolloutPlans,
	fedResource *unstructured.Unstructured,
	err error,
) {
	data := struct {
		Date           time.Time
		Name           string
		Namespace      string
		Kind           string
		Replicas       int32
		MaxSurge       int32
		MaxUnavailable int32
		Revision       string
		CurrentStatus  string
		Result         util.RolloutPlans
		ResultStr      string
		Error          string
	}{
		Date:      time.Now(),
		Name:      fedResource.GetName(),
		Namespace: fedResource.GetNamespace(),
		Kind:      fedResource.GetKind(),
		Result:    plans,
	}
	if err != nil {
		data.Error = err.Error()
	} else {
		if planner != nil {
			var s []string
			for _, t := range planner.Targets {
				s = append(s, t.String())
			}
			data.Replicas = planner.Replicas
			data.MaxSurge = planner.MaxSurge
			data.MaxUnavailable = planner.MaxUnavailable
			data.Revision = planner.Revision
			data.CurrentStatus = strings.Join(s, "\n")
		}
		if len(plans) > 0 {
			var s []string
			for c, p := range plans {
				s = append(s, c+":"+p.String())
			}
			data.ResultStr = strings.Join(s, "\n")
		}
	}
}
