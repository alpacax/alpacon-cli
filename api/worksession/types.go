package worksession

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

const (
	StatusPending   = "pending"
	StatusApproved  = "approved"
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusExpired   = "expired"
	StatusRevoked   = "revoked"
	StatusRejected  = "rejected"
	StatusCancelled = "cancelled"
)

// endedStatuses are the statuses after which a work session never runs again.
// A new status also needs a case in the create --wait switch in cmd/worksession.
var endedStatuses = []string{StatusCompleted, StatusExpired, StatusRevoked, StatusRejected, StatusCancelled}

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
	Adjustments       *Adjustments          `json:"adjustments"`
	Recommendations   []Recommendation      `json:"recommendations"`

	// PendingExtensionRequest is the open extension request, nil when there is none. 'extend'
	// answers 202 with it set when the workspace's approval policy holds the request, and later
	// reads keep it set until the request is approved, rejected, cancelled, or lapses.
	PendingExtensionRequest *PendingExtensionRequest `json:"pending_extension_request"`
}

// PendingExtensionRequest is the open request 'extend' filed instead of applying at once.
// ExpiresAt is the request's own deadline, never later than the session's expiry when it was
// filed; WorkSession.ExpiresAt keeps its prior value until the request is decided.
type PendingExtensionRequest struct {
	ID                 string    `json:"id"`
	RequestedExpiresAt time.Time `json:"requested_expires_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	Reason             string    `json:"reason"`
	AddedAt            time.Time `json:"added_at"`
}

// Adjustments is the approver's diff; each dimension is set only when changed, nil when unchanged.
type Adjustments struct {
	Scopes  *ScopeDiff  `json:"scopes,omitempty"`
	Servers *ServerDiff `json:"servers,omitempty"`
}

// ScopeDiff holds the requested (old) and granted (new) scope lists.
type ScopeDiff struct {
	Old []string `json:"old"`
	New []string `json:"new"`
}

// ServerDiff holds the requested (old) and granted (new) server lists.
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
	// Active is "*" on the workspace's active session and empty otherwise;
	// cmd/worksession.MarkActive sets it.
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
	// TargetAccountUsername is the account the work runs as on the servers.
	// Left out, the server decides the account itself.
	TargetAccountUsername string `json:"target_account_username,omitempty"`
}

// SudoPolicyInline is a sudo policy bound to a work session, read from a WorkSession and sent on
// create and update. Commands are the allowed patterns, wildcards permitted. SessionAuthorized lets
// matching sudo run in the approved session without an MFA prompt, the only way a non-interactive
// caller can sudo. There is no Users field: the server binds the policy to the session assignee.
//
// ID is set only on read and on update. An update's sudo_policies is the full desired set: send
// existing policies back with their ID and new ones without, since a policy left out is deleted.
type SudoPolicyInline struct {
	ID                string   `json:"id,omitempty"`
	Commands          []string `json:"commands"`
	Reason            string   `json:"reason,omitempty"`
	SessionAuthorized bool     `json:"session_authorized"`
}

// MarshalJSON sends both names so older servers receive the same policy.
func (p SudoPolicyInline) MarshalJSON() ([]byte, error) {
	type alias SudoPolicyInline
	return json.Marshal(struct {
		alias
		AllowBypassMFA bool `json:"allow_bypass_mfa"`
	}{alias: alias(p), AllowBypassMFA: p.SessionAuthorized})
}

func (p *SudoPolicyInline) UnmarshalJSON(data []byte) error {
	type alias SudoPolicyInline
	wire := struct {
		*alias
		AllowBypassMFA    json.RawMessage `json:"allow_bypass_mfa"`
		SessionAuthorized json.RawMessage `json:"session_authorized"`
	}{alias: (*alias)(p)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	// Presence, rather than truth, decides which name wins.
	if wire.SessionAuthorized != nil {
		return json.Unmarshal(wire.SessionAuthorized, &p.SessionAuthorized)
	}
	if wire.AllowBypassMFA != nil {
		return json.Unmarshal(wire.AllowBypassMFA, &p.SessionAuthorized)
	}
	return nil
}

// WorkSessionUpdateRequest is a partial update: omitempty leaves unset fields untouched.
// SudoPolicies replaces the whole set, so existing policies must be sent back or they are deleted;
// an empty slice is omitted, so this request cannot clear policies.
type WorkSessionUpdateRequest struct {
	Title        string             `json:"title,omitempty"`
	Description  string             `json:"description,omitempty"`
	Scopes       []string           `json:"scopes,omitempty"`
	Servers      []string           `json:"servers,omitempty"`
	StartsAt     string             `json:"starts_at,omitempty"`
	ExpiresAt    string             `json:"expires_at,omitempty"`
	SudoPolicies []SudoPolicyInline `json:"sudo_policies,omitempty"`
}

// WorkSessionExtendRequest is the payload for the extend action. The server requires Reason
// (work_session_extension_reason_required otherwise); it is omitted when empty so a caller without
// --reason gets that refusal rather than an empty string sent.
type WorkSessionExtendRequest struct {
	ExpiresAt string `json:"expires_at"`
	Reason    string `json:"reason,omitempty"`
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

// RecordChunk is one entry of the per-session recording route's cursor envelope.
type RecordChunk struct {
	AddedAt string `json:"added_at"`
	Record  string `json:"record"`
}

func EndedStatuses() []string {
	return append([]string(nil), endedStatuses...)
}

func IsEndedStatus(status string) bool {
	return slices.Contains(endedStatuses, status)
}
