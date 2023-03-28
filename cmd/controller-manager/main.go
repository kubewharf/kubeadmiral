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

package main

import (
	"context"
	_ "net/http/pprof"
	"os"

	"github.com/spf13/pflag"
	"k8s.io/klog/v2"

	"github.com/kubewharf/kubeadmiral/cmd/controller-manager/app"
	"github.com/kubewharf/kubeadmiral/cmd/controller-manager/app/options"
	"github.com/kubewharf/kubeadmiral/pkg/controllermanager/signals"
)

func main() {
	opts := options.NewOptions()
	flags := pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	opts.AddFlags(flags, app.KnownControllers(), app.ControllersDisabledByDefault())

	flags.Parse(os.Args[1:])
	flags.VisitAll(func(f *pflag.Flag) {
		klog.Infof("Flag: %v=%v", f.Name, f.Value.String())
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-signals.SetupSignalHandler()
		cancel()
	}()

	app.Run(ctx, opts)
}
