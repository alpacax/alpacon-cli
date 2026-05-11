package tunnel

import (
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
)

const (
	tunnelSessionURL = "/api/websh/tunnels/"
)

// CreateTunnelSession creates a new tunnel session for the specified server.
// It returns the WebSocket URL to connect to.
// workSessionID, when non-empty, attaches the tunnel to a work-session via the
// "work_session" body field (omitted when empty thanks to omitempty).
func CreateTunnelSession(ac *client.AlpaconClient, serverName, username, groupname string, targetPort int, workSessionID string) (*TunnelSessionResponse, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to get server ID: %w", err)
	}

	request := TunnelSessionRequest{
		Server:      serverID,
		TargetPort:  targetPort,
		Username:    username,
		Groupname:   groupname,
		ClientType:  "cli",
		WorkSession: workSessionID,
	}

	responseBody, err := ac.SendPostRequest(tunnelSessionURL, request)
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel session: %w", err)
	}

	var response TunnelSessionResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse tunnel session response: %w", err)
	}

	return &response, nil
}
