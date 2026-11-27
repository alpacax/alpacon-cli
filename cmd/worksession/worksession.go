package worksession

import (
	"context"
	"fmt"
	"regexp"
	"time"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

const approvalGuidance = "Approval and rejection happen in the Alpacon console (web) or Slack."

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

func NewWorkSessionCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "work-session",
		Aliases: []string{"session"},
		Short:   "Create and manage work sessions",
		Long: `Create, inspect, and manage work sessions.

Work sessions are approval-gated units that authorize access-path operations on Alpacon.

Gated operations:
  websh—'alpacon websh' (browser-based terminal)
  command—'alpacon exec' (remote command execution)
  webftp—'alpacon cp' (file transfer)
  tunnel—'alpacon tunnel' (port forwarding)
  sudo—'Privilege elevation' on Alpacon web (binding op: pending/approved/active allowed)
  kube—'alpacon kube login' and 'kube config' (Kubernetes API; needs a session that
    targets the cluster with --cluster); kube_exec adds exec, attach and port-forward

'exec', 'cp', 'edit', and 'websh SERVER COMMAND' require an active, approved WorkSession
for browser login and personal API tokens. Service tokens use endpoint-specific authorization.
This is client guidance; personal-token enforcement depends on the server version.
Ordinary reads and Work Session requests do not require an active WorkSession.

Tokens cannot run an interactive 'websh' terminal,
'websh watch', 'websh join', 'websh invite', or 'tunnel'—those need 'alpacon login'
without -t (on a machine without a browser, open the printed URL on another device).

Lifecycle:  pending → approved → active → complete | expired | revoked  (pending only → rejected | cancelled)

Error codes returned when a session check fails:
  work_session_required           no session selected for this shell
  work_session_not_active         session not active (pending, approved, completed, revoked, or cancelled)
  work_session_expired            session has expired
  work_session_scope_not_allowed  operation not in session scopes
  work_session_server_not_allowed target server not in session
  work_session_cluster_not_allowed target cluster not in session
  work_session_assignee_mismatch  session assigned to another principal
  work_session_agent_session_required  the CLI runs under an AI agent; use an agent session
  work_session_not_usable         session is no longer usable

Run 'alpacon whoami' to check your WorkSession requirement and active session.`,
		Args: utils.RejectUnknownSubcommand(approvalGuidance),
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd, approvalGuidance)
		},
	}
	cmd.AddCommand(
		newWorkSessionListCmd(newClient, ios),
		newWorkSessionCreateCmd(newClient, ios),
		newWorkSessionDescribeCmd(newClient, ios),
		newWorkSessionActivateCmd(newClient, ios),
		newWorkSessionCompleteCmd(newClient, ios),
		newWorkSessionExtendCmd(newClient, ios),
		newWorkSessionUpdateCmd(newClient, ios),
		newWorkSessionUseCmd(newClient, ios),
		newWorkSessionCurrentCmd(newClient, ios),
		newWorkSessionTimelineCmd(newClient, ios),
		newWorkSessionRecordingCmd(newClient, ios),
		newWorkSessionApproveCmd(),
		newWorkSessionRejectCmd(),
		newWorkSessionRevokeCmd(newClient, ios),
		newWorkSessionCancelCmd(newClient, ios),
	)
	return cmd
}

var shellSafeSessionID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

type pendingEndError struct {
	error
	id string
}

func (e *pendingEndError) Error() string {
	return fmt.Sprintf("%s. Work session %s is pending approval", e.error, e.id)
}

func (e *pendingEndError) Unwrap() error { return e.error }

func (e *pendingEndError) ErrorNextAction() utils.NextAction {
	action := utils.NextAction{Description: "cancel the pending work session"}
	if shellSafeSessionID.MatchString(e.id) {
		action.Command = "alpacon work-session cancel " + e.id
	} else {
		action.Description = "cancel the pending work session with alpacon work-session cancel SESSION_ID"
	}
	return action
}

// endSession ends the session and reports whether it cleared the active selection.
// A pending session can be cancelled instead. A failure to clear the selection only warns.
func endSession(ios *utils.IOStreams, ac *client.AlpaconClient, id string, end func(*client.AlpaconClient, string) error) (bool, error) {
	if err := end(ac, id); err != nil {
		if utils.StructuredErrorCode(err) == utils.WorkSessionNotActive && utils.RefusalHint(err) == "" {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if session, lookupErr := wsapi.GetWorkSessionWithContext(ctx, ac, id); lookupErr == nil && session.Status == wsapi.StatusPending {
				return false, &pendingEndError{error: err, id: id}
			}
		}
		return false, err
	}
	cleared, err := config.ClearActiveWorkSessionFor(ac.WorkspaceName, id)
	if err != nil {
		ios.Warning("Work session %s has ended but is still the active work-session: %s. Run 'alpacon work-session use --unset' to clear it.", id, err)
	}
	return cleared, nil
}
