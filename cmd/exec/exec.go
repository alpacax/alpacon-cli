package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var ExecCmd = &cobra.Command{
	Use:   "exec [flags] [USER@]SERVER [--] [COMMAND...]", // [flags] stays first: words after SERVER go to the remote command
	Short: "Execute a command on a remote server",
	Long: `Execute a command on a remote server.

This command executes a specified command on a remote server and returns the output.
It supports SSH-like syntax for specifying the user and server.

Use -- to separate alpacon flags from the remote command, ensuring that flags
intended for the remote command (e.g., -U, -d) are not interpreted as alpacon flags.

All flags must be placed before the server name.

The session purpose is what the assessor judges each command against. An agent
should pass --purpose with each command, which skips the purpose demand entirely.

Subcommand names (ls, logs, purpose) win over a server of the same name. To reach
a server literally named 'ls', 'logs' or 'purpose', put -- before it:
alpacon exec -- ls uptime

Shell metacharacters (;, |, &, $) pass through unquoted to the remote shell.
To send a literal metacharacter, wrap the argument in quotes:
  alpacon exec server 'echo hello;world'

Verified file execution (--file) submits a script as structure instead of a
command line: the platform hashes the bytes the reviewer sees, and the agent
proves at execution time that the file it runs on the server is those bytes. The
file must already exist at that path on the target server, or --upload writes it
there first; the CLI reads the content from the same path locally (or from
--file-from) so the reviewer and the assessor judge what you are about to run.
A script that exists only on the server takes --from-server: the CLI reads the
file from the server as the account the script runs as, and submits those
bytes, so no local copy is needed. With --upload the bytes written are the bytes submitted, and the agent still
hashes the file at execution and refuses a mismatch. A file --upload wrote stays
on the server even when the submission is then refused. An approval binds
exactly those bytes, on that server, as that account, with those arguments—it
does not mean "the file is safe". Environment, libraries, the interpreter's
version and anything the script fetches at run time are yours; only the first
entrypoint is verified. Composition (pipes, redirection, &&) goes inside the
script, where it is reviewed and hashed with it; a one-off composition around a
script stays on the ordinary command line. A reviewer can mark an approval
standing, and an unchanged re-run then stops asking anyone—one changed byte
re-queues review. The approver decides whether to grant reuse, and a grant
always expires: after the days --reuse-days proposed, or at the workspace
ceiling (1 to 366 days, default 90).

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
  --file PATH                   Run the script at PATH on the server as a verified
                                file instead of a command line. PATH is absolute on
                                the target; the content is read from the same path
                                locally and sent byte-for-byte (64 KB at most). The
                                script's arguments go after --; --file takes no
                                command line and no --env. Arguments are recorded
                                with the command and shown to the reviewer, so a
                                secret goes in a file or the environment the
                                script reads at run time, never in an argument.
  --file-from LOCAL_PATH        Read the script's content from LOCAL_PATH instead of
                                PATH. The file on the server must be byte-identical,
                                or the agent refuses to run it.
  --upload                      Write the script to PATH on the server first, then
                                submit it, in one step. The file is written as the
                                account and group the script runs as (-u or USER@,
                                -g, or the server's default account when neither
                                is given): a new file takes that account's umask,
                                missing directories are created, and the
                                interpreter reads the file, so no execute bit is
                                needed. An existing file is refused unless
                                --overwrite is given. PATH must be clean: no
                                '.' or '..' and no repeated or trailing slash.
  --overwrite                   With --upload, replace a file already at PATH. The
                                file keeps its owner and mode.
  --from-server                 Read the script's content from PATH on the server
                                instead of a local file, as the account and group
                                the script runs as, and submit those bytes. Use it
                                for a script that already exists only on the
                                server. Cannot be combined with --upload or
                                --file-from.
  --interpreter PATH            Interpreter for --file (default /bin/bash). Must be
                                an absolute path; a bare name would let the server's
                                PATH decide what runs.
  --reuse-days N                Propose how long an approval of --file stays
                                reusable, 1 to 366 days. The approver decides
                                whether to grant reuse, and a grant expires after
                                N days or at the workspace ceiling (1 to 366 days,
                                default 90), whichever is sooner. A ceiling lower
                                than N refuses the request. Without the flag no
                                duration is proposed; the approver may still
                                grant reuse up to the ceiling.
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
                                Inside an agent-requester work session, a command
                                the gate holds for its purpose can sit for about a
                                minute. Without a purpose it is judged again: it
                                runs, waits for a human approver (who is notified
                                then), or is refused. Pass --purpose to skip the
                                hold, or read the result with 'exec logs'; do not
                                resubmit.
  --wait                        When a sudo command needs human approval, block and
                                re-attempt until a reviewer approves it in the Alpacon
                                console (web), or the wait times out (default 5m).
  --wait-approval DURATION      Like --wait with a custom wait timeout (e.g. 30m;
                                default 5m). Implies --wait.

A command admitted by a standing file-execution grant is marked in the result.

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
secret with --env="KEY" instead. A --file script that carries a credential is
refused the same way; take the secret out of the script and have the script
read it on the host at run time. --output json emits an error envelope on
stderr with error_code command_inline_credential.
A command whose line carries a script body—a heredoc, a multi-line -c or -e
body, or a decoded payload piped into a shell—can be refused when the
workspace approval policy blocks script bodies, with exit code 6 and the code
use_file_execution. Then save the script to a local file and run it on the
same server as the same account with --upload --file PATH --file-from
LOCAL_PATH, which writes it to PATH and runs it in one step, or with --file
PATH --from-server when the script is already at PATH on the server. Add
--interpreter ABSOLUTE_PATH for a script /bin/bash does not run, such as a perl
-e body.
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
    prod-web -- systemctl restart chronyd

  # Run a script the reviewer can read, verified byte-for-byte on the server.
  # /opt/deploy.sh must exist on prod-web; its content is read from the same path
  # locally unless --file-from names another copy. Script arguments go after --.
  alpacon exec --file /opt/deploy.sh root@prod-web -- --fast
  alpacon exec --file /opt/deploy.sh --file-from ./deploy.sh --interpreter /bin/sh prod-web

  # Run a script that exists only on the server; its content is read from there.
  alpacon exec --file /opt/deploy.sh --from-server root@prod-web -- --fast

  # Write a local script to the server and run it as a verified file, in one step.
  alpacon exec --upload --file /opt/deploy.sh --file-from ./deploy.sh root@prod-web -- --fast

  # Propose that the approval stay reusable for 30 days; the approver decides
  alpacon exec --file /opt/deploy.sh --reuse-days 30 root@prod-web -- --fast`,
	// Cobra would otherwise consume remote command flags (e.g. -U, -d); Run parses flags itself.
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		parsed := ParseRemoteExecArgs(args)
		WarnUnsetEnv(parsed.UnsetEnv)

		if parsed.ShowHelp {
			_ = cmd.Help()
			return
		}

		if parsed.Err != "" {
			utils.CliArgumentErrorWithExit("%s", parsed.Err)
			return
		}

		if parsed.Server == "" {
			_ = cmd.Help()
			utils.CliArgumentErrorWithExit("server name is required.")
			return
		}

		if parsed.Command == "" && parsed.File == nil {
			utils.CliArgumentErrorWithExit("You must specify a command to execute, or a script with --file.")
			return
		}

		RunRemoteExec(parsed)
	},
}

