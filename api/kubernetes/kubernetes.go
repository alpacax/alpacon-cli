package kubernetes

import (
	"encoding/json"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	clusterURL = "/api/kubernetes/clusters/"
	// iamGroupURL is duplicated from api/iam so this package can build a lean
	// UUID→name projection without pulling in the full iam.GroupResponse type.
	iamGroupURL = "/api/iam/groups/"
)

// GetClusterList returns every cluster the caller can see, projected for 'kube ls'.
// A service token sees none: the server answers it with an empty list.
func GetClusterList(ac *client.AlpaconClient) ([]ClusterAttributes, error) {
	clusters, err := api.FetchAllPages[ClusterDetail](ac, clusterURL, nil)
	if err != nil {
		return nil, err
	}

	var clusterList []ClusterAttributes
	for _, cluster := range clusters {
		clusterList = append(clusterList, toAttributes(cluster))
	}

	return clusterList, nil
}

func toAttributes(cluster ClusterDetail) ClusterAttributes {
	return ClusterAttributes{
		Name:         cluster.Name,
		Provider:     cluster.Provider,
		KubeVersion:  Optional[string]{Value: cluster.KubeVersion},
		NodeCount:    Optional[int]{Value: cluster.NodeCount},
		Connected:    cluster.IsConnected,
		Enabled:      cluster.Enabled,
		AgentVersion: cluster.AgentVersion,
	}
}

// GetClusterIDByName resolves a cluster name to its ID through the server's
// exact-match name filter.
func GetClusterIDByName(ac *client.AlpaconClient, clusterName string) (string, error) {
	result, err := api.ResolveByName[ClusterDetail](ac, api.ResolveByNameOptions{
		Endpoint:    clusterURL,
		FilterKey:   "name",
		Name:        clusterName,
		BlankMsg:    "cluster name is required",
		NotFoundMsg: "no cluster found with the given name",
	})
	if err != nil {
		return "", err
	}

	return result.ID, nil
}

// GetClusterDetail fetches one cluster by ID.
func GetClusterDetail(ac *client.AlpaconClient, clusterID string) (*ClusterDetail, error) {
	body, err := GetClusterDetailRaw(ac, clusterID)
	if err != nil {
		return nil, err
	}

	var cluster ClusterDetail
	if err := json.Unmarshal(body, &cluster); err != nil {
		return nil, err
	}

	return &cluster, nil
}

// GetClusterDetailRaw fetches one cluster by ID and returns the body as the server sent it.
func GetClusterDetailRaw(ac *client.AlpaconClient, clusterID string) ([]byte, error) {
	return ac.SendGetRequest(utils.BuildURL(clusterURL, clusterID, nil))
}

// ResolveGroupNames maps group UUIDs to names in one batched lookup. A UUID
// with no match, or every UUID when the lookup fails, is shown as-is so the
// caller's output is never blocked on the group list.
func ResolveGroupNames(ac *client.AlpaconClient, groupIDs []string) []string {
	if len(groupIDs) == 0 {
		return nil
	}

	groups, err := api.FetchAllPages[groupSummary](ac, iamGroupURL, nil)
	if err != nil {
		utils.CliWarning("Could not resolve group names; showing UUIDs instead: %s", err)
		return groupIDs
	}

	nameByID := make(map[string]string, len(groups))
	for _, g := range groups {
		nameByID[g.ID] = g.Name
	}

	names := make([]string, 0, len(groupIDs))
	for _, id := range groupIDs {
		if name, ok := nameByID[id]; ok && name != "" {
			names = append(names, name)
			continue
		}
		names = append(names, id)
	}

	return names
}
