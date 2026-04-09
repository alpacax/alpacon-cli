package workspace

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	preferencesURL = "/api/workspaces/preferences/-/"
)

// GetPreferences retrieves the workspace preferences.
func GetPreferences(ac *client.AlpaconClient) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(preferencesURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve workspace preferences: %w", err)
	}

	return responseBody, nil
}

// UpdatePreferences opens the current preferences in an editor and sends the changes.
func UpdatePreferences(ac *client.AlpaconClient) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(preferencesURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve workspace preferences: %w", err)
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(preferencesURL, data)
	if err != nil {
		return nil, fmt.Errorf("failed to update workspace preferences: %w", err)
	}

	return responseBody, nil
}
