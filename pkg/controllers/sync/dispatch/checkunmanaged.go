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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

type CheckUnmanagedDispatcher interface {
	OperationDispatcher

	CheckRemovedOrUnlabeled(ctx context.Context, clusterName string)
}

type checkUnmanagedDispatcherImpl struct {
	dispatcher *operationDispatcherImpl

	targetGVK  schema.GroupVersionKind
	targetName common.QualifiedName
}

func NewCheckUnmanagedDispatcher(
	clientAccessor clientAccessorFunc,
	targetGVK schema.GroupVersionKind,
	targetName common.QualifiedName,
) CheckUnmanagedDispatcher {
	dispatcher := newOperationDispatcher(clientAccessor, nil)
	return &checkUnmanagedDispatcherImpl{
		dispatcher: dispatcher,
		targetGVK:  targetGVK,
		targetName: targetName,
	}
}

func (d *checkUnmanagedDispatcherImpl) Wait() (bool, error) {
	return d.dispatcher.Wait()
}

// CheckRemovedOrUnlabeled checks that a resource either does not
// exist in the given cluster, or if it does exist, that it does not
// have the managed label.
func (d *checkUnmanagedDispatcherImpl) CheckRemovedOrUnlabeled(ctx context.Context, clusterName string) {
	d.dispatcher.incrementOperationsInitiated()
	const op = "check for deletion of resource or removal of managed label from"
	errLogMessage := fmt.Sprintf("Failed to %s target obj", op)
	go d.dispatcher.clusterOperation(ctx, clusterName, op, func(client generic.Client) bool {
		keyedLogger := klog.FromContext(ctx).WithValues("cluster-name", clusterName)
		targetName := d.targetNameForCluster(clusterName)

		keyedLogger.V(2).Info("Checking for deletion of resource or removal of managed label from target obj")
		clusterObj := &unstructured.Unstructured{}
		clusterObj.SetGroupVersionKind(d.targetGVK)
		err := client.Get(context.Background(), clusterObj, targetName.Namespace, targetName.Name)
		if apierrors.IsNotFound(err) {
			return true
		}
		if err != nil {
			keyedLogger.Error(fmt.Errorf("failed to get resource: %w", err), errLogMessage)
			return false
		}
		if clusterObj.GetDeletionTimestamp() != nil {
			keyedLogger.Error(fmt.Errorf("resource is pending deletion"), errLogMessage)
			return false
		}
		if !util.HasManagedLabel(clusterObj) {
			return true
		}
		keyedLogger.Error(fmt.Errorf("resource still has the managed label"), errLogMessage)
		return false
	})
}

func (d *checkUnmanagedDispatcherImpl) wrapOperationError(err error, clusterName, operation string) error {
	return wrapOperationError(
		err,
		operation,
		d.targetGVK.Kind,
		d.targetNameForCluster(clusterName).String(),
		clusterName,
	)
}

func (d *checkUnmanagedDispatcherImpl) targetNameForCluster(clusterName string) common.QualifiedName {
	return util.QualifiedNameForCluster(clusterName, d.targetName)
}
