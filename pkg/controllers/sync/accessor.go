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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	genericclient "github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync/version"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

// FederatedResourceAccessor provides a way to retrieve and visit
// logical federated resources (e.g. FederatedConfigMap)
type FederatedResourceAccessor interface {
	Run(stopChan <-chan struct{})
	HasSynced() bool
	FederatedResource(
		qualifiedName common.QualifiedName,
	) (federatedResource FederatedResource, possibleOrphan bool, err error)
	VisitFederatedResources(visitFunc func(obj interface{}))
}

type resourceAccessor struct {
	limitedScope bool
	typeConfig   *fedcorev1a1.FederatedTypeConfig
	fedNamespace string

	// The informer for the federated type.
	federatedStore      cache.Store
	federatedController cache.Controller

	fedNamespaceAPIResource *metav1.APIResource

	// The informer used to source federated namespaces used in
	// determining placement for namespaced resources.  Will only be
	// initialized if the target resource is namespaced.
	fedNamespaceStore      cache.Store
	fedNamespaceController cache.Controller

	// Manages propagated versions
	versionManager *version.VersionManager

	// Records events on the federated resource
	eventRecorder record.EventRecorder
}

func NewFederatedResourceAccessor(
	controllerConfig *util.ControllerConfig,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	fedNamespaceAPIResource *metav1.APIResource,
	client genericclient.Client,
	enqueueObj func(pkgruntime.Object),
	eventRecorder record.EventRecorder,
) (FederatedResourceAccessor, error) {
	a := &resourceAccessor{
		limitedScope:            controllerConfig.LimitedScope(),
		typeConfig:              typeConfig,
		fedNamespace:            controllerConfig.FedSystemNamespace,
		fedNamespaceAPIResource: fedNamespaceAPIResource,
		eventRecorder:           eventRecorder,
	}

	targetNamespace := controllerConfig.TargetNamespace

	federatedTypeAPIResource := typeConfig.GetFederatedType()
	federatedTypeClient, err := util.NewResourceClient(controllerConfig.KubeConfig, &federatedTypeAPIResource)
	if err != nil {
		return nil, err
	}
	a.federatedStore, a.federatedController = util.NewResourceInformer(
		federatedTypeClient,
		targetNamespace,
		enqueueObj,
		controllerConfig.Metrics,
	)

	if typeConfig.GetNamespaced() {
		fedNamespaceEnqueue := func(fedNamespaceObj pkgruntime.Object) {
			// When a federated namespace changes, every resource in
			// the namespace needs to be reconciled.
			//
			// TODO Consider optimizing this to only reconcile
			// contained resources in response to a change in
			// placement for the federated namespace.
			namespace := common.NewQualifiedName(fedNamespaceObj).Name
			for _, rawObj := range a.federatedStore.List() {
				obj := rawObj.(pkgruntime.Object)
				qualifiedName := common.NewQualifiedName(obj)
				if qualifiedName.Namespace == namespace {
					enqueueObj(obj)
				}
			}
		}
		// Initialize an informer for federated namespaces.  Placement
		// for a resource is computed as the intersection of resource
		// and federated namespace placement.
		fedNamespaceClient, err := util.NewResourceClient(controllerConfig.KubeConfig, fedNamespaceAPIResource)
		if err != nil {
			return nil, err
		}
		a.fedNamespaceStore, a.fedNamespaceController = util.NewResourceInformer(
			fedNamespaceClient,
			targetNamespace,
			fedNamespaceEnqueue,
			controllerConfig.Metrics,
		)
	}

	a.versionManager = version.NewVersionManager(
		client,
		typeConfig.GetNamespaced(),
		typeConfig.GetFederatedType().Kind,
		typeConfig.GetTargetType().Kind,
		targetNamespace,
	)

	return a, nil
}

func (a *resourceAccessor) Run(stopChan <-chan struct{}) {
	go a.versionManager.Sync(stopChan)
	go a.federatedController.Run(stopChan)
	if a.fedNamespaceController != nil {
		go a.fedNamespaceController.Run(stopChan)
	}
}

func (a *resourceAccessor) HasSynced() bool {
	kind := a.typeConfig.GetFederatedType().Kind
	if !a.versionManager.HasSynced() {
		klog.V(2).Infof("Version manager for %s not synced", kind)
		return false
	}
	if !a.federatedController.HasSynced() {
		klog.V(2).Infof("Informer for %s not synced", kind)
		return false
	}
	if a.fedNamespaceController != nil && !a.fedNamespaceController.HasSynced() {
		klog.V(2).Infof("FederatedNamespace informer for %s not synced", kind)
		return false
	}
	return true
}

func (a *resourceAccessor) FederatedResource(eventSource common.QualifiedName) (FederatedResource, bool, error) {
	if a.typeConfig.GetTargetType().Kind == common.NamespaceKind && a.isSystemNamespace(eventSource.Name) {
		klog.V(7).Infof("Ignoring system namespace %q", eventSource.Name)
		return nil, false, nil
	}

	kind := a.typeConfig.GetFederatedType().Kind

	// Most federated resources have the same name as their targets.
	targetName := common.QualifiedName{
		Namespace: eventSource.Namespace,
		Name:      eventSource.Name,
	}
	federatedName := common.QualifiedName{
		Namespace: util.NamespaceForResource(eventSource.Namespace, a.fedNamespace),
		Name:      eventSource.Name,
	}

	key := federatedName.String()

	resource, err := util.ObjFromCache(a.federatedStore, kind, key)
	if err != nil {
		return nil, false, err
	}
	if resource == nil {
		return nil, true, nil
	}

	var fedNamespace *unstructured.Unstructured
	if a.typeConfig.GetNamespaced() {
		fedNamespaceName := common.QualifiedName{Name: federatedName.Namespace}
		fedNamespace, err = util.ObjFromCache(
			a.fedNamespaceStore,
			a.fedNamespaceAPIResource.Kind,
			fedNamespaceName.String(),
		)
		if err != nil {
			return nil, false, err
		}
		// If fedNamespace is nil, the resources in member clusters
		// will be removed.
	}

	return &federatedResource{
		limitedScope:      a.limitedScope,
		typeConfig:        a.typeConfig,
		targetName:        targetName,
		federatedKind:     kind,
		federatedName:     federatedName,
		federatedResource: resource,
		versionManager:    a.versionManager,
		fedNamespace:      fedNamespace,
		eventRecorder:     a.eventRecorder,
	}, false, nil
}

func (a *resourceAccessor) VisitFederatedResources(visitFunc func(obj interface{})) {
	for _, obj := range a.federatedStore.List() {
		visitFunc(obj)
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
