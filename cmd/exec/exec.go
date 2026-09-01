package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var ExecCmd = &cobra.Command{
	Use:   "exec [flags] [USER@]SERVER [--] COMMAND...",
	Short: "Execute a command on a remote server",
	Long: `Execute a command on a remote server.

This command executes a specified command on a remote server and returns the output.
It supports SSH-like syntax for specifying the user and server.

Use -- to separate alpacon flags from the remote command, ensuring that flags
intended for the remote command (e.g., -U, -d) are not interpreted as alpacon flags.

All flags must be placed before the server name.

Subcommand names (ls, logs, purpose) win over a server of the same name. To reach
a server literally named 'ls', 'logs' or 'purpose', put -- before it:
alpacon exec -- ls uptime

Shell metacharacters (;, |, &, $) pass through unquoted to the remote shell.
To send a literal metacharacter, wrap the argument in quotes:
  alpacon exec server 'echo hello;world'

Flags:
  -u, --username [USER_NAME]    Specify the username for command execution.
  -g, --groupname [GROUP_NAME]  Specify the group name for command execution.
  --env="KEY"                   Pass an environment variable to the remote command,
                                reading its value from the current shell. This keeps
                                the value off your local alpacon command line—use it
                                for secrets such as passwords or tokens.
  --env="KEY=VALUE"             Set 'KEY' to a literal value. Discouraged: the value
                                stays on your local alpacon command line, so it lands
                                in shell history, ps output, and CI job logs. Never
                                pass credentials this way.
  --work-session [UUID]         Attach this command to a work-session.
                                Overrides the workspace's active session set via
                                'alpacon work-session use'.
  --purpose "TEXT"              State what this command is for (max 2000 chars).
                                The assessor judges the command with it in hand,
                                so a command that would otherwise queue for a
                                human may clear on its own. State a fact local to
                                this host that the session description does not
                                already imply; general knowledge adds nothing the
                                assessor lacks, and a purpose cannot lower a
                                command's intrinsic risk.
  --detach                      Submit the command and return immediately without
                                waiting for completion. Prints the job ID to stdout.
                                Use 'alpacon exec logs JOB_ID' to retrieve the result.
  --wait                        When a sudo command needs human approval, block and
                                re-attempt until a reviewer approves it in the Alpacon
                                console (web), or the wait times out (default 5m).
  --wait-approval DURATION      Like --wait with a custom wait timeout (e.g. 30m;
                                default 5m). Implies --wait.

Exit code 3 indicates a WorkSession gate denial; run with --output json to
parse a machine-readable diagnostic on stderr.
Exit code 4 indicates the sudo command is pending human approval (approve it in
the Alpacon console, then re-run, or pass --wait to block—--wait-approval
DURATION to block with a longer timeout); --output json emits
{"status":"pending_approval", ...} on stdout.
Exit code 7 indicates the verification gate held the command and is asking what
it is for. Nobody has been notified and no approval request exists: answer with
'alpacon exec purpose JOB_ID "..."' within about a minute, or pass --purpose up
front and skip the demand. --wait does not apply—the answer is yours to give,
not somebody else's to grant. --output json emits {"status":"purpose_required",
...} on stdout, carrying the command id and what makes a purpose useful.
The server rejects a command whose command line carries a credential—a
-p/--password flag, a KEY=VALUE secret such as PGPASSWORD=..., or a
user:pass@host connection string—before it runs, with exit code 1. Pass the
secret with --env="KEY" instead; --output json emits an error envelope on
stderr with error_code command_inline_credential.
Requires an active WorkSession when using Browser login (Auth0); Token auth (API token or Service token) bypasses this requirement.`,
	Example: `  # Simple command execution
  alpacon exec prod-docker docker ps
  alpacon exec root@prod-docker docker ps

  # Use -- to pass flags to the remote command
  alpacon exec root@db-server -- docker exec postgres psql -U myproject -d myproject
  alpacon exec my-server -- grep -r "pattern" /var/log

  # Specify user and group with flags
  alpacon exec -u root prod-docker systemctl status nginx
  alpacon exec -g docker user@server docker images

  # Pass a secret via the shell env; the value stays off the alpacon command line.
  # Read it in rather than typing it inline, so it stays out of shell history too.
  # psql reads PGPASSWORD from the environment, so it never reaches the remote argv.
  printf 'PGPASSWORD: ' && read -rs PGPASSWORD && export PGPASSWORD
  alpacon exec --env="PGPASSWORD" db-server -- psql -h localhost -U app -c 'SELECT 1'

  # Submit a command asynchronously and retrieve the result later
  alpacon exec --detach web-server -- apt-get update
  alpacon exec logs <JOB_ID>

  # Block up to 30 minutes for a reviewer to approve a sudo command
  alpacon exec --wait-approval 30m root@prod-docker -- systemctl restart nginx

  # State what the command is for, so it is judged with that in hand
  alpacon exec --purpose 'chronyd drifted 40s; the renewed cert reads as future-dated' \
    prod-web -- systemctl restart chronyd`,
	// DisableFlagParsing is required because remote command arguments (e.g., -U, -d)
	// would otherwise be consumed by Cobra's flag parser.
	// All flags are parsed manually in the Run function.
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		parsed := ParseRemoteExecArgs(args)

		if parsed.ShowHelp {
			_ = cmd.Help()
			return
		}

		if parsed.Err != "" {
			utils.CliErrorWithExit("%s", parsed.Err)
			return
		}

		if parsed.Server == "" {
			_ = cmd.Help()
			utils.CliErrorWithExit("server name is required.")
			return
		}

		if parsed.Command == "" {
			utils.CliErrorWithExit("You must specify a command to execute.")
			return
		}

		RunRemoteExec(parsed)
	},
}

