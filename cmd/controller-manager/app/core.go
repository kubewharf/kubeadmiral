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

package app

import (
	"context"
	"fmt"

	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/pkg/controllermanager"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/automigration"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federate"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federatedcluster"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federatedhpa"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/follower"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/mcs"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/nsautoprop"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/policyrc"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/status"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/statusaggregator"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/sync"
)

func startFederateController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	federateController, err := federate.NewFederateController(
		controllerCtx.KubeClientset,
		controllerCtx.DynamicClientset,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.InformerManager,
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
		controllerCtx.FedSystemNamespace,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating federate controller: %w", err)
	}

	go federateController.Run(ctx)

	return federateController, nil
}

func startPolicyRCController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	policyRCController, err := policyrc.NewPolicyRCController(
		controllerCtx.RestConfig,
		controllerCtx.FedInformerFactory,
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating policyRC controller: %w", err)
	}

	go policyRCController.Run(ctx)

	return policyRCController, nil
}

func startOverridePolicyController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	overrideController, err := override.NewOverridePolicyController(
		controllerCtx.KubeClientset,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory,
		controllerCtx.InformerManager,
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating override controller: %w", err)
	}

	go overrideController.Run(ctx)

	return overrideController, nil
}

func startNamespaceAutoPropagationController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	nsAutoPropController, err := nsautoprop.NewNamespaceAutoPropagationController(
		controllerCtx.KubeClientset,
		controllerCtx.InformerManager,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedClusters(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.KubeInformerFactory.Core().V1().Namespaces(),
		controllerCtx.ComponentConfig.NSAutoPropExcludeRegexp,
		controllerCtx.FedSystemNamespace,
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating namespace auto propagation controller: %w", err)
	}

	go nsAutoPropController.Run(ctx)

	return nsAutoPropController, nil
}

func startStatusController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	statusController, err := status.NewStatusController(
		controllerCtx.KubeClientset,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().CollectedStatuses(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterCollectedStatuses(),
		controllerCtx.InformerManager,
		controllerCtx.FederatedInformerManager,
		controllerCtx.ClusterAvailableDelay,
		controllerCtx.ClusterUnavailableDelay,
		controllerCtx.ComponentConfig.MemberObjectEnqueueDelay,
		klog.Background(),
		controllerCtx.WorkerCount,
		controllerCtx.Metrics,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating sync controller: %w", err)
	}

	go statusController.Run(ctx)

	return statusController, nil
}

func startFederatedClusterController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	federatedClusterController, err := federatedcluster.NewFederatedClusterController(
		controllerCtx.KubeClientset,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedClusters(),
		controllerCtx.FederatedInformerManager,
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.ComponentConfig.ClusterJoinTimeout,
		controllerCtx.WorkerCount,
		controllerCtx.FedSystemNamespace,
		controllerCtx.ComponentConfig.ResourceAggregationNodeFilter,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating federate controller: %w", err)
	}

	go federatedClusterController.Run(ctx)

	return federatedClusterController, nil
}

func startScheduler(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	scheduler, err := scheduler.NewScheduler(
		controllerCtx.KubeClientset,
		controllerCtx.FedClientset,
		controllerCtx.DynamicClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().PropagationPolicies(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterPropagationPolicies(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedClusters(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().SchedulingProfiles(),
		controllerCtx.InformerManager,
		controllerCtx.FedInformerFactory.Core().V1alpha1().SchedulerPluginWebhookConfigurations(),
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating scheduler: %w", err)
	}

	go scheduler.Run(ctx)

	return scheduler, nil
}

func startSyncController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	syncController, err := sync.NewSyncController(
		controllerCtx.KubeClientset,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.InformerManager,
		controllerCtx.FederatedInformerManager,
		controllerCtx.FedSystemNamespace,
		controllerCtx.TargetNamespace,
		controllerCtx.ClusterAvailableDelay,
		controllerCtx.ClusterUnavailableDelay,
		controllerCtx.ComponentConfig.MemberObjectEnqueueDelay,
		klog.Background(),
		controllerCtx.WorkerCount,
		controllerCtx.CascadingDeletionWorkerCount,
		controllerCtx.Metrics,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating sync controller: %w", err)
	}

	go syncController.Run(ctx)

	return syncController, nil
}

func startFollowerController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	followerController, err := follower.NewFollowerController(
		controllerCtx.KubeClientset,
		controllerCtx.FedClientset,
		controllerCtx.InformerManager,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedTypeConfigs(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating follower controller: %w", err)
	}

	go followerController.Run(ctx)

	return followerController, nil
}

func startAutoMigrationController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	autoMigrationController, err := automigration.NewAutoMigrationController(
		ctx,
		controllerCtx.KubeClientset,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.FederatedInformerManager,
		controllerCtx.InformerManager,
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating auto-migration controller: %w", err)
	}

	go autoMigrationController.Run(ctx)

	return autoMigrationController, nil
}

func startStatusAggregatorController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	statusAggregatorController, err := statusaggregator.NewStatusAggregatorController(
		controllerCtx.KubeClientset,
		controllerCtx.DynamicClientset,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.FederatedInformerManager,
		controllerCtx.InformerManager,
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
		controllerCtx.ClusterAvailableDelay,
		controllerCtx.ClusterUnavailableDelay,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating status-aggregator controller: %w", err)
	}

	go statusAggregatorController.Run(ctx)

	return statusAggregatorController, nil
}

func startFederatedHPAController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	federatedHPAController, err := federatedhpa.NewFederatedHPAController(
		controllerCtx.KubeClientset,
		controllerCtx.FedClientset,
		controllerCtx.DynamicClientset,
		controllerCtx.InformerManager,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().PropagationPolicies(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterPropagationPolicies(),
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating federated-hpa controller: %w", err)
	}

	go federatedHPAController.Run(ctx)

	return federatedHPAController, nil
}

func startServiceExportController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	serviceExportController, err := mcs.NewServiceExportController(
		controllerCtx.KubeClientset,
		controllerCtx.KubeInformerFactory.Discovery().V1beta1().EndpointSlices(),
		controllerCtx.FederatedInformerManager,
		klog.Background(),
		controllerCtx.Metrics,
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating serviceexport controller: %w", err)
	}

	go serviceExportController.Run(ctx)

	return serviceExportController, nil
}

func startServiceImportController(
	ctx context.Context,
	controllerCtx *controllercontext.Context,
) (controllermanager.Controller, error) {
	serviceImportController, err := mcs.NewServiceImportController(
		controllerCtx.KubeClientset,
		controllerCtx.KubeInformerFactory.Discovery().V1beta1().EndpointSlices(),
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.Metrics,
		klog.Background(),
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating serviceimport controller: %w", err)
	}

	go serviceImportController.Run(ctx)

	return serviceImportController, nil
}
