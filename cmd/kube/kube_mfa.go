package kube

import (
	"errors"
	"fmt"
	"time"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const opKubeMFA = "kube_mfa"

// The agent's refusal prints this command line verbatim, so it must not change.
const kubeMFACommand = "alpacon kube mfa"

const (
	// kubeMFATick and kubeMFATimeout pace the wait for a person finishing MFA in a browser,
	// the same as the MFA wait 'kube login' runs when it opens a session.
	kubeMFATick    = time.Second
	kubeMFATimeout = 3 * time.Minute
)

// kubeMFAOptions are the seams a test replaces: whether stdin is a terminal, what opens
// the browser, and the wait's pacing.
type kubeMFAOptions struct {
	interactive func() bool
	open        func(url string)
	tick        time.Duration
	timeout     time.Duration
}

// kubeMFAResult is what --output json prints on success.
type kubeMFAResult struct {
	Workspace string `json:"workspace"`
	Verified  bool   `json:"verified"`
	// AlreadyVerified is true when the MFA was fresh before the command ran, so no link was
	// issued.
	AlreadyVerified bool `json:"already_verified"`
}

func newKubeMFACmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return newKubeMFACmdWith(newClient, ios, kubeMFAOptions{
		interactive: utils.IsInteractiveShell,
		open:        utils.OpenBrowser,
		tick:        kubeMFATick,
		timeout:     kubeMFATimeout,
	})
}

func newKubeMFACmdWith(newClient clientFactory, ios *utils.IOStreams, opts kubeMFAOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "mfa",
		Short: "Confirm MFA for Kubernetes writes and deletes",
		Long: `Confirm a recent MFA so that writes and deletes in your kube sessions go through.

A workspace can require a recent MFA for writes and deletes in kube sessions. When
yours is older than the workspace allows, the cluster refuses the request and the
message kubectl prints names this command. It prints a link and opens it in your
browser; once you finish MFA there, it exits and you retry the request. The MFA then
holds until the workspace's MFA window passes. kubectl cannot ask for it itself, so
this is how Helm, Lens and any other client get past the refusal too.

The MFA counts for this CLI only: the cluster checks the CLI that opened the kube
session, so run this command on the machine and login that ran 'alpacon kube
login' or 'alpacon kube config'. A session opened by another CLI, or before your
last 'alpacon login', keeps being refused after this command succeeds; run
'alpacon kube login' for the cluster again to open one this CLI can confirm.
When the MFA is still fresh, it says so and issues no link.

Without a terminal on stdin, as when an AI agent or a script runs it, it prints the
link and exits 9 at once instead of waiting. The link still works after the command
exits: hand it to the person who signs in, and run 'alpacon kube mfa' again once
they are done to confirm. An interactive run that waits 3 minutes without seeing
the MFA finish exits 9 the same way.`,
		Example: `  alpacon kube mfa
  alpacon kube mfa --output json`,
		Args: cobra.NoArgs,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			ac, err := newClient()
			if err != nil {
				return utils.NewEnvelopeExitError(opKubeMFA, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}
			return runKubeMFA(ios, ac, opts)
		}),
	}
}

// runKubeMFA asks for the workspace's MFA link, which the server answers with no link once
// this client's MFA is fresh, and otherwise waits for the person to finish MFA in a browser.
// The server credits the MFA to the client instance the CLI's token names, which is the one
// a kube session it opened carries, so the token is not refreshed afterwards.
func runKubeMFA(ios *utils.IOStreams, ac *client.AlpaconClient, opts kubeMFAOptions) error {
	// Only a login with a person behind it opens a kube session, and only its bearer token
	// reaches the MFA endpoints.
	if !ac.IsBearerAuth() {
		return utils.NewEnvelopeExitError(opKubeMFA, nil, "Tokens issued by Alpacon (service or personal API token) cannot confirm MFA; it needs %s.", utils.LoginWithoutTokenHint)
	}

	link, err := mfa.GetWorkspaceSecurityMFALink(ac)
	if errors.Is(err, mfa.ErrMFAAlreadyDone) {
		return printKubeMFAResult(ios, kubeMFAResult{Workspace: ac.WorkspaceName, Verified: true, AlreadyVerified: true})
	}
	if err != nil {
		return utils.NewEnvelopeExitError(opKubeMFA, err, "Failed to get the MFA link: %s.", err)
	}

	opts.open(link)
	// Without a terminal nobody watches a wait, and the browser leg credits the MFA whether or
	// not the CLI is still polling, so the link is handed out in the failure's next actions.
	if !opts.interactive() {
		return kubeMFAPending(link, fmt.Sprintf("Kubernetes writes and deletes in workspace %q need a recent MFA, and it is not verified yet.", ac.WorkspaceName), nil)
	}

	_, _ = fmt.Fprintf(ios.ErrOut, "Kubernetes writes and deletes in workspace %q need a recent MFA. Open this link to verify:\n  %s\n",
		utils.SanitizeTerminalText(ac.WorkspaceName), utils.SanitizeTerminalText(link))

	spinner := utils.NewSpinner("Waiting for MFA verification...")
	spinner.Start()
	err = mfa.AwaitMFACompletion(ac, opts.tick, opts.timeout)
	spinner.Stop()
	if errors.Is(err, mfa.ErrMFAWaitTimedOut) {
		return kubeMFAPending(link, fmt.Sprintf("MFA was not verified within %v.", opts.timeout), err)
	}
	if err != nil {
		return utils.NewEnvelopeExitError(opKubeMFA, err, "Failed to confirm the MFA: %s.", err)
	}
	return printKubeMFAResult(ios, kubeMFAResult{Workspace: ac.WorkspaceName, Verified: true})
}

// kubeMFAPending is the exit-9 failure for an MFA the person has not finished yet. The link
// goes in the next actions as well, since under --output json the envelope is all a caller
// without a terminal reads. A hint the wait's last refusal carried leads them.
func kubeMFAPending(link, message string, waitErr error) error {
	next := append(utils.RefusalNextActions(waitErr),
		utils.NextAction{Description: "Open this link in a browser and finish MFA: " + link},
		utils.NextAction{Command: kubeMFACommand, Description: "run it again once MFA is done to confirm, then retry the Kubernetes request"},
	)
	return utils.NewMFARequiredExitError(opKubeMFA, message, next)
}

// printKubeMFAResult reports the confirmed MFA. It cannot promise that the retried request
// passes: a kube session opened by another CLI, or before this CLI's identity changed, is
// checked against that CLI's MFA, which the server does not let this one see.
func printKubeMFAResult(ios *utils.IOStreams, result kubeMFAResult) error {
	if ios.IsJSON() {
		if err := utils.PrintJSONValue(ios.Out, result); err != nil {
			return utils.NewExitError("Failed to print the result: %s.", err)
		}
		return nil
	}
	if result.AlreadyVerified {
		ios.Success("MFA is already verified for this CLI in workspace %q.", result.Workspace)
	} else {
		ios.Success("MFA verified for this CLI in workspace %q.", result.Workspace)
	}
	ios.Info("Retry the Kubernetes request. Writes and deletes in the kube sessions this CLI opened go through until the workspace's MFA window passes.")
	ios.Info("If the cluster refuses it the same way again, its kube session was opened by another CLI or before your last 'alpacon login'; run 'alpacon kube login' for the cluster again.")
	return nil
}
