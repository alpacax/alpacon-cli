package websh

import (
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/gorilla/websocket"
)

type WebsocketClient struct {
	header http.Header
	// conn is the connection currently serving the session, replaced by each
	// reconnect. Only the goroutine running the session touches it.
	conn       *connection
	done       chan struct{} // closed once the first outcome is recorded
	err        error
	finishOnce sync.Once
	// reconnect carries a dropped session to a new connection. nil on a client
	// that ends the session on the first drop, which is every non-interactive one.
	reconnect *reconnector
}

// connection is one WebSocket connection of a session. A session outlives its
// connections—a dropped link is re-dialed onto a new user channel—so each one
// records its own ending and the session decides what that ending means.
type connection struct {
	ws      *websocket.Conn
	ended   chan struct{} // closed once the first ending is recorded
	err     error
	endOnce sync.Once
	pumps   sync.WaitGroup
}

// reconnector is what an interactive session needs to survive a dropped
// connection: a new user channel to dial, and the terminal size to re-send on it.
type reconnector struct {
	// provision issues a new user channel on the same session and returns its
	// WebSocket URL.
	provision func() (string, error)
	// resendSize re-sends the terminal size, which is what makes the remote shell
	// redraw at the right width once the new connection is up.
	resendSize func() error
	// notice is where the reconnect line is printed, os.Stderr in production.
	notice io.Writer
	// baseDelay is the first backoff step, lowered by tests so a reconnect
	// assertion does not have to sit through the production delay.
	baseDelay   time.Duration
	maxAttempts int
}

type SessionRequest struct {
	Rows        int    `json:"rows"`
	Cols        int    `json:"cols"`
	Server      string `json:"server"` // server id
	Username    string `json:"username"`
	Groupname   string `json:"groupname"`
	WorkSession string `json:"work_session,omitempty"`
}

// SessionSizeRequest updates a session's terminal size. The server resizes the
// remote PTY on every update, so re-sending an unchanged size is what makes the
// shell redraw after a reconnect.
type SessionSizeRequest struct {
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

type SessionResponse struct {
	ID           string              `json:"id"`
	Rows         int                 `json:"rows"`
	Cols         int                 `json:"cols"`
	Server       types.ServerSummary `json:"server"`
	User         types.UserSummary   `json:"user"`
	Username     string              `json:"username"`
	Groupname    string              `json:"groupname"`
	UserAgent    string              `json:"user_agent"`
	RemoteIP     string              `json:"remote_ip"`
	WebsocketURL string              `json:"websocket_url"`
}

type ShareResponse struct {
	SharedURL  string    `json:"shared_url"`
	Password   string    `json:"password"`
	ReadOnly   bool      `json:"read_only"`
	Expiration time.Time `json:"expiration"`
}

type ShareRequest struct {
	ReadOnly bool `json:"read_only"`
}

type JoinRequest struct {
	Password string `json:"password"`
}

type SessionListItem struct {
	ID       string `json:"id"        table:"ID"`
	Server   string `json:"server"    table:"Server"`
	User     string `json:"user"      table:"User"`
	Username string `json:"username"  table:"Username"`
	RemoteIP string `json:"remote_ip" table:"Remote IP"`
	AddedAt  string `json:"added_at"  table:"Added At"`
	ClosedAt string `json:"closed_at" table:"Closed At"`
}

type SessionDetailResponse struct {
	ID         string              `json:"id"`
	Rows       int                 `json:"rows"`
	Cols       int                 `json:"cols"`
	Server     types.ServerSummary `json:"server"`
	User       types.UserSummary   `json:"user"`
	Username   string              `json:"username"`
	Groupname  string              `json:"groupname"`
	UserAgent  string              `json:"user_agent"`
	RemoteIP   string              `json:"remote_ip"`
	IsTunnel   bool                `json:"is_tunnel"`
	ClientType string              `json:"client_type"`
	AddedAt    string              `json:"added_at"`
	UpdatedAt  string              `json:"updated_at"`
	ClosedAt   *string             `json:"closed_at"`
	Success    bool                `json:"success"`
}

type InviteRequest struct {
	Emails   []string `json:"emails"`
	ReadOnly bool     `json:"read_only"`
}

type ConnectRequest struct {
	Session  string `json:"session"`
	IsMaster bool   `json:"is_master"`
	ReadOnly bool   `json:"read_only"`
}

type SessionRecord struct {
	AddedAt string `json:"added_at" table:"Added At"`
	Record  string `json:"record"   table:"Record"`
}
