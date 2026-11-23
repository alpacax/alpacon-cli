package kube

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type clientFactory func() (*client.AlpaconClient, error)

func NewKubeCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "kube",
		Aliases: []string{"k8s", "clusters"},
		Short:   "Access Kubernetes clusters",
		Long: `View the Kubernetes clusters registered in the workspace, and log in to one
so kubectl reaches it under a WorkSession that targets the cluster. When the
workspace requires a recent MFA for writes and deletes, 'alpacon kube mfa'
confirms it.

Clusters are registered by the agent running inside each cluster, not from
the CLI. This group appears only when the server reports that Kubernetes
support is enabled for the workspace; 'alpacon login' and
'alpacon workspace switch' refresh that answer.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newKubeListCmd(newClient, ios),
		newKubeDescribeCmd(newClient, ios),
		newKubeLoginCmd(newClient, ios),
		newKubeLogoutCmd(newClient, ios),
		newKubeSessionsCmd(newClient, ios),
		newKubeCredentialsCmd(newClient, ios),
		newKubeConfigCmd(newClient, ios),
		newKubeMFACmd(newClient, ios),
		newKubeProxyCmd(ios),
	)
	return cmd
}
