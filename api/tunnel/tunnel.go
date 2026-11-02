package tunnel

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
)

const (
	tunnelSessionURL = "/api/websh/tunnels/"
)

// CreateTunnelSession creates a tunnel session for the server and returns the
// WebSocket URL to connect to. A non-empty workSessionID attaches the tunnel to
// that work session.
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

// GetTunnelSession fetches a tunnel session's detail, sessionID being the ID CreateTunnelSession returned.
func GetTunnelSession(ctx context.Context, ac *client.AlpaconClient, sessionID string) (*TunnelSessionDetail, error) {
	pathID, err := api.RequireResponseID(sessionID, "tunnel session")
	if err != nil {
		return nil, err
	}
	responseBody, err := ac.SendGetRequestWithContext(ctx, api.IDURL(tunnelSessionURL, pathID, "", nil))
	if err != nil {
		return nil, fmt.Errorf("failed to get tunnel session: %w", err)
	}

	var detail TunnelSessionDetail
	if err := json.Unmarshal(responseBody, &detail); err != nil {
		return nil, fmt.Errorf("failed to parse tunnel session detail: %w", err)
	}
	return &detail, nil
}
