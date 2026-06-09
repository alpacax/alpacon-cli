package worksession

import (
	"fmt"
	"slices"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const pendingWorkSessionStatus = "pending"

// validateSessionForSudoUpdate checks the preconditions for adding sudo
// policies to an existing work session. It is extracted from the command Run
// so the guard logic can be unit-tested without driving the cobra/HTTP stack.
func validateSessionForSudoUpdate(session *wsapi.WorkSession) error {
	if session.Status == pendingWorkSessionStatus {
		return fmt.Errorf(
			"work session %s is pending approval; sudo policies cannot be modified yet. Set them at create time with --sudo, or wait until it is approved",
			session.ID,
		)
	}
	if !slices.Contains(session.Scopes, "sudo") {
		return fmt.Errorf(
			"work session %s does not include the 'sudo' scope; sudo policies cannot be added. Create a new session with --sudo (it adds the 'sudo' scope automatically)",
			session.ID,
		)
	}
	return nil
}

// attachSudoPoliciesToSession is the testable core of the update command Run:
// load the current session, validate it can accept sudo policy changes, then
// PATCH the full desired set (existing policies echoed back by ID + new
// additions). The modify endpoint is PUT-style — any policy absent from the
// request is deleted — so the echo-existing step is the regression bait this
// helper exists to lock down.
func attachSudoPoliciesToSession(
	ac *client.AlpaconClient,
	sessionID string,
	newPolicies []wsapi.SudoPolicyInline,
) (*wsapi.WorkSession, error) {
	session, err := wsapi.GetWorkSession(ac, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load work session %s: %w", sessionID, err)
	}
	if err := validateSessionForSudoUpdate(session); err != nil {
		return nil, err
	}
	desired := make([]wsapi.SudoPolicyInline, 0, len(session.SudoPolicies)+len(newPolicies))
	desired = append(desired, session.SudoPolicies...)
	desired = append(desired, newPolicies...)
	return wsapi.UpdateWorkSession(ac, sessionID, wsapi.WorkSessionUpdateRequest{SudoPolicies: desired})
}

var (
	updateSudo       []string
	updateSudoReason string
)

var workSessionUpdateCmd = &cobra.Command{
	Use:   "update [SESSION_ID]",
	Short: "Update a work session (e.g. add sudo command patterns)",
	Long: `Update an existing work session.

Use --sudo to add MFA-bypass sudo command patterns to the session. This is the
recovery path when an 'exec' sudo was denied: add the command and re-run.
Each --sudo value is a comma-separated pattern list (wildcards allowed) forming one
policy. The session's existing policies are preserved; the additions may require
approval before they take effect.

If SESSION_ID is omitted, the effective work session is resolved from the
ALPACON_WORK_SESSION environment variable, then the workspace's active session
(set via 'alpacon work-session use').`,
	Args: cobra.MaximumNArgs(1),
	Example: `  alpacon work-session update ses-abc123 --sudo "systemctl restart nginx"
  alpacon work-session update --sudo "tail -f /var/log/nginx/*.log"`,
	Run: func(cmd *cobra.Command, args []string) {
		var sessionID string
		if len(args) == 1 {
			sessionID = args[0]
		} else {
			resolved, err := Resolve("")
			if err != nil || resolved == "" {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "No SESSION_ID given and no active work session. Pass an ID, set ALPACON_WORK_SESSION, or run 'alpacon work-session use <id>' first.")
			}
			sessionID = resolved
		}

		newPolicies := buildSudoPolicies(updateSudo, updateSudoReason)
		if len(newPolicies) == 0 {
			utils.CliUsageErrorEnvelopeWithExit(opUpdate, "Nothing to update. Pass --sudo with at least one command pattern.")
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opUpdate, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		updated, err := attachSudoPoliciesToSession(ac, sessionID, newPolicies)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opUpdate, err, "%s.", err)
		}

		utils.CliSuccess("Work session %s updated (status: %s).", updated.ID, updated.Status)
		utils.CliInfo("Added sudo policies may require approval before they take effect. Re-run your command once the session reflects the change.")
	},
}

func init() {
	workSessionUpdateCmd.Flags().StringArrayVar(&updateSudo, "sudo", nil, "Sudo command patterns to add as MFA-bypass policies (repeatable; each value is a comma-separated pattern list forming one policy, wildcards allowed)")
	workSessionUpdateCmd.Flags().StringVar(&updateSudoReason, "sudo-reason", "", "Justification applied to the sudo policies added via --sudo")
}
