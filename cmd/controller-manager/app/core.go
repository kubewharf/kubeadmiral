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
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/federate"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/nsautoprop"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/override"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/policyrc"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/status"
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
