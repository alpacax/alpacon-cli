package kubernetes

import (
	"encoding/json"
	"fmt"
	"time"
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
