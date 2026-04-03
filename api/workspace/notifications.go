package workspace

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	notificationsURL = "/api/workspaces/notifications/"
)

// GetNotifications retrieves the workspace notification settings.
func GetNotifications(ac *client.AlpaconClient) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(notificationsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve notification settings: %w", err)
	}

	return responseBody, nil
}

// UpdateNotifications opens the current notification settings in an editor and sends the changes.
func UpdateNotifications(ac *client.AlpaconClient) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(notificationsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve notification settings: %w", err)
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(notificationsURL, data)
	if err != nil {
		return nil, fmt.Errorf("failed to update notification settings: %w", err)
	}

	return responseBody, nil
}
