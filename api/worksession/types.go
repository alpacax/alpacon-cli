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

// TimelineItem represents a single event in a work session's activity timeline.
// All event types share this struct; type-specific fields are zero-valued for other types.
type TimelineItem struct {
	Type      string  `json:"type"`
	Timestamp *string `json:"timestamp"`
	ID        string  `json:"id"`
	ServerID  *string `json:"server_id"`

	// shared across session-like types (websh_session, tunnel_session, ftp_session)
	Username  string  `json:"username"`
	Groupname string  `json:"groupname"`
	ClosedAt  *string `json:"closed_at"`
	ClientType string `json:"client_type"`

	// command
	Shell       string   `json:"shell"`
	Line        string   `json:"line"`
	Success     *bool    `json:"success"`
	Denied      bool     `json:"denied"`
	ElapsedTime float64  `json:"elapsed_time"`

	// tunnel_session
	IsTunnel   bool  `json:"is_tunnel"`
	TargetPort *int  `json:"target_port"`

	// file_upload / file_download (path may be string or []string — rendered as-is after JSON decode)
	Name string `json:"name"`
	Size int64  `json:"size"`

	// sudo_grant
	GrantType string  `json:"grant_type"`
	Status    string  `json:"status"`
	Command   *string `json:"command"`
	OneTime   bool    `json:"one_time"`

	// websh_record
	SessionID    string `json:"session_id"`
	MaskedRecord string `json:"masked_record"`
}

type TimelineResponse struct {
	Results []TimelineItem `json:"results"`
}

type TimelineAttributes struct {
	Time    string `json:"timestamp" table:"Time"`
	Type    string `json:"type"      table:"Type"`
	Server  string `json:"server"    table:"Server"`
	User    string `json:"user"      table:"User"`
	Details string `json:"details"   table:"Details"`
}
