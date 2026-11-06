package exec

import (
	"errors"
	"io"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// RunExecWithPresenceStepUp runs a command via RunCommandWithRetry and, when it
// is denied for a missing recent MFA (SUDO_PRESENCE_REQUIRED) on an interactive
// terminal, offers an MFA step-up and retries once. Non-interactive callers
// (scripts, CI, AI agents) fall through unchanged so HandleCommandResult prints
// the static denial hint; non-interactive humans additionally get the
// verification link they can complete out of band. Reached via RunRemoteExec by
// exec and websh command mode; interactive websh keeps its own sudo MFA flow.
func RunExecWithPresenceStepUp(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID, purpose string, out io.Writer) error {
	return runWithPresenceStepUp(ac, serverName, func() error {
		return RunCommandWithRetry(ac, serverName, command, username, groupname, env, workSessionID, purpose, out)
	})
}

// runWithPresenceStepUp is RunExecWithPresenceStepUp over a commandRun.
func runWithPresenceStepUp(ac *client.AlpaconClient, serverName string, run commandRun) error {
	err := run()
	// A real presence denial makes sudo exit non-zero, so it always surfaces as a
	// RemoteCommandError carrying the denial line. Require that error as well as
	// the line match: a command that merely prints the line and SUCCEEDS
	// (err == nil) must not trigger a step-up and a re-run of a side-effecting
	// command.
	var remoteErr *event.RemoteCommandError
	if !errors.As(err, &remoteErr) || !hasSudoPresenceDenial(remoteErr.Output) {
		return err
	}

	if !utils.IsInteractiveShell() {
		printPresenceStepUpLink(ac, serverName)
		return err
	}

	if stepErr := mfa.StepUpForSudo(ac, serverName); stepErr != nil {
		utils.CliWarning("MFA step-up did not complete: %s", stepErr)
		return err
	}

	// Presence is fresh—retry once. Any remaining denial falls through to the
	// static hint in HandleCommandResult.
	return run()
}

// printPresenceStepUpLink surfaces the verification link for a non-interactive
// presence denial, so a human reading the logs can complete MFA out of band and
// re-run. We cannot prompt or open a browser here. A link that cannot be
// fetched is dropped silently: the static denial hint already carries the
// actionable part, and the link is the extra.
func printPresenceStepUpLink(ac *client.AlpaconClient, serverName string) {
	url, err := mfaLinkByServerName(ac, serverName)
	if err != nil {
		return
	}
	utils.CliInfo("MFA verification link (open in a browser):\n  %s", url)
}
