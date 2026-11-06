package kube

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var KubeCmd = newKubeCmd(client.NewAlpaconAPIClient)

// clientFactory builds the workspace client a command calls the server with.
type clientFactory func() (*client.AlpaconClient, error)

func newKubeCmd(newClient clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "kube",
		Aliases: []string{"k8s", "clusters"},
		Short:   "Access Kubernetes clusters",
		Long: `View the Kubernetes clusters registered in the workspace, and log in to one
so kubectl reaches it under a WorkSession that targets the cluster.

Clusters are registered by the agent running inside each cluster, not from
the CLI. This group appears only when the server reports that Kubernetes
support is enabled for the workspace; 'alpacon login' and
'alpacon workspace switch' refresh that answer.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newKubeListCmd(newClient),
		newKubeDescribeCmd(newClient),
		newKubeLoginCmd(newClient),
		newKubeLogoutCmd(),
		newKubeSessionsCmd(newClient),
		newKubeCredentialsCmd(),
		newKubeConfigCmd(newClient),
		newKubeProxyCmd(),
	)
	return cmd
}
