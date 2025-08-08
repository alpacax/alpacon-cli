package mfa

import (
	"encoding/json"
	"fmt"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	mfaURL = "/api/auth0/mfa"
)

type mfaResponse struct {
	MfaURL string `json:"mfa_url"`
}

func GetMFALink(ac *client.AlpaconClient, serverID string, workspaceName string) (string, error) {
	params := map[string]string{
		"location":  "command",
		"server":    serverID,
		"workspace": workspaceName,
	}
	responseBody, err := ac.SendGetRequest(utils.BuildURL(mfaURL, "", params))
	if err != nil {
		return "", fmt.Errorf("failed to get the MFA URL: %w", err)
	}

	var mfaResp mfaResponse
	_ = json.Unmarshal(responseBody, &mfaResp)

	return mfaResp.MfaURL, nil
}
