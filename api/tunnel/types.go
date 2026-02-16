package tunnel

import "github.com/alpacax/alpacon-cli/api/server"

type TunnelSessionRequest struct {
	Server     string `json:"server"`      // Server UUID
	TargetPort int    `json:"target_port"` // Target port on the remote server
	Username   string `json:"username"`    // Username for the tunnel
	Groupname  string `json:"groupname"`
	ClientType string `json:"client_type"` // cli, web, proxy (default: cli)
}

type TunnelSessionResponse struct {
	ID            string `json:"id"`
	WebsocketURL  string `json:"websocket_url"`
	UserchannelID string `json:"userchannel_id"`
	Server        server.ServerInfo `json:"server"`
	TargetPort    int    `json:"target_port"`
}
