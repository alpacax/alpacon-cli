package mfa

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
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
	mfaURL, err := GetMFALinkByServerName(ac, serverName)
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

// ErrorCallbacks returns the standard callback set commands hand to
// utils.HandleCommonErrors: MFA and username-required handling, MFA completion
// polling, and a token refresh once MFA completes. The retry function re-runs
// the operation that failed.
func ErrorCallbacks(ac *client.AlpaconClient, retry func() error) utils.ErrorHandlerCallbacks {
	return utils.ErrorHandlerCallbacks{
		OnMFARequired: func(serverName string) error {
			return HandleMFAError(ac, serverName)
		},
		OnUsernameRequired: func() error {
			_, err := iam.HandleUsernameRequired()
			return err
		},
		CheckMFACompleted: func() (bool, error) {
			return CheckMFACompletion(ac)
		},
		RefreshToken:   ac.RefreshToken,
		RetryOperation: retry,
	}
}

// WorkspaceErrorCallbacks is ErrorCallbacks for a workspace-level change: a workspace-wide
// setting or role binding names no server, so the server-scoped MFA link's lookup would be
// handed an empty name. No username handling: such a change never asks for a system user.
func WorkspaceErrorCallbacks(ac *client.AlpaconClient, retry func() error) utils.ErrorHandlerCallbacks {
	return utils.ErrorHandlerCallbacks{
		OnMFARequired: func(_ string) error {
			mfaURL, err := GetWorkspaceSecurityMFALink(ac)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "\nMFA authentication required. Please visit:\n%s\n\n", mfaURL)
			utils.OpenBrowser(mfaURL)
			return nil
		},
		CheckMFACompleted: func() (bool, error) {
			return CheckMFACompletion(ac)
		},
		RefreshToken:   ac.RefreshToken,
		RetryOperation: retry,
	}
}

// GetMFALinkByServerName is for callers holding a name, not an ID. It runs at the
// MFA prompt, which an editor session or a long transfer puts minutes after the
// client was built—config by then can name another workspace.
func GetMFALinkByServerName(ac *client.AlpaconClient, serverName string) (string, error) {
	// Before the lookup, so no workspace costs no round trip and the error names
	// the workspace rather than a lookup that failed after it.
	if err := requireWorkspace(ac); err != nil {
		return "", err
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve server: %w", err)
	}

	return GetMFALink(ac, serverID)
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
	stepUpURL, err := GetMFALinkByServerName(ac, serverName)
	if err != nil {
		return fmt.Errorf("failed to get the MFA step-up link: %w", err)
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

// GetWorkspaceSecurityMFALink uses location "cli" so the mfa-success page notifies
// the backend, which is what CheckMFACompletion polls for.
func GetWorkspaceSecurityMFALink(ac *client.AlpaconClient) (string, error) {
	return fetchMFALink(ac, map[string]string{
		"location":  "cli",
		"workspace": ac.WorkspaceName,
	})
}

// GetMFALink scopes the link to one server. The workspace is the client's own,
// never caller-supplied: no link may name a workspace the request is not going to.
func GetMFALink(ac *client.AlpaconClient, serverID string) (string, error) {
	return fetchMFALink(ac, map[string]string{
		"location":  "cli",
		"server":    serverID,
		"workspace": ac.WorkspaceName,
	})
}

// requireWorkspace rejects a client that can name no workspace: every link carries one.
func requireWorkspace(ac *client.AlpaconClient) error {
	if ac.WorkspaceName == "" {
		return errors.New("no workspace name on the client; run 'alpacon login' first")
	}
	return nil
}

// fetchMFALink rejects an empty URL as an error: every caller prints the link to
// the user. The workspace is checked first, or the link would name none.
func fetchMFALink(ac *client.AlpaconClient, params map[string]string) (string, error) {
	if err := requireWorkspace(ac); err != nil {
		return "", err
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
