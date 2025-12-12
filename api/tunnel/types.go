package tunnel

// TunnelSessionRequest represents the request to create a tunnel session.
// Endpoint: POST /api/websh/tunnels/
type TunnelSessionRequest struct {
	Server     string `json:"server"`               // Server UUID
	Protocol   string `json:"protocol"`             // tcp, ssh, vnc, rdp, postgresql, mysql
	TargetPort int    `json:"target_port"`          // Target port on the remote server
	Username   string `json:"username"`             // Username for the tunnel
	Groupname  string `json:"groupname,omitempty"`  // Groupname (server default: alpacon)
	ClientType string `json:"client_type"`          // cli, web, proxy (default: cli)
}

// TunnelSessionResponse represents the response from creating a tunnel session.
type TunnelSessionResponse struct {
	ID            string `json:"id"`
	ConnectURL  string `json:"connect_url"`
	UserchannelID string `json:"userchannel_id"`
	Server        string `json:"server"`
	Protocol      string `json:"protocol"`
	TargetPort    int    `json:"target_port"`
}
