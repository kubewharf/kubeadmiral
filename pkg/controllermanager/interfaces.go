/*
Copyright 2016 The Kubernetes Authors.

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

package controllermanager

import (
	"context"
	"fmt"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/healthz"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
)

type Controller interface {
	IsControllerReady() bool
}

func HealthzCheckerAdaptor(name string, controller Controller) healthz.Checker {
	return func(_ *http.Request) error {
		if controller.IsControllerReady() {
			return nil
		}

		return fmt.Errorf("controller %s not ready", name)
	}
}

// startControllerFunc is responsible for constructing and starting a controller. startControllerFunc should be
// asynchronous and an error is only returned if we fail to start the controller.
type StartControllerFunc func(ctx context.Context, controllerCtx *controllercontext.Context) (Controller, error)

type FTCSubControllerInitFuncs struct {
	StartFunc     StartFTCSubControllerFunc
	IsEnabledFunc IsFTCSubControllerEnabledFunc
}

// StartFTCSubControllerFunc is responsible for constructing and starting a FTC subcontroller. A FTC subcontroller is started/stopped
// dynamically for every FTC. StartFTCSubControllerFunc should be asynchronous and an error is only returned if we fail to start the
// controller.
//
//nolint:lll
type StartFTCSubControllerFunc func(ctx context.Context, controllerCtx *controllercontext.Context, typeConfig *fedcorev1a1.FederatedTypeConfig) (Controller, error)

type IsFTCSubControllerEnabledFunc func(typeConfig *fedcorev1a1.FederatedTypeConfig) bool
