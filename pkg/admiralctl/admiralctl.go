package admiralctl

import (
	"flag"
	"fmt"
	"os"

	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/federalize"
	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/join"
	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/options"
	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/util"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	apiserverflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	"k8s.io/kubectl/pkg/util/templates"
)

var (
	cliName      = "admiralctl"
	rootCmdShort = "%s controls the Kubernetes cluster federation manager."
	// defaultConfigFlags It composes the set of values necessary for obtaining a REST client config with default values set.
	defaultConfigFlags = genericclioptions.NewConfigFlags(true).
				WithDeprecatedPasswordFlag().WithDiscoveryBurst(300).WithDiscoveryQPS(50.0)
)

// NewDefaultAdmiralctlCommand creates the `admiralctl` command.
func NewDefaultAdmiralctlCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   cliName,
		Short: fmt.Sprintf(rootCmdShort, cliName),
		RunE:  runHelp,
	}

	// Init log flags
	klog.InitFlags(flag.CommandLine)

	// Add the command line flags from other dependencies (e.g., klog), but do not
	// warn if they contain underscores.
	pflag.CommandLine.SetNormalizeFunc(apiserverflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	flags := rootCmd.PersistentFlags()
	flags.AddFlagSet(pflag.CommandLine)
	addKubeConfigFlags(flags)

	// From this point and forward we get warnings on flags that contain "_" separators
	// when adding them with hyphen instead of the original name.
	rootCmd.SetGlobalNormalizationFunc(apiserverflag.WarnWordSepNormalizeFunc)

	// Prevent klog errors about logging before parsing.
	_ = flag.CommandLine.Parse(nil)
	f := util.NewFactory(defaultConfigFlags)

	ioStreams := genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
	groups := templates.CommandGroups{
		{
			Message: "Resource Management Commands:",
			Commands: []*cobra.Command{
				federalize.NewCmdFederalize(f, cliName),
				join.NewCmdJoin(f, cliName),
			},
		},
	}
	groups.Add(rootCmd)
	filters := []string{"options"}
	rootCmd.AddCommand(options.NewCmdOptions(cliName, ioStreams.Out))
	templates.ActsAsRootCommand(rootCmd, filters, groups...)

	return rootCmd
}

// addKubeConfigFlags adds flags to the specified FlagSet.
func addKubeConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(defaultConfigFlags.KubeConfig, "kubeconfig", *defaultConfigFlags.KubeConfig,
		"Path to the kubeconfig file to use for CLI requests.")
	flags.StringVar(defaultConfigFlags.Context, "kubeadmiral-context", *defaultConfigFlags.Context,
		"The name of the kubeconfig context to use")
}

func runHelp(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}
