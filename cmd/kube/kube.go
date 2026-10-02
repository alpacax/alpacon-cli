package kube

import (
	"errors"

	"github.com/spf13/cobra"
)

var KubeCmd = &cobra.Command{
	Use:     "kube",
	Aliases: []string{"k8s", "clusters"},
	Short:   "View Kubernetes clusters",
	Long: `View the Kubernetes clusters registered in the workspace.

Clusters are registered by the agent running inside each cluster, not from
the CLI. This group appears only when the server reports that Kubernetes
support is enabled for the workspace; 'alpacon login' and
'alpacon workspace switch' refresh that answer.

Subcommands:
  ls        List clusters
  describe  Show details of a cluster`,
	// NoArgs makes an unrecognized subcommand fail as "unknown command"
	// instead of falling through to the help below.
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cmd.Help(); err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon kube ls' or 'alpacon kube describe'. Run 'alpacon kube --help' for more information")
	},
}

func init() {
	KubeCmd.AddCommand(kubeListCmd)
	KubeCmd.AddCommand(kubeDescribeCmd)
}
