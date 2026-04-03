package workspace

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	accessControlURL = "/api/workspaces/access-control/-/"
)

// GetAccessControl retrieves the workspace access control settings.
func GetAccessControl(ac *client.AlpaconClient) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(accessControlURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve access control settings: %w", err)
	}

	return responseBody, nil
}

// EditAccessControl retrieves current settings and opens them in an editor for editing.
// Returns the edited data ready to be sent as a PATCH request.
func EditAccessControl(ac *client.AlpaconClient) (any, error) {
	responseBody, err := ac.SendGetRequest(accessControlURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve access control settings: %w", err)
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// PatchAccessControl sends the edited access control settings to the server.
// Returns the raw error from SendPatchRequest to preserve error structure for ParseErrorResponse.
func PatchAccessControl(ac *client.AlpaconClient, data any) ([]byte, error) {
	return ac.SendPatchRequest(accessControlURL, data)
}
