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

package sync

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedcorev1a1client "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned/typed/core/v1alpha1"
	fedcorev1a1informers "github.com/kubewharf/kubeadmiral/pkg/client/informers/externalversions/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/version"
	"github.com/kubewharf/kubeadmiral/pkg/util/eventhandlers"
	"github.com/kubewharf/kubeadmiral/pkg/util/fedobjectadapters"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager"
)

// FederatedResourceAccessor provides a way to retrieve and visit
// logical federated resources (e.g. FederatedConfigMap)
type FederatedResourceAccessor interface {
	Run(context.Context)
	HasSynced() bool
	FederatedResource(
		qualifiedName common.QualifiedName,
	) (federatedResource FederatedResource, err error)
	VisitFederatedResources(visitFunc func(fedcorev1a1.GenericFederatedObject))
}

type resourceAccessor struct {
	fedNamespace string

	// Informers for federated objects
	fedObjectInformer        fedcorev1a1informers.FederatedObjectInformer
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer

	// Manages propagated versions
	versionManager        *version.VersionManager
	clusterVersionManager *version.VersionManager

	// Manages FTCs
	ftcManager informermanager.FederatedTypeConfigManager

	// Records events on the federated resource
	eventRecorder record.EventRecorder

	logger klog.Logger
}

func NewFederatedResourceAccessor(
	logger klog.Logger,
	fedSystemNamespace, targetNamespace string,
	client fedcorev1a1client.CoreV1alpha1Interface,
	fedObjectInformer fedcorev1a1informers.FederatedObjectInformer,
	clusterFedObjectInformer fedcorev1a1informers.ClusterFederatedObjectInformer,
	ftcManager informermanager.FederatedTypeConfigManager,
	enqueue func(common.QualifiedName),
	eventRecorder record.EventRecorder,
) FederatedResourceAccessor {
	a := &resourceAccessor{
		fedNamespace:             fedSystemNamespace,
		fedObjectInformer:        fedObjectInformer,
		clusterFedObjectInformer: clusterFedObjectInformer,
		ftcManager:               ftcManager,
		eventRecorder:            eventRecorder,
		logger:                   logger.WithValues("origin", "resource-accessor"),
	}

	handler := eventhandlers.NewTriggerOnAllChanges(func(o pkgruntime.Object) {
		enqueue(common.NewQualifiedName(o))
	})
	fedObjectInformer.Informer().AddEventHandler(handler)
	clusterFedObjectInformer.Informer().AddEventHandler(handler)

	a.versionManager = version.NewNamespacedVersionManager(
		logger,
		client,
		targetNamespace,
	)
	a.clusterVersionManager = version.NewClusterVersionManager(
		logger,
		client,
	)

	return a
}

func (a *resourceAccessor) Run(ctx context.Context) {
	go a.versionManager.Sync(ctx)
	go a.clusterVersionManager.Sync(ctx)
}

func (a *resourceAccessor) HasSynced() bool {
	if !a.versionManager.HasSynced() {
		a.logger.V(3).Info("Version manager not synced")
		return false
	}
	if !a.clusterVersionManager.HasSynced() {
		a.logger.V(3).Info("Cluster version manager not synced")
		return false
	}
	return true
}

func (a *resourceAccessor) FederatedResource(
	qualifiedName common.QualifiedName,
) (FederatedResource, error) {
	federatedObject, err := fedobjectadapters.GetFromLister(
		a.fedObjectInformer.Lister(),
		a.clusterFedObjectInformer.Lister(),
		qualifiedName.Namespace,
		qualifiedName.Name,
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		} else {
			return nil, err
		}
	}
	federatedObject = federatedObject.DeepCopyGenericFederatedObject()

	template := &unstructured.Unstructured{}
	if err := template.UnmarshalJSON(federatedObject.GetSpec().Template.Raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal template: %w", err)
	}

	targetGVK := template.GroupVersionKind()
	if targetGVK == corev1.SchemeGroupVersion.WithKind(common.NamespaceKind) && a.isSystemNamespace(template.GetName()) {
		return nil, nil
	}

	typeConfig, exists := a.ftcManager.GetResourceFTC(targetGVK)
	if !exists || typeConfig == nil {
		return nil, nil
	}
	typeConfig = typeConfig.DeepCopy()

	var versionManager *version.VersionManager
	if typeConfig.GetNamespaced() {
		versionManager = a.versionManager
	} else {
		versionManager = a.clusterVersionManager
	}

	return &federatedResource{
		typeConfig:      typeConfig,
		federatedName:   qualifiedName,
		targetName:      common.NewQualifiedName(template),
		federatedObject: federatedObject,
		template:        template,
		versionManager:  versionManager,
		eventRecorder:   a.eventRecorder,
	}, nil
}

func (a *resourceAccessor) VisitFederatedResources(visitFunc func(obj fedcorev1a1.GenericFederatedObject)) {
	fedObjects, err := a.fedObjectInformer.Lister().List(labels.Everything())
	if err == nil {
		for _, obj := range fedObjects {
			visitFunc(obj)
		}
	} else {
		a.logger.Error(err, "Failed to list FederatedObjects from lister")
	}

	clusterFedObjects, err := a.clusterFedObjectInformer.Lister().List(labels.Everything())
	if err == nil {
		for _, obj := range clusterFedObjects {
			visitFunc(obj)
		}
	} else {
		a.logger.Error(err, "Failed to list ClusterFederatedObjects from lister")
	}
}

func (a *resourceAccessor) isSystemNamespace(namespace string) bool {
	// TODO(font): Need a configurable or discoverable list of namespaces
	// to not propagate beyond just the default system namespaces e.g.
	switch namespace {
	case "kube-system", "kube-public", "default", a.fedNamespace:
		return true
	default:
		return false
	}
}
