package worksession

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type WorkSession struct {
	ID            string                `json:"id"`
	Description   string                `json:"description"`
	Status        string                `json:"status"`
	RequesterType string                `json:"requester_type"`
	Scopes        []string              `json:"scopes"`
	Servers       []types.ServerSummary `json:"servers"`
	CreatedBy     *types.UserSummary    `json:"created_by"`
	AssignedUser  *types.UserSummary    `json:"assigned_user"`
	ExpiresAt     time.Time             `json:"expires_at"`
	StartedAt     *time.Time            `json:"started_at"`
	CompletedAt   *time.Time            `json:"completed_at"`
	AddedAt       time.Time             `json:"added_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

type WorkSessionAttributes struct {
	// Active is the active-session marker ("*" for the workspace's active session, empty otherwise).
	// Decorated by cmd/worksession.MarkActive; hidden from JSON output via the "-" tag.
	Active      string `json:"-"           table:"Active"`
	ID          string `json:"id"          table:"ID"`
	Status      string `json:"status"      table:"Status"`
	Scopes      string `json:"scopes"      table:"Scopes"`
	Servers     string `json:"servers"     table:"Servers"`
	ExpiresAt   string `json:"expires_at"  table:"Expires At"`
	Description string `json:"description" table:"Description"`
}

type WorkSessionCreateRequest struct {
	Description   string   `json:"description"`
	RequesterType string   `json:"requester_type"`
	Scopes        []string `json:"scopes"`
	Servers       []string `json:"servers"`
	ExpiresAt     string   `json:"expires_at"`
}

type WorkSessionExtendRequest struct {
	ExpiresAt string `json:"expires_at"`
}

type WorkSessionApproveRequest struct {
	AdjustedScopes  []string `json:"adjusted_scopes,omitempty"`
	AdjustedServers []string `json:"adjusted_servers,omitempty"`
}
