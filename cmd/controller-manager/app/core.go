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
