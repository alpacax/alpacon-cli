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
func CreateTunnelSession(ac *client.AlpaconClient, serverName, protocol string, targetPort int) (*TunnelSessionResponse, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to get server ID: %w", err)
	}

	request := TunnelSessionRequest{
		Server:     serverID,
		Protocol:   protocol,
		TargetPort: targetPort,
		ClientType: "cli",
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
