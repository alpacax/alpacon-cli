package kube

import (
	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var kubeListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List Kubernetes clusters",
	Long: `List the Kubernetes clusters registered in the workspace, with each
cluster's provider, Kubernetes version, node count, and whether its agent is
connected. A version or node count the agent has not reported yet is blank.`,
	Example: `  alpacon kube ls
  alpacon k8s ls --output json`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		clusterList, err := kubernetes.GetClusterList(ac)
		if err != nil {
			if isSurfaceOff(err) {
				exitNotEnabled()
			}
			utils.CliErrorWithExit("Failed to retrieve the clusters: %s.", err)
		}

		utils.PrintTable(clusterList)
	},
}
