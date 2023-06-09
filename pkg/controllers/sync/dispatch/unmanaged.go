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

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/finalizers"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/managedlabel"
)

const (
	RetainTerminatingObjectFinalizer = common.DefaultPrefix + "retain-terminating-object"
	eventTemplate                    = "%s %s %q in cluster %q"
)

// UnmanagedDispatcher dispatches operations to member clusters for
// resources that are no longer managed by a federated resource.
type UnmanagedDispatcher interface {
	OperationDispatcher

	Delete(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured)
	RemoveManagedLabel(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured)
}

type unmanagedDispatcherImpl struct {
	dispatcher *operationDispatcherImpl

	targetGVK  schema.GroupVersionKind
	targetName common.QualifiedName

	recorder dispatchRecorder
}

func NewUnmanagedDispatcher(
	clientAccessor clientAccessorFunc,
	targetGVK schema.GroupVersionKind,
	targetName common.QualifiedName,
) UnmanagedDispatcher {
	dispatcher := newOperationDispatcher(clientAccessor, nil)
	return newUnmanagedDispatcher(dispatcher, nil, targetGVK, targetName)
}

func newUnmanagedDispatcher(
	dispatcher *operationDispatcherImpl,
	recorder dispatchRecorder,
	targetGVK schema.GroupVersionKind,
	targetName common.QualifiedName,
) *unmanagedDispatcherImpl {
	return &unmanagedDispatcherImpl{
		dispatcher: dispatcher,
		targetGVK:  targetGVK,
		targetName: targetName,
		recorder:   recorder,
	}
}

func (d *unmanagedDispatcherImpl) Wait() (bool, error) {
	return d.dispatcher.Wait()
}

func (d *unmanagedDispatcherImpl) Delete(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured) {
	d.dispatcher.incrementOperationsInitiated()
	const op = "delete"
	const opContinuous = "Deleting"
	go d.dispatcher.clusterOperation(ctx, clusterName, op, func(client generic.Client) bool {
		keyedLogger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)
		targetName := d.targetNameForCluster(clusterName)
		keyedLogger.V(1).Info("Deleting target object in cluster")
		if d.recorder != nil {
			d.recorder.recordEvent(clusterName, op, opContinuous)
		}

		// Avoid mutating the resource in the informer cache
		clusterObj := clusterObj.DeepCopy()

		needUpdate, err := removeRetainObjectFinalizer(clusterObj)
		if err != nil {
			if d.recorder == nil {
				wrappedErr := d.wrapOperationError(err, clusterName, op)
				keyedLogger.Error(wrappedErr, "Failed to delete target object in cluster")
			} else {
				d.recorder.recordOperationError(ctx, fedtypesv1a1.DeletionFailed, clusterName, op, err)
			}
			return false
		}
		if needUpdate {
			err := client.Update(
				ctx,
				clusterObj,
			)
			if apierrors.IsNotFound(err) {
				err = nil
			}
			if err != nil {
				if d.recorder == nil {
					wrappedErr := d.wrapOperationError(err, clusterName, op)
					keyedLogger.Error(wrappedErr, "Failed to delete target object in cluster")
				} else {
					d.recorder.recordOperationError(ctx, fedtypesv1a1.DeletionFailed, clusterName, op, err)
				}
				return false
			}
		}

		// Avoid deleting a deleted resource again.
		if clusterObj.GetDeletionTimestamp() != nil {
			return true
		}

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(d.targetGVK)
		err = client.Delete(
			ctx,
			obj,
			targetName.Namespace,
			targetName.Name,
			// When deleting some resources (e.g. batch/v1.Job, batch/v1beta1.CronJob) without setting PropagationPolicy in DeleteOptions,
			// kube-apiserver defaults to Orphan for backward compatibility.
			// This would leak the dependents after the main propagated resource has been deleted.
			// Ref: https://github.com/kubernetes/kubernetes/pull/71792
			//
			// To avoid this, we explicitly set the PropagationPolicy to Background like `kubectl delete` does by default.
			// Ref: https://github.com/kubernetes/kubernetes/pull/65908
			runtimeclient.PropagationPolicy(metav1.DeletePropagationBackground),
		)
		if apierrors.IsNotFound(err) {
			err = nil
		}
		if err != nil {
			if d.recorder == nil {
				wrappedErr := d.wrapOperationError(err, clusterName, op)
				keyedLogger.Error(wrappedErr, "Failed to delete target object in cluster")
			} else {
				d.recorder.recordOperationError(ctx, fedtypesv1a1.DeletionFailed, clusterName, op, err)
			}
			return false
		}
		return true
	})
}

func (d *unmanagedDispatcherImpl) RemoveManagedLabel(ctx context.Context, clusterName string, clusterObj *unstructured.Unstructured) {
	d.dispatcher.incrementOperationsInitiated()
	const op = "remove managed label from"
	const opContinuous = "Removing managed label from"
	go d.dispatcher.clusterOperation(ctx, clusterName, op, func(client generic.Client) bool {
		keyedLogger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)
		keyedLogger.V(1).Info("Removing managed label from target object in cluster")
		if d.recorder != nil {
			d.recorder.recordEvent(clusterName, op, opContinuous)
		}

		// Avoid mutating the resource in the informer cache
		updateObj := clusterObj.DeepCopy()

		managedlabel.RemoveManagedLabel(updateObj)
		if _, err := removeRetainObjectFinalizer(updateObj); err != nil {
			if d.recorder == nil {
				wrappedErr := d.wrapOperationError(err, clusterName, op)
				keyedLogger.Error(wrappedErr, "Failed to remove managed label from target object in cluster")
			} else {
				d.recorder.recordOperationError(ctx, fedtypesv1a1.LabelRemovalFailed, clusterName, op, err)
			}
			return false
		}

		err := client.Update(ctx, updateObj)
		if err != nil {
			if d.recorder == nil {
				wrappedErr := d.wrapOperationError(err, clusterName, op)
				keyedLogger.Error(wrappedErr, "Failed to remove managed label from target object in cluster")
			} else {
				d.recorder.recordOperationError(ctx, fedtypesv1a1.LabelRemovalFailed, clusterName, op, err)
			}
			return false
		}
		return true
	})
}

func (d *unmanagedDispatcherImpl) wrapOperationError(err error, clusterName, operation string) error {
	return wrapOperationError(
		err,
		operation,
		d.targetGVK.Kind,
		d.targetNameForCluster(clusterName).String(),
		clusterName,
	)
}

func (d *unmanagedDispatcherImpl) targetNameForCluster(clusterName string) common.QualifiedName {
	return util.QualifiedNameForCluster(clusterName, d.targetName)
}

func wrapOperationError(err error, operation, targetKind, targetName, clusterName string) error {
	return errors.Wrapf(err, "Failed to "+eventTemplate, operation, targetKind, targetName, clusterName)
}

func removeRetainObjectFinalizer(obj *unstructured.Unstructured) (bool, error) {
	return finalizers.RemoveFinalizers(obj, sets.NewString(RetainTerminatingObjectFinalizer))
}
