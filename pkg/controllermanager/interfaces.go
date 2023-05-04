package controllermanager

import (
	"context"
	"fmt"
	"net/http"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	controllercontext "github.com/kubewharf/kubeadmiral/pkg/controllers/context"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
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
