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
)

// stepUpPollInterval and stepUpTimeout bound the StepUpForSudo wait. They match the
// websh sudo listener (api/event/sudolistener.go) so both terminals behave alike.
const (
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

// HandleMFAErrorForServerID is HandleMFAError for a caller that holds the
// server's ID, such as a transfer already created on it.
func HandleMFAErrorForServerID(ac *client.AlpaconClient, serverID string) error {
	if err := requireWorkspace(ac); err != nil {
		return err
	}
	mfaURL, err := GetMFALink(ac, serverID)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nMFA authentication required. Please visit:\n%s\n\n", mfaURL)
	utils.OpenBrowser(mfaURL)

	return nil
}

// CheckMFACompletion reports whether the server has recorded a fresh MFA for
// this client. Only the exec sudo step-up polls it: the refused request there is
// a command that already ran, so re-running it would repeat its side effects.
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
// utils.HandleCommonErrors: MFA and username-required handling. The retry
// function re-runs the operation that failed, and after an MFA prompt it is
// also what tells the wait that MFA went through.
func ErrorCallbacks(ac *client.AlpaconClient, retry func() error) utils.ErrorHandlerCallbacks {
	return utils.ErrorHandlerCallbacks{
		OnMFARequired: func(serverName string) error {
			return HandleMFAError(ac, serverName)
		},
		OnUsernameRequired: func() error {
			_, err := iam.HandleUsernameRequired()
			return err
		},
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
		RetryOperation: retry,
	}
}

// GetMFALinkByServerName is GetMFALink for a caller holding a server name. The link
// names the client's workspace, not the config's, which can change in the minutes an
// editor session or a long transfer puts between building the client and the prompt.
func GetMFALinkByServerName(ac *client.AlpaconClient, serverName string) (string, error) {
	// Checked before the lookup so a missing workspace fails without a round trip.
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
// when the user presses Enter, and polls until the server records the MFA. It
// returns nil once presence is fresh, or an error on timeout.
//
// Enter never gates progress: the poll runs regardless, so a link opened by hand
// still completes and an absent user ends at the timeout. Callers should still
// gate this on an interactive terminal; scripts, CI, and AI agents never receive
// a presence denial.
func StepUpForSudo(ac *client.AlpaconClient, serverName string) error {
	return stepUpForSudo(ac, serverName, stepUpPollInterval, stepUpTimeout)
}

// stepUpForSudo takes its pacing as arguments, not package vars a test would
// overwrite: the poll runs beside a stdin goroutine, so a var written mid-suite races.
func stepUpForSudo(ac *client.AlpaconClient, serverName string, pollInterval, timeout time.Duration) error {
	// The location=cli link is what lets CheckMFACompletion observe the completion.
	stepUpURL, err := GetMFALinkByServerName(ac, serverName)
	if err != nil {
		return fmt.Errorf("failed to get the MFA step-up link: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nsudo needs a recent MFA to proceed. Open this link to verify:\n%s\n", stepUpURL)
	fmt.Fprintf(os.Stderr, "Press Enter to open it in your browser, or open the link manually.\n")

	// Open the browser on Enter without blocking the poll. The done guard makes a late
	// Enter a no-op, EOF on stdin just skips the auto-open, and the goroutine lives
	// until process exit if Enter never comes.
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

	// A fixed tick, as in the websh sudo listener: a person finishes MFA in a browser
	// inside one 60s window, so a widened tail would turn a half-second detection lag
	// into five and could miss a late completion. utils.NextPollTick paces for a
	// service token's quota, and no script or agent reaches a step-up.
	deadline := time.After(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	// lastRefusal is the latest completion check's refusal, if any, so a timeout
	// still carries its hint.
	var lastRefusal error
	for {
		select {
		case <-deadline:
			spinner.Stop()
			timedOut := fmt.Sprintf("MFA step-up timed out after %v", timeout)
			// Only the refusal's hint goes with the timeout: its code would read
			// as that refusal to an MFA retry.
			return utils.HintOnly(timedOut, lastRefusal)
		case <-ticker.C:
			completed, cerr := CheckMFACompletion(ac)
			lastRefusal = nil
			if cerr != nil && utils.IsRefusalStatus(utils.HTTPStatusCode(cerr)) {
				lastRefusal = cerr
			}
			if cerr != nil || !completed {
				continue // the endpoint may lag the browser; keep polling
			}
			spinner.Stop()
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

// GetWorkspaceSecurityMFALink uses location "cli" so the mfa-success page credits
// the MFA to this client, which is what the retried request is then checked against.
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
