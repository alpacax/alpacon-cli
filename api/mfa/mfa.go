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

// mfaCompletionResponse is the step-up probe's verdict at each presence tier.
//
// CompletedSensitive is a pointer so an absent field is distinguishable from a
// reported false: a server predating the two-tier split reports Completed and
// omits CompletedSensitive, and a caller that needs the sensitive verdict must
// fall back to Completed rather than poll forever against a field that server
// will never send.
type mfaCompletionResponse struct {
	Completed          bool  `json:"completed"`
	CompletedSensitive *bool `json:"completed_sensitive"`
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

// CheckMFACompletion reports whether presence is fresh enough for an ordinary
// action. Callers gated at the sensitive tier—privilege elevation and security
// settings—must use CheckSensitiveMFACompletion instead.
func CheckMFACompletion(ac *client.AlpaconClient) (bool, error) {
	resp, err := fetchMFACompletion(ac)
	if err != nil {
		return false, err
	}

	return resp.Completed, nil
}

// CheckSensitiveMFACompletion reports whether presence is fresh enough for a
// sensitive-tier action.
//
// The sensitive window is the shorter one, so asking at the ordinary tier
// returns true while the gate that follows still rejects—the poll then stops
// before the human has finished the browser prompt, and the action fails on a
// step-up that had in fact not completed yet. Retrying succeeds, which is what
// made this read as "the first sudo always fails".
//
// Against a server predating the two-tier split the field is absent and this
// falls back to the ordinary verdict: that server applies one window to both
// tiers, so the two verdicts are the same answer there.
func CheckSensitiveMFACompletion(ac *client.AlpaconClient) (bool, error) {
	resp, err := fetchMFACompletion(ac)
	if err != nil {
		return false, err
	}

	if resp.CompletedSensitive != nil {
		return *resp.CompletedSensitive, nil
	}

	return resp.Completed, nil
}

func fetchMFACompletion(ac *client.AlpaconClient) (mfaCompletionResponse, error) {
	var resp mfaCompletionResponse

	responseBody, err := ac.SendGetRequest(mfaCompletionURL)
	if err != nil {
		return resp, fmt.Errorf("failed to check MFA completion: %w", err)
	}

	if err := json.Unmarshal(responseBody, &resp); err != nil {
		return resp, fmt.Errorf("failed to parse MFA completion response: %w", err)
	}

	return resp, nil
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
	// notifies the backend, letting the poll below observe completion.
	stepUpURL, err := GetMFALinkForSudo(ac, serverName)
	if err != nil {
		return fmt.Errorf("failed to get the MFA step-up link: %w", err)
	}
	// GetMFALink can return an empty URL without an error on a malformed
	// response; fail fast rather than print a blank link and poll until timeout.
	if stepUpURL == "" {
		return fmt.Errorf("server returned an empty MFA step-up link")
	}

	fmt.Fprintf(os.Stderr, "\nsudo needs a recent MFA to proceed. Open this link to verify:\n%s\n", stepUpURL)
	fmt.Fprintf(os.Stderr, "Press Enter to open it in your browser, or open the link manually.\n")

	// Open the browser on Enter without blocking the poll. The done guard makes a
	// late Enter—pressed after the step-up already completed or timed out—a no-op,
	// so the browser never opens unexpectedly later in the command flow. EOF or a
	// closed stdin simply skips the auto-open; it never aborts the flow. The
	// goroutine ends at process exit if Enter is never pressed.
	done := make(chan struct{})
	defer close(done)
	go func() {
		if _, rerr := bufio.NewReader(os.Stdin).ReadString('\n'); rerr != nil {
			return
		}
		select {
		case <-done:
		default:
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
			completed, cerr := CheckSensitiveMFACompletion(ac)
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
// enabling the completion poll to detect when MFA is done.
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
