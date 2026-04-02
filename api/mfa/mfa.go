package mfa

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	mfaURL           = "/api/auth0/mfa"
	mfaCompletionURL = "/api/auth0/mfa/completion/"
)

type mfaResponse struct {
	MfaURL string `json:"mfa_url"`
}

type mfaCompletionResponse struct {
	Completed bool `json:"completed"`
}

func HandleMFAError(ac *client.AlpaconClient, serverName string) error {

	cfg, err := config.LoadConfig()
	if err != nil {
		utils.CliErrorWithExit("Failed to load configuration: %s.", err)
	}

	serverID, _ := server.GetServerIDByName(ac, serverName)
	mfaURL, err := GetMFALink(ac, serverID, cfg.WorkspaceName)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nMFA authentication required. Please visit:\n%s\n\n", mfaURL)
	utils.OpenBrowser(mfaURL)

	return nil
}

func CheckMFACompletion(ac *client.AlpaconClient) (bool, error) {
	responseBody, err := ac.SendGetRequest(mfaCompletionURL)
	if err != nil {
		return false, fmt.Errorf("failed to check MFA completion: %w", err)
	}

	var resp mfaCompletionResponse
	if err := json.Unmarshal(responseBody, &resp); err != nil {
		return false, fmt.Errorf("failed to parse MFA completion response: %w", err)
	}

	return resp.Completed, nil
}

// GetMFALinkForSudo resolves the server name and returns a CLI MFA URL.
// Used by the sudo MFA listener where only the server name is available.
func GetMFALinkForSudo(ac *client.AlpaconClient, serverName string) (string, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load configuration: %w", err)
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve server: %w", err)
	}

	return GetMFALink(ac, serverID, cfg.WorkspaceName)
}

func GetMFALink(ac *client.AlpaconClient, serverID string, workspaceName string) (string, error) {
	params := map[string]string{
		"location":  "cli",
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
