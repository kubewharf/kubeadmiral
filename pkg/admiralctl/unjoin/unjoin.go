package unjoin

import (
	"fmt"

	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/util"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/kubectl/pkg/util/templates"
)

var (
	unjoinLongDesc = templates.LongDesc(`
	Unjoin a FederatedCluster from Kubeadmiral federation. 

	If the the federated cluster has not joined the federation, this command will do nothing.
	`)

	unjoinExample = templates.Examples(`
		# Unjoin cluster1 from Kubeadmiral federation
		%[1]s unjoin cluster1
	`)
)

// CommandUnjoinOption holds all command options for unjoin
type CommandUnjoinOption struct {
}

// AddFlags adds flags for a specified FlagSet
func (o *CommandUnjoinOption) AddFlags(flags *pflag.FlagSet) {

}

func NewCmdJoin(f util.Factory, parentCommand string) *cobra.Command {
	o := CommandUnjoinOption{}

	cmd := &cobra.Command{
		Use:                   "unjoin <FCLUSTER_NAME>",
		Short:                 "unjoin the FederatedCluster from Kubeadmiral federation",
		Long:                  unjoinLongDesc,
		Example:               fmt.Sprintf(unjoinExample, parentCommand),
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	flag := cmd.Flags()
	o.AddFlags(flag)

	return cmd
}
