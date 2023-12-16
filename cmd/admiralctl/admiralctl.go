package main

import (
	"github.com/kubewharf/kubeadmiral/pkg/admiralctl"
	"k8s.io/component-base/cli"
	"k8s.io/kubectl/pkg/cmd/util"
)

func main() {
	cmd := admiralctl.NewDefaultAdmiralctlCommand()
	if err := cli.RunNoErrOutput(cmd); err != nil {
		util.CheckErr(err)
	}
}
