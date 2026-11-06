package kubernetes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	clusterURL = "/api/kubernetes/clusters/"
	sessionURL = "/api/kubernetes/sessions/"
	// iamGroupURL is duplicated from api/iam so this package can build a lean
	// UUID→name projection without pulling in the full iam.GroupResponse type.
	iamGroupURL = "/api/iam/groups/"
)

// Session statuses: a 'kube sessions' row shows one, and the list filter takes
// the same values.
const (
	SessionStatusOpen   = "open"
	SessionStatusClosed = "closed"
)

// SurfaceNotEnabledMessage is what the CLI prints when the workspace does
// not expose the Kubernetes surface.
const SurfaceNotEnabledMessage = "Kubernetes support is not enabled on this workspace. If it was enabled recently, run 'alpacon login' again to refresh."

// IsSurfaceOff reports whether err carries a 404, which the server answers for
// every path under /api/kubernetes/ when the Kubernetes surface is off.
func IsSurfaceOff(err error) bool {
	return utils.HTTPStatusCode(err) == http.StatusNotFound
}

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

	pathID, err := api.RequireResponseID(result.ID, "cluster")
	if err != nil {
		return "", err
	}
	return pathID.String(), nil
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
	pathID, err := api.RequireID(clusterID, "cluster")
	if err != nil {
		return nil, err
	}
	return ac.SendGetRequest(api.IDURL(clusterURL, pathID, "", nil))
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

// CreateSession opens a proxy session to a cluster. The channel token appears
// only in this response—the server never returns it again—so the caller must
// persist it at once.
func CreateSession(ac *client.AlpaconClient, req KubeSessionCreateRequest) (*KubeSessionResponse, error) {
	body, err := ac.SendPostRequest(sessionURL, req)
	if err != nil {
		return nil, err
	}

	var session KubeSessionResponse
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, err
	}
	pathID, err := api.RequireResponseID(session.ID, "kube session")
	if err != nil {
		return nil, fmt.Errorf("the server created the kube session, but its ID is unusable (%w)", err)
	}
	session.ID = pathID.String()

	return &session, nil
}

// GetSession reads a proxy session back, to learn whether it is still open
// and when its token expires now.
func GetSession(ac *client.AlpaconClient, sessionID string) (*KubeSessionDetail, error) {
	pathID, err := api.RequireID(sessionID, "kube session")
	if err != nil {
		return nil, err
	}
	body, err := ac.SendGetRequest(api.IDURL(sessionURL, pathID, "", nil))
	if err != nil {
		return nil, err
	}
	var session KubeSessionDetail
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// CloseSession ends a proxy session.
func CloseSession(ac *client.AlpaconClient, sessionID string) error {
	pathID, err := api.RequireID(sessionID, "kube session")
	if err != nil {
		return err
	}
	_, err = ac.SendPostRequest(api.IDURL(sessionURL, pathID, "close", nil), struct{}{})
	return err
}

// GetSessionList returns the kube sessions the caller can see, newest first:
// its own and those of clusters it manages. An empty clusterID or status leaves
// that filter off; clusterID is an ID, not a name.
func GetSessionList(ac *client.AlpaconClient, clusterID, status string) ([]KubeSessionListItem, error) {
	params := map[string]string{}
	if clusterID != "" {
		params["cluster"] = clusterID
	}
	if status != "" {
		params["status"] = status
	}
	return api.FetchAllPages[KubeSessionListItem](ac, sessionURL, params)
}

// GetClusterNames maps the IDs of the clusters the caller can see to their names.
func GetClusterNames(ac *client.AlpaconClient) (map[string]string, error) {
	clusters, err := api.FetchAllPages[clusterSummary](ac, clusterURL, nil)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(clusters))
	for _, cluster := range clusters {
		names[cluster.ID] = cluster.Name
	}
	return names, nil
}

// ProjectSessions builds the 'kube sessions' rows, with the expiry shown in loc.
// A cluster missing from names shows its ID, and the status reads closed_at
// alone: a session whose token has expired stays open until the server's sweep
// closes it.
func ProjectSessions(sessions []KubeSessionListItem, names map[string]string, loc *time.Location) []KubeSessionAttributes {
	rows := make([]KubeSessionAttributes, 0, len(sessions))
	for _, session := range sessions {
		row := KubeSessionAttributes{
			ID:             session.ID,
			Cluster:        session.Cluster,
			ClusterID:      session.Cluster,
			Status:         SessionStatusOpen,
			TokenExpiredAt: session.TokenExpiredAt,
			ClosedAt:       session.ClosedAt,
		}
		if name := names[session.Cluster]; name != "" {
			row.Cluster = name
		}
		if session.User != nil {
			row.User = session.User.Name
			if row.User == "" {
				row.User = session.User.Email
			}
		}
		if session.ClosedAt != nil {
			row.Status = SessionStatusClosed
		}
		if session.TokenExpiredAt != nil {
			row.ExpiresAt = session.TokenExpiredAt.In(loc).Format("2006-01-02 15:04")
		}
		rows = append(rows, row)
	}
	return rows
}
