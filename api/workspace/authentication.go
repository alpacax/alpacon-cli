package workspace

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	// authenticationURL points to the workspace security endpoint which manages
	// authentication settings (MFA requirements, timeout, allowed methods).
	authenticationURL = "/api/workspaces/security/-/"
)

// GetAuthentication retrieves the workspace authentication settings.
func GetAuthentication(ac *client.AlpaconClient) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(authenticationURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve authentication settings: %w", err)
	}

	return responseBody, nil
}

// EditAuthentication retrieves current settings and opens them in an editor for editing.
// Returns the edited data ready to be sent as a PATCH request.
func EditAuthentication(ac *client.AlpaconClient) (any, error) {
	responseBody, err := ac.SendGetRequest(authenticationURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve authentication settings: %w", err)
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// PatchAuthentication sends the edited authentication settings to the server.
// Returns the raw error from SendPatchRequest to preserve error structure for ParseErrorResponse.
func PatchAuthentication(ac *client.AlpaconClient, data any) ([]byte, error) {
	return ac.SendPatchRequest(authenticationURL, data)
}
