package workspace

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/client"
)

const (
	// mfaMethodsURL points to the endpoint that returns available MFA methods
	// for the workspace (a read-only subset of the security settings).
	mfaMethodsURL = "/api/workspaces/security/-/mfa-methods/"
)

// GetMFAMethods retrieves the available MFA methods for the workspace.
func GetMFAMethods(ac *client.AlpaconClient) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(mfaMethodsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve MFA methods: %w", err)
	}

	return responseBody, nil
}
