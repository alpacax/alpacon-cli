package kube

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var KubeCmd = &cobra.Command{
	Use:     "kube",
	Aliases: []string{"k8s", "clusters"},
	Short:   "Access Kubernetes clusters",
	Long: `View the Kubernetes clusters registered in the workspace, and log in to one
so kubectl reaches it under a WorkSession that targets the cluster.

Clusters are registered by the agent running inside each cluster, not from
the CLI. This group appears only when the server reports that Kubernetes
support is enabled for the workspace; 'alpacon login' and
'alpacon workspace switch' refresh that answer.`,
	// NoArgs makes an unrecognized subcommand fail as "unknown command"
	// instead of falling through to the help below.
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	KubeCmd.AddCommand(kubeListCmd)
	KubeCmd.AddCommand(kubeDescribeCmd)
	KubeCmd.AddCommand(kubeLoginCmd)
	KubeCmd.AddCommand(kubeConfigCmd)
	KubeCmd.AddCommand(kubeProxyCmd)
}
