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

	// "k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	// "github.com/kubewharf/kubeadmiral/pkg/client/generic"
	"github.com/kubewharf/kubeadmiral/pkg/controllermanager"
	// "github.com/kubewharf/kubeadmiral/pkg/controllers/automigration"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federate"
	// "github.com/kubewharf/kubeadmiral/pkg/controllers/federatedcluster"
	// "github.com/kubewharf/kubeadmiral/pkg/controllers/federatedtypeconfig"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/follower"
	// "github.com/kubewharf/kubeadmiral/pkg/controllers/monitor"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
	// schemautil "github.com/kubewharf/kubeadmiral/pkg/controllers/util/schema"
)

// func startFederatedClusterController(ctx context.Context, controllerCtx *controllercontext.Context) (controllermanager.Controller, error) {
// 	clusterController, err := federatedcluster.NewFederatedClusterController(
// 		controllerCtx.FedClientset,
// 		controllerCtx.KubeClientset,
// 		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedClusters(),
// 		controllerCtx.FederatedClientFactory,
// 		controllerCtx.Metrics,
// 		controllerCtx.FedSystemNamespace,
// 		controllerCtx.RestConfig,
// 		controllerCtx.WorkerCount,
// 		controllerCtx.ComponentConfig.ClusterJoinTimeout,
// 	)
// 	if err != nil {
// 		return nil, fmt.Errorf("error creating federated cluster controller: %w", err)
// 	}

// 	go clusterController.Run(ctx)

// 	return clusterController, nil
// }

func startFederateController(ctx context.Context, controllerCtx *controllercontext.Context) (controllermanager.Controller, error) {
	federateController, err := federate.NewFederateController(
		controllerCtx.KubeClientset,
		controllerCtx.DynamicClientset,
		controllerCtx.FedClientset,
		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedObjects(),
		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterFederatedObjects(),
		controllerCtx.InformerManager,
		controllerCtx.Metrics,
		controllerCtx.WorkerCount,
		controllerCtx.FedSystemNamespace,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating federate controller: %w", err)
	}

	go federateController.Run(ctx)

	return federateController, nil
}

//func startMonitorController(ctx context.Context, controllerCtx *controllercontext.Context) (controllermanager.Controller, error) {
//	controllerConfig := controllerConfigFromControllerContext(controllerCtx)
//	//nolint:contextcheck
//	monitorController, err := monitor.NewMonitorController(controllerConfig)
//	if err != nil {
//		return nil, fmt.Errorf("error creating monitor controller: %w", err)
//	}

//	if err = monitorController.Run(ctx.Done()); err != nil {
//		return nil, err
//	}

//	return monitorController, nil
//}

//nolint:contextcheck
func startFollowerController(ctx context.Context, controllerCtx *controllercontext.Context) (controllermanager.Controller, error) {
	controller, err := follower.NewFollowerController(
		controllerCtx.KubeClientset,
		controllerCtx.DynamicClientset,
		controllerCtx.FedClientset,
		controllerCtx.DynamicInformerFactory,
		controllerCtx.Metrics,
		controllerCtx.WorkerCount,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating follower controller: %w", err)
	}

	go controller.Run(ctx.Done())

	return controller, nil
}

// TODO: remove this function once all controllers are fully refactored
func controllerConfigFromControllerContext(controllerCtx *controllercontext.Context) *util.ControllerConfig {
	return &util.ControllerConfig{
		FederationNamespaces: util.FederationNamespaces{
			FedSystemNamespace: controllerCtx.FedSystemNamespace,
			TargetNamespace:    controllerCtx.TargetNamespace,
		},
		KubeConfig:                            controllerCtx.RestConfig,
		ClusterAvailableDelay:                 controllerCtx.ClusterAvailableDelay,
		ClusterUnavailableDelay:               controllerCtx.ClusterUnavailableDelay,
		SkipAdoptingResources:                 true,
		WorkerCount:                           controllerCtx.WorkerCount,
		NamespaceAutoPropagationExcludeRegexp: controllerCtx.ComponentConfig.NSAutoPropExcludeRegexp,
		CreateCrdForFtcs:                      controllerCtx.ComponentConfig.FederatedTypeConfigCreateCRDsForFTCs,
		Metrics:                               controllerCtx.Metrics,
	}
}

// func startGlobalScheduler(
// 	ctx context.Context,
// 	controllerCtx *controllercontext.Context,
// 	typeConfig *fedcorev1a1.FederatedTypeConfig,
// ) (controllermanager.Controller, error) {
// 	federatedAPIResource := typeConfig.GetFederatedType()
// 	federatedGVR := schemautil.APIResourceToGVR(&federatedAPIResource)

// 	scheduler, err := scheduler.NewScheduler(
// 		klog.FromContext(ctx),
// 		typeConfig,
// 		controllerCtx.KubeClientset,
// 		controllerCtx.FedClientset,
// 		controllerCtx.DynamicClientset,
// 		controllerCtx.DynamicInformerFactory.ForResource(federatedGVR),
// 		controllerCtx.FedInformerFactory.Core().V1alpha1().PropagationPolicies(),
// 		controllerCtx.FedInformerFactory.Core().V1alpha1().ClusterPropagationPolicies(),
// 		controllerCtx.FedInformerFactory.Core().V1alpha1().FederatedClusters(),
// 		controllerCtx.FedInformerFactory.Core().V1alpha1().SchedulingProfiles(),
// 		controllerCtx.FedInformerFactory.Core().V1alpha1().SchedulerPluginWebhookConfigurations(),
// 		controllerCtx.Metrics,
// 		controllerCtx.WorkerCount,
// 	)
// 	if err != nil {
// 		return nil, fmt.Errorf("error creating global scheduler: %w", err)
// 	}

// 	go scheduler.Run(ctx)

// 	return scheduler, nil
// }

func isGlobalSchedulerEnabled(typeConfig *fedcorev1a1.FederatedTypeConfig) bool {
	for _, controllerGroup := range typeConfig.GetControllers() {
		for _, controller := range controllerGroup {
			if controller == scheduler.PrefixedGlobalSchedulerName {
				return true
			}
		}
	}
	return false
}

//func startAutoMigrationController(
//	ctx context.Context,
//	controllerCtx *controllercontext.Context,
//	typeConfig *fedcorev1a1.FederatedTypeConfig,
//) (controllermanager.Controller, error) {
//	genericClient, err := generic.New(controllerCtx.RestConfig)
//	if err != nil {
//		return nil, fmt.Errorf("error creating generic client: %w", err)
//	}

//	federatedAPIResource := typeConfig.GetFederatedType()
//	federatedGVR := schemautil.APIResourceToGVR(&federatedAPIResource)

//	//nolint:contextcheck
//	controller, err := automigration.NewAutoMigrationController(
//		controllerConfigFromControllerContext(controllerCtx),
//		typeConfig,
//		genericClient,
//		controllerCtx.KubeClientset,
//		controllerCtx.DynamicClientset.Resource(federatedGVR),
//		controllerCtx.DynamicInformerFactory.ForResource(federatedGVR),
//	)
//	if err != nil {
//		return nil, fmt.Errorf("error creating auto-migration controller: %w", err)
//	}

//	go controller.Run(ctx)

//	return controller, nil
//}

func isAutoMigrationControllerEnabled(typeConfig *fedcorev1a1.FederatedTypeConfig) bool {
	return typeConfig.Spec.AutoMigration != nil && typeConfig.Spec.AutoMigration.Enabled
}
