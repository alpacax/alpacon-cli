package websh

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/gorilla/websocket"
	"net/http"
	"time"
)

type WebsocketClient struct {
	Header http.Header
	conn   *websocket.Conn
	Done   chan error
}

type SessionRequest struct {
	Rows      int    `json:"rows"`
	Cols      int    `json:"cols"`
	Server    string `json:"server"` // server id
	Username  string `json:"username"`
	Groupname string `json:"groupname"`
}

type SessionResponse struct {
	ID           string          `json:"id"`
	Rows         int             `json:"rows"`
	Cols         int             `json:"cols"`
	Server       string          `json:"server"`
	User         iam.UserSummary `json:"user"`
	UserName     string          `json:"user_name"`
	GroupName    string          `json:"group_name"`
	UserAgent    string          `json:"user_agent"`
	RemoteIP     string          `json:"remote_ip"`
	WebsocketURL string          `json:"websocket_url"`
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
