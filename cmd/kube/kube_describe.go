package kube

import (
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type describeRow struct {
	Field string `table:"Field"`
	Value string `table:"Value"`
}

var kubeDescribeCmd = &cobra.Command{
	Use:     "describe CLUSTER",
	Aliases: []string{"desc"},
	Short:   "Show details of a Kubernetes cluster",
	Long: `Show the details of one cluster: provider, versions, node count,
connectivity, groups, and tags.

Use --output json to see the full raw response, with group IDs in place of
names and status and tags as the server stores them.`,
	Example: `  alpacon kube describe prod-cluster
  alpacon kube desc prod-cluster --output json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		clusterID, err := kubernetes.GetClusterIDByName(ac, args[0])
		if err != nil {
			// Only the list endpoint's 404 means the surface is off; a 404 on
			// the detail below is a cluster deleted since it was resolved.
			if isSurfaceOff(err) {
				exitNotEnabled()
			}
			utils.CliErrorWithExit("Failed to retrieve the cluster: %s.", err)
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			body, err := kubernetes.GetClusterDetailRaw(ac, clusterID)
			if err != nil {
				utils.CliErrorWithExit("Failed to retrieve the cluster: %s.", err)
			}
			utils.PrintJson(body)
			return
		}

		cluster, err := kubernetes.GetClusterDetail(ac, clusterID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the cluster: %s.", err)
		}

		utils.PrintTable(describeRows(cluster, kubernetes.ResolveGroupNames(ac, cluster.Groups)))
	},
}

func describeRows(cluster *kubernetes.ClusterDetail, groupNames []string) []describeRow {
	return []describeRow{
		{"ID", cluster.ID},
		{"Name", cluster.Name},
		{"Provider", cluster.Provider},
		{"Kube version", kubernetes.Optional[string]{Value: cluster.KubeVersion}.String()},
		{"Agent version", cluster.AgentVersion},
		{"Nodes", kubernetes.Optional[int]{Value: cluster.NodeCount}.String()},
		{"Connected", strconv.FormatBool(cluster.IsConnected)},
		{"Enabled", strconv.FormatBool(cluster.Enabled)},
		{"Status", formatJSONValue(cluster.Status)},
		{"Groups", strings.Join(groupNames, ", ")},
		{"Tags", formatTags(cluster.Tags)},
		{"Last connectivity", formatTime(cluster.LastConnectivity)},
		{"Last synced at", formatTime(cluster.LastSyncedAt)},
		{"Added at", formatTime(&cluster.AddedAt)},
		{"Updated at", formatTime(&cluster.UpdatedAt)},
	}
}