// RunRemoteExec is the shared post-parse execution path for exec and websh
// command mode. Requires Server and Command to be non-empty.
func RunRemoteExec(parsed RemoteExecArgs) {
	if parsed.OutputFormat != "" {
		if parsed.OutputFormat != utils.OutputFormatTable && parsed.OutputFormat != utils.OutputFormatJSON {
			utils.CliErrorWithExit("invalid --output value %q: must be 'table' or 'json'", parsed.OutputFormat)
		}
		utils.OutputFormat = parsed.OutputFormat
	}

	workSessionID := worksession.ResolveOrExit(parsed.WorkSessionID)

	authMethod := config.ResolveAuthMethod()

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		return
	}

	env := parsed.Env

	if parsed.Detach {
		resp, err := event.SubmitCommand(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env, workSessionID, parsed.Purpose)
		if err != nil {
			err = utils.HandleCommonErrors(err, parsed.Server, mfa.ErrorCallbacks(alpaconClient, func() error {
				resp, err = event.SubmitCommand(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env, workSessionID, parsed.Purpose)
				return err
			}))
		}
		if err != nil {
			utils.HandleWorkSessionError(err, "command", parsed.Server, authMethod, workSessionID)
			utils.CliErrorWithExit("failed to submit command on '%s': %s", parsed.Server, err)
			return
		}
		if utils.OutputFormat == utils.OutputFormatJSON {
			data, err := json.Marshal(map[string]string{"job_id": resp.ID})
			if err != nil {
				utils.CliErrorWithExit("failed to marshal JSON: %s", err)
				return
			}
			utils.PrintJson(data)
		} else {
			line1, line2 := detachResultLines(resp.ID)
			fmt.Println(line1)
			fmt.Fprintln(os.Stderr, line2)
		}
		return
	}

	// JSON mode buffers output to keep stdout clean for a pending-approval
	// signal; it is flushed below on success or plain failure. Table mode streams live.
	var out io.Writer = os.Stdout
	var buf *bytes.Buffer
	if utils.OutputFormat == utils.OutputFormatJSON {
		buf = &bytes.Buffer{}
		out = buf
	}

	err = RunExecWithApprovalWait(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env, workSessionID, parsed.Purpose, parsed.WaitTimeout(), out)
	utils.HandleWorkSessionError(err, "command", parsed.Server, authMethod, workSessionID)
	// A command parked for its purpose is reported first: it has no approval
	// request yet, so the pending-approval path below would name a queue it is
	// not in (ADR 0052).
	if HandlePurposeDemand(err) {
		return
	}
	// A sudo command left pending human approval, not waited on, emits a
	// machine-readable pending signal and exits before the normal result
	// handling treats the denial as a plain failure.
	if HandlePendingApproval(err, reRunHint(parsed)) {
		return
	}
	if buf != nil {
		_, _ = os.Stdout.Write(buf.Bytes())
	}
	HandleCommandResult(err, parsed.InvokedAs)
}

// reRunHint reconstructs the exec invocation (server, command, and any
// user/group, work-session, or --env keys) so the pending-approval message can
// tell a human exactly what to re-run once the request is approved. It uses --
// before the command so remote flags are never re-parsed as alpacon flags.
// The Command stays a pure, executable string; when --env keys are present the
// caveat rides in Description, since the rerun re-reads each value from the
// shell and a machine consumer replaying Command verbatim would otherwise
// submit without those keys.
// It names exec even when InvokedAs is websh: websh has no --wait, so the rerun
// genuinely has to go through exec (README "Exit codes", row 4).
func reRunHint(parsed RemoteExecArgs) utils.NextAction {
	parts := []string{string(ExecInvocation)}
	if parsed.Username != "" {
		parts = append(parts, "-u "+parsed.Username)
	}
	if parsed.Groupname != "" {
		parts = append(parts, "-g "+parsed.Groupname)
	}
	if parsed.WorkSessionID != "" {
		parts = append(parts, "--work-session "+parsed.WorkSessionID)
	}
	// Emit env keys only (never values): the rerun re-reads each value from the
	// shell, so the secret stays off this hint on stderr and out of the logs.
	keys := make([]string, 0, len(parsed.Env))
	for k := range parsed.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, "--env="+k)
	}
	parts = append(parts, parsed.Server, "--", parsed.Command)

	action := utils.NextAction{Command: strings.Join(parts, " ")}
	if len(keys) > 0 {
		action.Description = "--env values are re-read from your shell; export them before re-running"
	}
	return action
}
