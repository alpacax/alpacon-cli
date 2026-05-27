package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const pendingWorkSessionStatus = "pending"

var (
	updateSudo       []string
	updateSudoReason string
)

var workSessionUpdateCmd = &cobra.Command{
	Use:   "update [SESSION_ID]",
	Short: "Update a work session (e.g. add sudo command patterns)",
	Long: `Update an existing work session.

Use --sudo to add MFA-bypass sudo command patterns to the session. This is the
recovery path when an execute-command sudo was denied: add the command and re-run.
Each --sudo value is a comma-separated pattern list (wildcards allowed) forming one
policy. The session's existing policies are preserved; the additions may require
approval before they take effect.

If SESSION_ID is omitted, the workspace's active work session is used (set via
'alpacon work-session use').`,
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
				utils.CliErrorWithExit("No SESSION_ID given and no active work session. Pass an ID or run 'alpacon work-session use <id>' first.")
			}
			sessionID = resolved
		}

		newPolicies := buildSudoPolicies(updateSudo, updateSudoReason)
		if len(newPolicies) == 0 {
			utils.CliErrorWithExit("Nothing to update. Pass --sudo with at least one command pattern.")
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		// The modify endpoint takes the FULL desired set of policies (PUT-style):
		// any existing policy missing from the request is deleted. Read the
		// current set and echo it back (with IDs) alongside the additions so
		// nothing is dropped.
		session, err := wsapi.GetWorkSession(ac, sessionID)
		if err != nil {
			utils.CliErrorWithExit("Failed to load work session %s: %s.", sessionID, err)
		}
		if session.Status == pendingWorkSessionStatus {
			utils.CliErrorWithExit("Work session %s is pending approval; sudo policies cannot be modified yet. Set them at create time with --sudo, or wait until it is approved.", sessionID)
		}

		desired := make([]wsapi.SudoPolicyInline, 0, len(session.SudoPolicies)+len(newPolicies))
		desired = append(desired, session.SudoPolicies...)
		desired = append(desired, newPolicies...)

		req := wsapi.WorkSessionUpdateRequest{SudoPolicies: desired}
		updated, err := wsapi.UpdateWorkSession(ac, sessionID, req)
		if err != nil {
			utils.CliErrorWithExit("Failed to update work session: %s.", err)
		}

		utils.CliSuccess("Work session %s updated (status: %s).", updated.ID, updated.Status)
		utils.CliInfo("Added sudo policies may require approval before they take effect. Re-run your command once the session reflects the change.")
	},
}

func init() {
	workSessionUpdateCmd.Flags().StringArrayVar(&updateSudo, "sudo", nil, "Sudo command patterns to add as MFA-bypass policies (repeatable; each value is a comma-separated pattern list forming one policy, wildcards allowed)")
	workSessionUpdateCmd.Flags().StringVar(&updateSudoReason, "sudo-reason", "", "Justification applied to the sudo policies added via --sudo")
}
