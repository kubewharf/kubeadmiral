package app

import (
	"context"

	genericapiserver "k8s.io/apiserver/pkg/server"

	"github.com/kubewharf/kubeadmiral/cmd/hpa-aggregator/app/options"
)

// Run starts a new HPAAggregatorServer given Options
func Run(ctx context.Context, opts *options.Options) error {
	config, err := opts.Config()
	if err != nil {
		return err
	}

	server, err := config.Complete().New()
	if err != nil {
		return err
	}

	server.GenericAPIServer.AddPostStartHookOrDie("start-hpa-aggregator-server-informers", func(context genericapiserver.PostStartHookContext) error {
		config.GenericConfig.SharedInformerFactory.Start(context.StopCh)
		config.ExtraConfig.Run(ctx)
		return nil
	})
	server.GenericAPIServer.AddReadyzChecks()

	return server.GenericAPIServer.PrepareRun().Run(ctx.Done())
}
