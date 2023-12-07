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

	genericapiserver "k8s.io/apiserver/pkg/server"

	"github.com/kubewharf/kubeadmiral/cmd/hpa-aggregator/app/options"
)

// Run starts a new HPAAggregatorServer given Options
func Run(ctx context.Context, opts *options.Options) error {
	config, err := opts.Config() //nolint:contextcheck
	if err != nil {
		return err
	}

	server, err := config.Complete().New()
	if err != nil {
		return err
	}

	server.GenericAPIServer.AddPostStartHookOrDie(
		"start-hpa-aggregator-server-informers",
		func(context genericapiserver.PostStartHookContext) error {
			config.GenericConfig.SharedInformerFactory.Start(context.StopCh)
			config.ExtraConfig.FedInformerFactory.Start(ctx.Done())
			config.ExtraConfig.FederatedInformerManager.Start(ctx)
			return nil
		},
	)

	return server.GenericAPIServer.PrepareRun().Run(ctx.Done())
}
