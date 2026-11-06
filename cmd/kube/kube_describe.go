package kube

import (
	"strconv"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type describeRow struct {
	Field string `table:"Field"`
	Value string `table:"Value"`
}

func newKubeDescribeCmd(newClient clientFactory) *cobra.Command {
	return &cobra.Command{
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
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			ac, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			clusterID, err := kubernetes.GetClusterIDByName(ac, args[0])
			if err != nil {
				// Only the list endpoint's 404 means the surface is off; a 404 on
				// the detail below is a cluster deleted since it was resolved.
				if kubernetes.IsSurfaceOff(err) {
					return errNotEnabled()
				}
				return utils.NewExitError("Failed to retrieve the cluster: %s.", err)
			}

			if utils.OutputFormat == utils.OutputFormatJSON {
				body, err := kubernetes.GetClusterDetailRaw(ac, clusterID)
				if err != nil {
					return utils.NewExitError("Failed to retrieve the cluster: %s.", err)
				}
				utils.PrintJson(body)
				return nil
			}

			cluster, err := kubernetes.GetClusterDetail(ac, clusterID)
			if err != nil {
				return utils.NewExitError("Failed to retrieve the cluster: %s.", err)
			}

			utils.PrintTable(describeRows(cluster, kubernetes.ResolveGroupNames(ac, cluster.Groups), time.Local))
			return nil
		}),
	}
}

func describeRows(cluster *kubernetes.ClusterDetail, groupNames []string, zone *time.Location) []describeRow {
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
		{"Last connectivity", formatTime(cluster.LastConnectivity, zone)},
		{"Last synced at", formatTime(cluster.LastSyncedAt, zone)},
		{"Added at", formatTime(&cluster.AddedAt, zone)},
		{"Updated at", formatTime(&cluster.UpdatedAt, zone)},
	}
}
