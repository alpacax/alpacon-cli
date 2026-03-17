package websh

import (
	"net/http"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/gorilla/websocket"
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
	ID        string `json:"ID"`
	Server    string `json:"Server"`
	User      string `json:"User"`
	Username  string `json:"Username"`
	RemoteIP  string `json:"Remote IP"`
	AddedAt   string `json:"Added At"`
	ClosedAt  string `json:"Closed At"`
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
