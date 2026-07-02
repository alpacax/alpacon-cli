package worksession

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type WorkSession struct {
	ID                string                `json:"id"`
	Title             string                `json:"title"`
	Description       string                `json:"description"`
	Status            string                `json:"status"`
	RequesterType     string                `json:"requester_type"`
	Scopes            []string              `json:"scopes"`
	Servers           []types.ServerSummary `json:"servers"`
	CreatedBy         *types.UserSummary    `json:"created_by"`
	AssignedUser      *types.UserSummary    `json:"assigned_user"`
	ApprovalRequestID string                `json:"approval_request_id"`
	StartsAt          *time.Time            `json:"starts_at"`
	ExpiresAt         time.Time             `json:"expires_at"`
	StartedAt         *time.Time            `json:"started_at"`
	CompletedAt       *time.Time            `json:"completed_at"`
	AddedAt           time.Time             `json:"added_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	SudoPolicies      []SudoPolicyInline    `json:"sudo_policies"`
	Adjustments       *Adjustments          `json:"adjustments,omitempty"`
	Recommendations   []Recommendation      `json:"recommendations,omitempty"`
}

// Adjustments is the approver's diff applied at approval time. Each dimension
// is present only when the approver changed it; a nil Adjustments means no change.
type Adjustments struct {
	Scopes  *ScopeDiff  `json:"scopes,omitempty"`
	Servers *ServerDiff `json:"servers,omitempty"`
}

type ScopeDiff struct {
	Old []string `json:"old"`
	New []string `json:"new"`
}

type ServerDiff struct {
	Old []types.ServerSummary `json:"old"`
	New []types.ServerSummary `json:"new"`
}

// Recommendation is an admin-confirmed note attached at approval time.
type Recommendation struct {
	ID            string `json:"id"`
	Text          string `json:"text"`
	Severity      string `json:"severity"`
	Source        string `json:"source"`
	AutoCheckable bool   `json:"auto_checkable"`
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
	Description   string             `json:"description"`
	RequesterType string             `json:"requester_type"`
	Scopes        []string           `json:"scopes"`
	Servers       []string           `json:"servers"`
	ExpiresAt     string             `json:"expires_at"`
	SudoPolicies  []SudoPolicyInline `json:"sudo_policies,omitempty"`
}

// SudoPolicyInline is a sudo policy bound to a work session. It serves three
// roles: read (when returned on a WorkSession), create (attached at session
// create time), and modify (sent on update). Commands are the allowed patterns
// (wildcards permitted); AllowBypassMFA lets matching sudo run without
// interactive MFA, which is the only way a non-interactive caller (e.g. an AI
// agent running 'exec') can sudo. Users is intentionally omitted — the
// server binds the policy to the session assignee automatically so it never
// applies to other workspace users.
//
// ID is set only on read and on modify: the sudo_policies field of an update
// PATCH is the FULL desired set, so existing policies must be echoed back with
// their ID (modify in place) alongside new entries without an ID (additions).
// Omitting an existing policy from the set deletes it, so callers must preserve
// the current entries.
type SudoPolicyInline struct {
	ID             string   `json:"id,omitempty"`
	Commands       []string `json:"commands"`
	Reason         string   `json:"reason,omitempty"`
	AllowBypassMFA bool     `json:"allow_bypass_mfa"`
}

// WorkSessionUpdateRequest is a partial update: omitempty leaves unset fields
// untouched. SudoPolicies is PUT-style — the server replaces the whole set, so
// existing policies must be echoed back or they are deleted. An empty slice is
// also omitted (omitempty), so this request cannot clear policies.
type WorkSessionUpdateRequest struct {
	Title        string             `json:"title,omitempty"`
	Description  string             `json:"description,omitempty"`
	Scopes       []string           `json:"scopes,omitempty"`
	Servers      []string           `json:"servers,omitempty"`
	StartsAt     string             `json:"starts_at,omitempty"`
	ExpiresAt    string             `json:"expires_at,omitempty"`
	SudoPolicies []SudoPolicyInline `json:"sudo_policies,omitempty"`
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
	Username   string  `json:"username"`
	Groupname  string  `json:"groupname"`
	ClosedAt   *string `json:"closed_at"`
	ClientType string  `json:"client_type"`

	// command
	Shell       string  `json:"shell"`
	Line        string  `json:"line"`
	Success     *bool   `json:"success"`
	Denied      bool    `json:"denied"`
	ElapsedTime float64 `json:"elapsed_time"`

	// tunnel_session
	IsTunnel   bool `json:"is_tunnel"`
	TargetPort *int `json:"target_port"`

	// file_upload / file_download
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

type TimelineAttributes struct {
	Time    string `json:"timestamp" table:"Time"`
	Type    string `json:"type"      table:"Type"`
	Server  string `json:"server"    table:"Server"`
	User    string `json:"user"      table:"User"`
	Details string `json:"details"   table:"Details"`
}