// RunRemoteExec is the shared post-parse execution path for exec and websh
// command mode. Requires Server and either Command or File to be set.
func RunRemoteExec(parsed RemoteExecArgs) {
	if parsed.OutputFormat != "" {
		if parsed.OutputFormat != utils.OutputFormatTable && parsed.OutputFormat != utils.OutputFormatJSON {
			utils.CliArgumentErrorWithExit("invalid --output value %q: must be 'table' or 'json'", parsed.OutputFormat)
		}
		utils.OutputFormat = parsed.OutputFormat
	}

	// A local script is read before any network call, so a missing or oversized file fails here;
	// a --from-server script is read once the client exists.
	var file *event.FileExecution
	if parsed.File != nil && !parsed.File.FromServer {
		loaded, msg := loadFileExecution(*parsed.File)
		if msg != "" {
			utils.CliErrorWithExit("%s", msg)
			return
		}
		file = &loaded
	}

	authMethod := config.ResolveAuthMethod()

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		return
	}

	workSessionID := worksession.ResolveOrExit(alpaconClient, parsed.WorkSessionID)

	if file != nil && parsed.File.Upload {
		uploadFileOrExit(alpaconClient, parsed, *file, workSessionID, authMethod)
	}
	if parsed.File != nil && parsed.File.FromServer {
		loaded := downloadFileOrExit(alpaconClient, parsed, workSessionID, authMethod)
		file = &loaded
	}

	env := parsed.Env

	if parsed.Detach {
		submit := func() (event.CommandResponse, error) {
			if file != nil {
				return event.SubmitFileCommand(alpaconClient, parsed.Server, *file, parsed.Username, parsed.Groupname, workSessionID, parsed.Purpose)
			}
			return event.SubmitCommand(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env, workSessionID, parsed.Purpose)
		}
		resp, err := submit()
		if err != nil {
			err = utils.HandleCommonErrors(err, parsed.Server, mfa.ErrorCallbacks(alpaconClient, func() error {
				resp, err = submit()
				return err
			}))
		}
		if err != nil {
			utils.HandleWorkSessionError(err, utils.OperationCommand, parsed.Server, authMethod, workSessionID)
			if HandleFileExecRefusal(err, parsed.Server) {
				return
			}
			if HandleExecUserRefusal(err, parsed) {
				return
			}
			if file != nil && HandleFileExecInlineCredential(err) {
				return
			}
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

	// JSON mode buffers output so stdout stays clean for a pending-approval signal; table mode
	// streams live.
	var out io.Writer = os.Stdout
	var buf *bytes.Buffer
	if utils.OutputFormat == utils.OutputFormatJSON {
		buf = &bytes.Buffer{}
		out = buf
	}

	if file != nil {
		err = RunFileExecWithApprovalWait(alpaconClient, parsed.Server, *file, parsed.Username, parsed.Groupname, workSessionID, parsed.Purpose, parsed.WaitTimeout(), out)
	} else {
		err = RunExecWithApprovalWait(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env, workSessionID, parsed.Purpose, parsed.WaitTimeout(), out)
	}
	utils.HandleWorkSessionError(err, utils.OperationCommand, parsed.Server, authMethod, workSessionID)
	// A purpose demand has no approval request yet, so it is reported before the pending-approval
	// path, which would name a queue the command is not in.
	if HandlePurposeDemand(err) {
		return
	}
	// Before HandleCommandResult, which would treat the pending denial as a plain failure.
	if HandlePendingApproval(err, parsed) {
		return
	}
	// The command never ran, so under --output json nothing reaches stdout: the
	// buffer holds only the server's own wording of the refusal.
	if HandleUseFileExecutionRefusal(err, parsed) {
		return
	}
	if buf != nil {
		_, _ = os.Stdout.Write(buf.Bytes())
	}
	// A file-lane refusal names the server in its guidance, which
	// HandleCommandResult does not know; it answers only its own codes.
	if HandleFileExecRefusal(err, parsed.Server) {
		return
	}
	if HandleExecUserRefusal(err, parsed) {
		return
	}
	if file != nil && HandleFileExecInlineCredential(err) {
		return
	}
	HandleCommandResult(err, parsed.InvokedAs)
}

// reRunHint reconstructs the exec invocation (server, command, any -u/-g/--work-session flags and
// --env keys) so the pending-approval message shows what to re-run once the request is approved.
// -- precedes the command so remote flags are never re-parsed as alpacon flags. The server is
// quoted with argvQuote and the -u/-g/--work-session values come quoted from runAsFlags; --env
// keys are not quoted. Command stays executable as is; with --env keys the caveat goes in
// Description, since a consumer replaying Command would otherwise submit without them.
// It names exec even when invoked as websh, which has no --wait.
// On the file lane it repeats only the --file flags the user gave, each quoted with argvQuote since
// they were argv, never a shell line. It drops --upload and --overwrite, because the first run
// already wrote the file and the re-run verifies it in place, and keeps --from-server, since there
// may be no local copy to read.
func reRunHint(parsed RemoteExecArgs) utils.NextAction {
	parts := append([]string{string(ExecInvocation)}, runAsFlags(parsed)...)
	// Env keys only, never values: the rerun re-reads each value from the shell, keeping secrets
	// off stderr and out of the logs.
	keys := make([]string, 0, len(parsed.Env))
	for k := range parsed.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, "--env="+k)
	}
	if parsed.File != nil {
		parts = append(parts, "--file "+argvQuote(parsed.File.Path))
		if parsed.File.From != "" {
			parts = append(parts, "--file-from "+argvQuote(parsed.File.From))
		}
		if parsed.File.FromServer {
			parts = append(parts, "--from-server")
		}
		if parsed.File.Interpreter != "" {
			parts = append(parts, "--interpreter "+argvQuote(parsed.File.Interpreter))
		}
		if parsed.File.ReuseDays > 0 {
			parts = append(parts, "--reuse-days "+strconv.Itoa(parsed.File.ReuseDays))
		}
		parts = append(parts, argvQuote(parsed.Server))
		if len(parsed.File.Args) > 0 {
			parts = append(parts, "--")
			for _, a := range parsed.File.Args {
				parts = append(parts, argvQuote(a))
			}
		}
	} else {
		parts = append(parts, argvQuote(parsed.Server), "--", parsed.Command)
	}

	action := utils.NextAction{Command: strings.Join(parts, " ")}
	if len(keys) > 0 {
		action.Description = "--env values are re-read from your shell; export them before re-running"
	}
	return action
}

