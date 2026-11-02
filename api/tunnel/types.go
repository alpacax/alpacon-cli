package tunnel

import "github.com/alpacax/alpacon-cli/api/types"

type TunnelSessionRequest struct {
	Server      string `json:"server"` // Server UUID
	TargetPort  int    `json:"target_port"`
	Username    string `json:"username"`
	Groupname   string `json:"groupname"`
	ClientType  string `json:"client_type"`            // Ignored; the server reads User-Agent
	WorkSession string `json:"work_session,omitempty"` // Work-session UUID; omitted when empty
}

type TunnelSessionResponse struct {
	ID            string              `json:"id"`
	WebsocketURL  string              `json:"websocket_url"`
	UserChannelID string              `json:"userchannel_id"`
	Server        types.ServerSummary `json:"server"`
	TargetPort    int                 `json:"target_port"`
}

// OpenStatus is the state of a tunnel session's open request,
// empty from a server that predates it.
type OpenStatus string

const (
	OpenStatusPending OpenStatus = "pending"
	OpenStatusOpened  OpenStatus = "opened"
	OpenStatusFailed  OpenStatus = "failed"
)

// TunnelSessionDetail is the part of a tunnel session's detail this CLI reads.
type TunnelSessionDetail struct {
	OpenStatus  OpenStatus  `json:"open_status"`
	OpenFailure OpenFailure `json:"open_failure"`
}

// OpenFailure is the outcome of the command that failed to open the tunnel.
type OpenFailure struct {
	Result string `json:"result"`
}
