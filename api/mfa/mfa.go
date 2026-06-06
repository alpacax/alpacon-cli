package mfa

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	mfaURL           = "/api/auth0/mfa"
	mfaCompletionURL = "/api/auth0/mfa/completion/"

	// stepUpPollInterval and stepUpTimeout bound the StepUpForSudo wait. They
	// mirror the websh sudo listener (api/event/sudolistener.go) so the step-up
	// feel is consistent across the two terminals.
	stepUpPollInterval = 500 * time.Millisecond
	stepUpTimeout      = 60 * time.Second
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

// StepUpForSudo runs an interactive MFA step-up for an exec-sudo presence denial
// (SUDO_PRESENCE_REQUIRED). It prints the verification link, opens the browser
// when the user presses Enter, and polls until the server records the MFA
// completion. It returns nil once presence is fresh, or an error on timeout.
//
// The Enter prompt opens the browser as a convenience but never gates progress:
// the poll runs regardless, so a user who opens the link manually still
// completes, and an absent human or a misconfigured agent always terminates at
// the bounded timeout rather than wedging the command. Callers should still gate
// this on an interactive terminal, since scripts, CI, and AI agents never
// receive a presence denial in the first place.
func StepUpForSudo(ac *client.AlpaconClient, serverName string) error {
	// Use the CLI-scoped sudo MFA URL (location=cli) so the mfa-success page
	// notifies the backend, letting CheckMFACompletion observe completion.
	stepUpURL, err := GetMFALinkForSudo(ac, serverName)
	if err != nil {
		return fmt.Errorf("failed to get the MFA step-up link: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nsudo needs a recent MFA to proceed. Open this link to verify:\n%s\n", stepUpURL)
	fmt.Fprintf(os.Stderr, "Press Enter to open it in your browser, or open the link manually.\n")

	// Open the browser on Enter without blocking the poll. EOF or a closed stdin
	// simply skips the auto-open; it never aborts the flow. The goroutine ends at
	// process exit if Enter is never pressed.
	go func() {
		if _, rerr := bufio.NewReader(os.Stdin).ReadString('\n'); rerr == nil {
			utils.OpenBrowser(stepUpURL)
		}
	}()

	spinner := utils.NewSpinner("Waiting for MFA verification...")
	spinner.Start()

	// Mirror the websh sudo listener: a precise deadline plus a fixed-interval
	// ticker (api/event/sudolistener.go).
	deadline := time.After(stepUpTimeout)
	ticker := time.NewTicker(stepUpPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			spinner.Stop()
			return fmt.Errorf("MFA step-up timed out after %v", stepUpTimeout)
		case <-ticker.C:
			completed, cerr := CheckMFACompletion(ac)
			if cerr != nil || !completed {
				// Non-fatal: the endpoint may lag the browser; keep polling.
				continue
			}
			spinner.Stop()
			// Refresh the token so the server sees the updated MFA state on
			// retry, mirroring the websh sudo listener.
			if rerr := ac.RefreshToken(); rerr != nil {
				return fmt.Errorf(
					"failed to refresh token after MFA; run 'alpacon login' to re-authenticate: %w",
					rerr,
				)
			}
			return nil
		}
	}
}

// GetWorkspaceSecurityMFALink returns an MFA URL for workspace security settings.
// Uses location "cli" so the mfa-success page notifies the backend,
// enabling CheckMFACompletion polling to detect when MFA is done.
func GetWorkspaceSecurityMFALink(ac *client.AlpaconClient, workspaceName string) (string, error) {
	params := map[string]string{
		"location":  "cli",
		"workspace": workspaceName,
	}
	responseBody, err := ac.SendGetRequest(utils.BuildURL(mfaURL, "", params))
	if err != nil {
		return "", fmt.Errorf("failed to get the MFA URL: %w", err)
	}

	var mfaResp mfaResponse
	if err := json.Unmarshal(responseBody, &mfaResp); err != nil {
		return "", fmt.Errorf("failed to parse MFA URL response: %w", err)
	}
	if mfaResp.MfaURL == "" {
		return "", fmt.Errorf("MFA URL is empty in server response")
	}

	return mfaResp.MfaURL, nil
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
