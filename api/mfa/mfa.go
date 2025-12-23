package mfa

import (
	"encoding/json"
	"fmt"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	mfaURL = "/api/auth0/mfa"
)

type mfaResponse struct {
	MfaURL string `json:"mfa_url"`
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

	fmt.Println("\n==================== AUTHENTICATION REQUIRED ====================")
	fmt.Println("\nPlease authenticate by visiting the following URL:")
	fmt.Printf("%s\n\n", mfaURL)
	fmt.Print("===============================================================\n\n")

	return nil
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
