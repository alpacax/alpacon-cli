package workspace

import (
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	claimNamespace = "https://alpacon.io/"
)

// GetWorkspacesFromToken decodes the JWT access token and extracts the workspaces claim.
func GetWorkspacesFromToken(accessToken string) ([]Workspace, error) {
	claims, err := utils.DecodeJWTPayload(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode access token: %v", err)
	}

	claimKey := claimNamespace + "workspaces"
	rawWorkspaces, ok := claims[claimKey]
	if !ok {
		return nil, fmt.Errorf("workspaces claim not found in token")
	}

	// Re-marshal and unmarshal to convert from any to []Workspace
	data, err := json.Marshal(rawWorkspaces)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal workspaces claim: %v", err)
	}

	var workspaces []Workspace
	if err := json.Unmarshal(data, &workspaces); err != nil {
		return nil, fmt.Errorf("failed to parse workspaces claim: %v", err)
	}

	return workspaces, nil
}

// GetWorkspaceList returns a display-ready list of workspaces, marking the current one.
func GetWorkspaceList(cfg config.Config) ([]WorkspaceListEntry, error) {
	workspaces, err := GetWorkspacesFromToken(cfg.AccessToken)
	if err != nil {
		return nil, err
	}

	currentName := cfg.WorkspaceName
	var entries []WorkspaceListEntry
	for _, ws := range workspaces {
		current := ""
		if ws.SchemaName == currentName {
			current = "*"
		}
		entries = append(entries, WorkspaceListEntry{
			Name:    ws.SchemaName,
			Region:  ws.Region,
			Current: current,
		})
	}

	return entries, nil
}

// ResolveWorkspaceURL finds the target workspace in the JWT and builds its full URL.
func ResolveWorkspaceURL(accessToken, targetName, baseDomain string) (newURL, newName string, err error) {
	workspaces, err := GetWorkspacesFromToken(accessToken)
	if err != nil {
		return "", "", err
	}

	for _, ws := range workspaces {
		if ws.SchemaName == targetName {
			newURL = fmt.Sprintf("https://%s.%s.%s", ws.SchemaName, ws.Region, baseDomain)
			return newURL, ws.SchemaName, nil
		}
	}

	return "", "", fmt.Errorf("workspace %q not found in your account", targetName)
}

// ValidateAndBuildWorkspaceURL finds the target workspace in the JWT and builds its full URL.
func ValidateAndBuildWorkspaceURL(cfg config.Config, targetName string) (newURL, newName string, err error) {
	return ResolveWorkspaceURL(cfg.AccessToken, targetName, cfg.BaseDomain)
}
