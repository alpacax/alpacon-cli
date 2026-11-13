package kubernetes

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

// ClusterDetail decodes one entry of /api/kubernetes/clusters/. Status and Tags
// stay raw: the server stores both as free-form JSON, and status has no enum.
// KubeVersion and NodeCount are nil until the agent has measured them, which
// is not the same as an empty version or zero nodes.
type ClusterDetail struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Provider         string          `json:"provider"`
	KubeVersion      *string         `json:"kube_version"`
	AgentVersion     string          `json:"agent_version"`
	NodeCount        *int            `json:"node_count"`
	Status           json.RawMessage `json:"status"`
	Enabled          bool            `json:"enabled"`
	Groups           []string        `json:"groups"`
	Tags             json.RawMessage `json:"tags"`
	IsConnected      bool            `json:"is_connected"`
	LastConnectivity *time.Time      `json:"last_connectivity"`
	LastSyncedAt     *time.Time      `json:"last_synced_at"`
	AddedAt          time.Time       `json:"added_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// ClusterAttributes is the 'kube ls' row. Its version and node count keep the
// server's names and types, so a script reading --output json sees node_count
// as a number and a value the agent has not reported yet as null; the table
// shows that as blank. Connected follows ServerAttributes, not is_connected.
type ClusterAttributes struct {
	Name         string           `json:"name"`
	Provider     string           `json:"provider"`
	KubeVersion  Optional[string] `json:"kube_version" table:"Kube Version"`
	NodeCount    Optional[int]    `json:"node_count" table:"Nodes"`
	Connected    bool             `json:"connected"`
	Enabled      bool             `json:"enabled"`
	AgentVersion string           `json:"agent_version" table:"Agent Version"`
}

// Optional is a value the server may send as null. PrintTable formats a cell
// with %v, which would print a bare pointer's address, so the pointer is
// wrapped: String renders null as blank and MarshalJSON keeps it null.
type Optional[T any] struct {
	Value *T
}

func (o Optional[T]) String() string {
	if o.Value == nil {
		return ""
	}
	return fmt.Sprint(*o.Value)
}

func (o Optional[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.Value)
}

type groupSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// clusterSummary is the part of a cluster that names it, decoded alone so a
// change in another field cannot fail the name lookup.
type clusterSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ClientTypeCLI is the client_type a session opened from this CLI declares.
const ClientTypeCLI = "cli"

// KubeSessionCreateRequest opens a proxy session to one cluster. Cluster and
// WorkSession are UUIDs; WorkSession is left out when neither --work-session
// nor ALPACON_WORK_SESSION names a session.
type KubeSessionCreateRequest struct {
	Cluster     string `json:"cluster"`
	WorkSession string `json:"work_session,omitempty"`
	ClientType  string `json:"client_type"`
}

// KubeSessionResponse is the session the server opened. ChannelToken is the
// credential kubectl presents to ProxyURL until TokenExpiredAt.
// RBACDriftWarning is set when the session opened despite cluster RBAC
// bindings outside Alpacon. It stays raw so --output json carries it as the
// server sent it and a shape this build does not expect cannot fail the open.
type KubeSessionResponse struct {
	ID               string          `json:"id"`
	Cluster          string          `json:"cluster"`
	ChannelID        string          `json:"channel_id"`
	ChannelToken     string          `json:"channel_token"`
	TokenExpiredAt   time.Time       `json:"token_expired_at"`
	ProxyURL         string          `json:"proxy_url"`
	RBACDriftWarning json.RawMessage `json:"rbac_drift_warning"`
}

// RBACDriftWarning is the part of the server's rbac_drift_warning the CLI
// shows: its cluster's RBAC check at the open, a snapshot that a later cleanup
// of the cluster does not change. The two counts are permissions beyond
// Alpacon's baseline that bindings Alpacon did not make grant the session
// identity, through ClusterRoleBindings (in every namespace) and through
// RoleBindings (once per namespace). Incomplete means the check did not
// finish, so the counts are a lower bound. The other fields stay out, so a
// value the CLI never reads cannot make the counts unreadable; --output json
// carries the whole object.
type RBACDriftWarning struct {
	ClusterExternalGrants    int  `json:"cluster_external_grants"`
	NamespacedExternalGrants int  `json:"namespaced_external_grants"`
	Incomplete               bool `json:"incomplete"`
}

// KubeSessionDetail is a kube session as it is read back. It never carries
// the channel token. TokenExpiredAt follows an extended work session, and
// ClosedAt is set once the session has ended for any reason.
type KubeSessionDetail struct {
	ID             string     `json:"id"`
	TokenExpiredAt *time.Time `json:"token_expired_at"`
	ClosedAt       *time.Time `json:"closed_at"`
}

// KubeSessionUser is the person a listed kube session belongs to.
type KubeSessionUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// KubeSessionListItem is one entry of the session list. It is apart from
// KubeSessionDetail so a change in these fields cannot fail the read
// 'kube credentials' makes on every kubectl call.
//
// LatestExec is the newest exec, attach or port-forward stream the session
// opened, {id, kind}, or null when it opened none. RBACDriftWarning is null
// unless the session opened with a warning. An older server leaves either
// out. Both stay raw so a shape this build does not expect cannot fail the
// list.
type KubeSessionListItem struct {
	ID               string           `json:"id"`
	Cluster          string           `json:"cluster"`
	User             *KubeSessionUser `json:"user"`
	TokenExpiredAt   *time.Time       `json:"token_expired_at"`
	LatestExec       json.RawMessage  `json:"latest_exec"`
	RBACDriftWarning json.RawMessage  `json:"rbac_drift_warning"`
	ClosedAt         *time.Time       `json:"closed_at"`
}

// KubeSessionAttributes is the 'kube sessions' row. The table shows the expiry
// in local time; --output json carries it, both raw times and the cluster's ID, all times in UTC.
// Kind is one of the SessionKind labels or the kind of the session's newest stream.
// A session that opened with an RBAC drift warning shows its external grants
// in the table; --output json carries the server's warning object as it came,
// or null.
type KubeSessionAttributes struct {
	ID               string          `json:"id" table:"ID"`
	Cluster          string          `json:"cluster" table:"Cluster"`
	ClusterID        string          `json:"cluster_id" table:"-"`
	User             string          `json:"user" table:"User"`
	Status           string          `json:"status" table:"Status"`
	Kind             string          `json:"kind" table:"Kind"`
	ExpiresAt        utils.Timestamp `json:"expires_at" table:"Expires At"`
	TokenExpiredAt   utils.Timestamp `json:"token_expired_at" table:"-"`
	ClosedAt         utils.Timestamp `json:"closed_at" table:"-"`
	RBACDriftWarning json.RawMessage `json:"rbac_drift_warning" table:"-"`
	ExternalGrants   string          `json:"-" table:"External grants"`
}