// fileSuggestionAction renders the server's file_execution_suggestion for a
// held command line as the --file --from-server command that submits the same
// script on the verified file lane, keeping the account, group and work
// session of the original. A suggestion whose interpreter was a bare name
// (interpreter null, interpreter_name set) gets an --interpreter placeholder
// instead, since --file takes only an absolute interpreter and only the target
// knows where that name resolves. It answers false when there is no
// suggestion, when the command was already on the file lane, when it carried
// --env (the file lane cannot, so the script would run without them), or when
// the suggestion names a path or interpreter --file would refuse or holds a
// control character argvQuote would print as is.
func fileSuggestionAction(parsed RemoteExecArgs, s *event.FileExecutionSuggestion) (utils.NextAction, bool) {
	if s == nil || parsed.File != nil || len(parsed.Env) > 0 || !strings.HasPrefix(s.Path, "/") {
		return utils.NextAction{}, false
	}
	if slices.ContainsFunc(append([]string{s.Path, s.Interpreter}, s.Args...), hasControlChar) {
		return utils.NextAction{}, false
	}
	// The CLI cannot cancel the held command, which runs if approved, so submitting this as well
	// would file a second approval request for the same script.
	const heldNote = "; the held command stays queued and runs if approved, so cancel it in the Alpacon console (Commands) before submitting this"
	description := "to run the script as a verified file instead, whose content the reviewer reads and whose approval an unchanged re-run can reuse" + heldNote
	interpreter := s.Interpreter
	switch {
	case interpreter == DefaultInterpreter:
		interpreter = ""
	case interpreter != "":
		if !strings.HasPrefix(interpreter, "/") {
			return utils.NextAction{}, false
		}
	case isInterpreterName(s.InterpreterName):
		interpreter = "<absolute-path-of-" + s.InterpreterName + ">"
		description = fmt.Sprintf("to run the script as a verified file instead, replace %s with the absolute path of %s on the server, which 'command -v %s' prints there"+heldNote,
			interpreter, s.InterpreterName, s.InterpreterName)
	default:
		return utils.NextAction{}, false
	}
	suggested := RemoteExecArgs{
		Username:      parsed.Username,
		Groupname:     parsed.Groupname,
		WorkSessionID: parsed.WorkSessionID,
		Server:        parsed.Server,
		File: &FileExecArgs{
			Path:        s.Path,
			Interpreter: interpreter,
			Args:        s.Args,
			FromServer:  true,
		},
	}
	return utils.NextAction{
		Command:     reRunHint(suggested).Command,
		Description: description,
	}, true
}

// hasControlChar reports whether s holds a C0 or C1 control character.
func hasControlChar(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// isInterpreterName reports whether name is a bare command name safe to print
// in a placeholder and a 'command -v' hint: no path separator, no quoting.
func isInterpreterName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r == '/' || !isArgvSafe(r) {
			return false
		}
	}
	return true
}
