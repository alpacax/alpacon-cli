package worksession

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// Operation identifiers carried in JSON outputs: success "operation" and error context.operation.
const (
	opActivate  = "activate"
	opCancel    = "cancel"
	opComplete  = "complete"
	opCreate    = "create"
	opCurrent   = "current"
	opDescribe  = "describe"
	opExtend    = "extend"
	opList      = "list"
	opRecording = "recording"
	opRevoke    = "revoke"
	opTimeline  = "timeline"
	opUnset     = "unset"
	opUpdate    = "update"
	opUse       = "use"
)

var (
	statusFilter    string
	requesterFilter string
	userFilter      string
)

var WorkSessionCmd = &cobra.Command{
	Use:     "work-session",
	Aliases: []string{"session"},
	Short:   "Create and manage work sessions",
	Long: `Create, inspect, and manage work sessions.

Work sessions are approval-gated units that authorize access-path operations on Alpacon.

Gated operations (require an active WorkSession under interactive auth):
  websh—'alpacon websh' (browser-based terminal)
  command—'alpacon exec' (remote command execution)
  webftp—'alpacon cp' (file transfer)
  tunnel—'alpacon tunnel' (port forwarding)
  sudo—'Privilege elevation' on Alpacon web (binding op: pending/approved/active allowed)

Bypass: Token auth (API token or Service token) skips the requirement for 'exec', 'cp',
'edit', and 'websh SERVER COMMAND', but cannot run an interactive 'websh' terminal,
'websh watch', 'websh join', 'websh invite', or 'tunnel'—those need 'alpacon login'
without -t.

Lifecycle:  pending → approved → active → complete | expired | revoked  (pending only → rejected | cancelled)

Error codes returned when a session check fails:
  work_session_required           no session selected for this shell
  work_session_not_active         session not active (pending, approved, completed, revoked, or cancelled)
  work_session_expired            session has expired
  work_session_scope_not_allowed  operation not in session scopes
  work_session_server_not_allowed target server not in session
  work_session_assignee_mismatch  session assigned to another principal
  work_session_not_usable         session is no longer usable

Run 'alpacon whoami' to check your WorkSession requirement and active session.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd, "Approval and rejection happen in the Alpacon console (web) or Slack.")
	},
}

func init() {
	WorkSessionCmd.AddCommand(workSessionListCmd)
	WorkSessionCmd.AddCommand(workSessionCreateCmd)
	WorkSessionCmd.AddCommand(workSessionDescribeCmd)
	WorkSessionCmd.AddCommand(workSessionActivateCmd)
	WorkSessionCmd.AddCommand(workSessionCompleteCmd)
	WorkSessionCmd.AddCommand(workSessionExtendCmd)
	WorkSessionCmd.AddCommand(workSessionUpdateCmd)
	WorkSessionCmd.AddCommand(workSessionUseCmd)
	WorkSessionCmd.AddCommand(workSessionCurrentCmd)
	WorkSessionCmd.AddCommand(workSessionTimelineCmd)
	WorkSessionCmd.AddCommand(workSessionRecordingCmd)
	WorkSessionCmd.AddCommand(workSessionApproveCmd)
	WorkSessionCmd.AddCommand(workSessionRejectCmd)
	WorkSessionCmd.AddCommand(workSessionRevokeCmd)
	WorkSessionCmd.AddCommand(workSessionCancelCmd)
}
