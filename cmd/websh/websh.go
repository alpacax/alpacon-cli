package websh

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	execCmd "github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// errHelpRequested signals that -h/--help was seen during parsing.
var errHelpRequested = errors.New("help requested")

type WebshArgs struct {
	Username      string
	Groupname     string
	ServerName    string
	CommandArgs   []string
	Share         bool
	ReadOnly      bool
	WorkSessionID string
	OutputFormat  string
	Env           map[string]string
}

// ParseWebshArgs parses raw CLI args for `alpacon websh` (DisableFlagParsing mode). It reads every
// flag before returning the first error or errHelpRequested, so OutputFormat holds the last --output.
func ParseWebshArgs(args []string) (WebshArgs, error) {
	res := WebshArgs{Env: map[string]string{}}
	var stop error
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-s" || args[i] == "--share":
			res.Share = true
		case args[i] == "-h" || args[i] == "--help":
			stop = cmp.Or(stop, errHelpRequested)
		case strings.HasPrefix(args[i], "-u") || strings.HasPrefix(args[i], "--username"):
			res.Username, i = extractValue(args, i)
		case strings.HasPrefix(args[i], "-g") || strings.HasPrefix(args[i], "--groupname"):
			res.Groupname, i = extractValue(args, i)
		case args[i] == "--env" || strings.HasPrefix(args[i], "--env="):
			if errMsg := execCmd.ParseEnvArg(args[i], res.Env); errMsg != "" {
				stop = cmp.Or(stop, errors.New(errMsg))
			}
		case strings.HasPrefix(args[i], "--read-only"):
			if strings.Contains(args[i], "=") {
				parts := strings.SplitN(args[i], "=", 2)
				normalized := strings.TrimSpace(strings.ToLower(parts[1]))
				switch normalized {
				case "", "true":
					res.ReadOnly = true
				case "false":
					res.ReadOnly = false
				default:
					stop = cmp.Or(stop, fmt.Errorf("the --read-only value must be either 'true' or 'false'"))
				}
			} else {
				// Without "=", take the next word only if it is "true" or "false"; otherwise true.
				if i+1 < len(args) {
					next := strings.TrimSpace(strings.ToLower(args[i+1]))
					if next == "true" || next == "false" {
						res.ReadOnly = next == "true"
						i++
					} else {
						res.ReadOnly = true
					}
				} else {
					res.ReadOnly = true
				}
			}
		case args[i] == "--work-session" || strings.HasPrefix(args[i], "--work-session="):
			ws, newI := extractValue(args, i)
			if ws == "" {
				stop = cmp.Or(stop, fmt.Errorf("--work-session requires a value"))
			}
			res.WorkSessionID = ws
			i = newI
		case args[i] == "--output" || strings.HasPrefix(args[i], "--output="):
			val, newI := extractValue(args, i)
			if val == "" {
				stop = cmp.Or(stop, fmt.Errorf("--output requires a value (table|json)"))
			} else {
				res.OutputFormat = val
			}
			i = newI
		default:
			if res.ServerName == "" {
				res.ServerName = args[i]
			} else {
				res.CommandArgs = append(res.CommandArgs, args[i:]...)
				i = len(args)
			}
		}
	}
	return res, stop
}

// buildRemoteExecArgs assembles the shared-runner args for websh command mode.
// username and serverName come in separately because Run resolves user@host
// after parsing. InvokedAs is pinned to WebshInvocation so a refusal hint
// renders websh syntax.
func buildRemoteExecArgs(parsed WebshArgs, username, serverName string) execCmd.RemoteExecArgs {
	return execCmd.RemoteExecArgs{
		Username:      username,
		Groupname:     parsed.Groupname,
		WorkSessionID: parsed.WorkSessionID,
		OutputFormat:  parsed.OutputFormat,
		InvokedAs:     execCmd.WebshInvocation,
		Server:        serverName,
		Command:       execCmd.ShellJoin(parsed.CommandArgs),
		Env:           parsed.Env,
	}
}

var WebshCmd = &cobra.Command{
	Use:   "websh [flags] [USER@]SERVER [COMMAND...]", // [flags] stays first: words after SERVER go to the remote command
	Short: "Open a Websh terminal or run a command",
	Long: `Open a Websh terminal for interacting with a server or execute a command directly on the server.
Supports SSH-like user@host syntax for specifying the username inline.
For executing commands, it is highly recommended to wrap the entire command string in quotes
to ensure it is interpreted correctly on the remote server.

Shell metacharacters (;, |, &, $) pass through unquoted to the remote shell.
To send a literal metacharacter, wrap the argument in quotes:
  alpacon websh server 'echo hello;world'

Executing a command (SERVER followed by COMMAND) runs through the same executor
as 'alpacon exec'—identical output, exit codes, and sudo-approval handling.
Command execution does not accept exec-only flags such as --detach, --wait, or --wait-approval.

Exit code 3 indicates a WorkSession gate denial; run with --output json to
parse a machine-readable diagnostic on stderr.
Exit code 4 indicates the sudo command is pending human approval; approve it in
the Alpacon console (web), then re-run—or use 'alpacon exec --wait' to block.
Exit code 7 indicates the verification gate held the command and is asking what
it is for. websh command mode has no --purpose, so it cannot state one up front:
answer with 'alpacon exec purpose JOB_ID "..."', or submit through
'alpacon exec --purpose' when the command is expected to be held.
When websh runs a command, the server rejects it before it runs if its command
line carries a credential—a -p/--password flag, a KEY=VALUE secret such as
PGPASSWORD=..., or a user:pass@host connection string—with exit code 1. Pass
the secret with --env="KEY" instead; --output json emits an error envelope on
stderr with error_code command_inline_credential.
A command whose line carries a script body—a heredoc, a multi-line -c or -e
body, or a decoded payload piped into a shell—can be refused when the
workspace approval policy blocks script bodies, with exit code 6 and the code
use_file_execution. Save the script to a file and run it with
'alpacon exec --file', as the printed commands show; --output json carries them
in next_actions.
Requires an active WorkSession when using Browser login (Auth0). Token auth (API
token or Service token) bypasses this requirement for 'websh SERVER COMMAND',
but cannot open an interactive terminal or watch, join, or invite to a session—those
need 'alpacon login' without -t (on a machine without a browser, open the printed URL on another device).`,
	Example: `  # Open a websh terminal
  alpacon websh my-server

  # Open as root using SSH-like syntax
  alpacon websh root@my-server

  # Open with specific user and group
  alpacon websh admin@my-server
  alpacon websh -u admin -g developers my-server

  # Execute a command on a server
  alpacon websh my-server "ls -la /var/log"
  alpacon websh root@my-server "systemctl status nginx"

  # Pass a secret via the shell env; the value stays off the alpacon command line.
  # Read it in rather than typing it inline, so it stays out of shell history too.
  # psql reads PGPASSWORD directly from the env.
  printf 'PGPASSWORD: ' && read -rs PGPASSWORD && export PGPASSWORD
  alpacon websh --env="PGPASSWORD" my-server 'psql -h localhost -U app -c "SELECT 1"'

  # Share terminal session
  alpacon websh --share my-server
  alpacon websh --share --read-only=true my-server

  # Join an existing shared session
  alpacon websh join --url https://myws.us1.alpacon.io/websh/shared/abcd1234?channel=default --password my-session-pass

  # Session management
  alpacon websh ls                          # List active sessions
  alpacon websh describe SESSION_ID         # Show session details
  alpacon websh watch SESSION_ID            # Watch a session (read-only, staff/superuser only)
  alpacon websh invite SESSION_ID --email user@example.com
  alpacon websh close SESSION_ID            # Close a session
  alpacon websh force-close SESSION_ID      # Force close (Superuser only)

Flags:
  -u, --username [USER_NAME]         Specify the username for command execution.
  -g, --groupname [GROUP_NAME]       Specify the group name for command execution.
  --env="KEY"                        Pass an environment variable, reading its value
                                     from the current shell. This keeps the value off
                                     your local alpacon command line—use it for
                                     secrets such as passwords or tokens.
  --env="KEY=VALUE"                  Set 'KEY' to a literal value. Discouraged: the
                                     value stays on your local alpacon command line,
                                     so it lands in shell history, ps output, and CI
                                     job logs. Never pass credentials this way.
  -s, --share                        Share the terminal via a temporary link.
  --read-only=[true|false]           Set shared session to read-only (default: false).
  --work-session [UUID]              Attach this session to a work-session.
                                     Overrides the workspace's active session
                                     set via 'alpacon work-session use'.

Note: All flags must be placed before the server name.
      Everything after the server name is treated as the remote command.`,
	// Positional args after the server name (e.g. "ls -la") must reach the remote command, so flags
	// are parsed by hand in Run.
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		parsed, err := ParseWebshArgs(args)
		if err != nil {
			if errors.Is(err, errHelpRequested) {
				_ = cmd.Help()
				return
			}
			utils.CliArgumentErrorWithExit("%s", err)
		}

		username := parsed.Username
		groupname := parsed.Groupname
		serverName := parsed.ServerName
		commandArgs := parsed.CommandArgs
		share := parsed.Share
		readOnly := parsed.ReadOnly
		env := parsed.Env

		if serverName == "" {
			utils.CliArgumentErrorWithExit("Server name is required.")
		}

		if share && len(commandArgs) > 0 {
			utils.CliArgumentErrorWithExit("The --share flag cannot be used with remote commands. Use --share for interactive sessions only.")
		}

		if strings.Contains(serverName, "@") && !strings.Contains(serverName, ":") {
			sshTarget := utils.ParseSSHTarget(serverName)
			if username == "" && sshTarget.User != "" {
				username = sshTarget.User
			}
			serverName = sshTarget.Host
		}

		// Command mode is an alias for exec: delegate to the shared runner so the
		// pending-approval exit code, sudo-denial hints, and JSON buffering match
		// exec. Wait is left false—websh has no --wait, so the blocking wait never runs.
		if len(commandArgs) > 0 {
			execCmd.RunRemoteExec(buildRemoteExecArgs(parsed, username, serverName))
			return
		}

		// Interactive websh has no channel for env: CreateWebshSession takes none.
		// Warn rather than silently drop, since the help frames --env as the secrets channel.
		if len(env) > 0 {
			utils.CliWarning("--env has no effect on an interactive websh session; it applies only when running a command")
		}

		if parsed.OutputFormat != "" {
			if parsed.OutputFormat != utils.OutputFormatTable && parsed.OutputFormat != utils.OutputFormatJSON {
				utils.CliArgumentErrorWithExit("invalid --output value %q: must be 'table' or 'json'", parsed.OutputFormat)
			}
			utils.OutputFormat = parsed.OutputFormat
		}

		authMethod := config.ResolveAuthMethod()

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		workSessionID := worksession.ResolveOrExit(alpaconClient, parsed.WorkSessionID)

		session, err := websh.CreateWebshSession(alpaconClient, serverName, username, groupname, share, readOnly, workSessionID)

		if err != nil {
			err = utils.HandleCommonErrors(err, serverName, mfa.ErrorCallbacks(alpaconClient, func() error {
				session, err = websh.CreateWebshSession(alpaconClient, serverName, username, groupname, share, readOnly, workSessionID)
				return err
			}))

			if err != nil {
				utils.HandleWorkSessionError(err, utils.OperationWebsh, serverName, authMethod, workSessionID)
				utils.CliErrorWithExit("Failed to create websh session for '%s' server: %s.", serverName, utils.InteractiveOnly(err, "open an interactive Websh terminal"))
			}
		}
		// Start the sudo MFA listener in the background so it does not delay the terminal; a sudo
		// typed before it is ready lets the approval request expire, and the user can retry.
		listenerDone := make(chan *event.SudoListener, 1)
		go func() {
			listenerDone <- setupSudoListener(alpaconClient, serverName, session.ID)
		}()
		defer func() {
			select {
			case sl := <-listenerDone:
				if sl != nil {
					sl.Stop()
				}
			case <-time.After(3 * time.Second):
				// Don't block exit if listener setup is stuck
			}
		}()

		if err = websh.OpenNewTerminal(alpaconClient, session); err != nil {
			// A session the client could not get back to says so on its own terms:
			// wrapping it in "ended with error" would read as the shell having
			// failed, when what failed is the connection to it.
			switch {
			case errors.Is(err, websh.ErrReconnectFailed):
				utils.CliErrorWithExitCode(utils.ExitCodeGeneralError, "Could not reconnect to the session.")
			case errors.Is(err, websh.ErrSessionGone):
				utils.CliErrorWithExitCode(utils.ExitCodeGeneralError, "The session is no longer open.")
			default:
				utils.CliErrorWithExitCode(utils.ExitCodeGeneralError, "Websh session ended with error: %s.", err)
			}
		}
	},
}

func init() {
	WebshCmd.AddCommand(webshJoinCmd)
	WebshCmd.AddCommand(webshListCmd)
	WebshCmd.AddCommand(webshDescribeCmd)
	WebshCmd.AddCommand(webshCloseCmd)
	WebshCmd.AddCommand(webshForceCloseCmd)
	WebshCmd.AddCommand(webshInviteCmd)
	WebshCmd.AddCommand(webshWatchCmd)
	WebshCmd.AddCommand(webshRecordsCmd)
}

func extractValue(args []string, i int) (string, int) {
	if strings.Contains(args[i], "=") { // --username=admins
		parts := strings.SplitN(args[i], "=", 2)
		return parts[1], i
	}
	if i+1 < len(args) { // --username admin
		return args[i+1], i + 1
	}
	return "", i
}

// setupSudoListener starts the sudo MFA listener for the given websh session.
// Returns nil when it cannot connect.
func setupSudoListener(ac *client.AlpaconClient, serverName, sessionID string) *event.SudoListener {
	listener := event.NewSudoListener(ac, serverName, sessionID)
	listener.Start()

	if !listener.WaitConnected(5 * time.Second) {
		listener.Stop()
		if warning := sudoListenerWarning(listener.Err()); warning != "" {
			// Not utils.CliWarning: this runs in a goroutine racing
			// OpenNewTerminal, so the terminal may already be in raw mode,
			// where a bare newline leaves the cursor where it stood.
			_, _ = fmt.Fprintf(os.Stderr, "\r\n%s: %s\r\n%s", utils.Yellow("Warning"), warning, utils.RawTerminalHintLines(listener.Err()))
		}
		return nil
	}

	return listener
}

// sudoListenerWarning explains why the sudo MFA listener never connected, or returns ""
// when the failure is not worth telling the user about: a 404 is an older server without
// the events API, and the websh session runs fine without it. The server's own text is
// sanitized because this command is about to put the terminal into raw mode.
func sudoListenerWarning(cause error) string {
	if utils.HTTPStatusCode(cause) == http.StatusNotFound {
		return ""
	}
	reason := "timed out connecting to the event channel"
	if cause != nil {
		reason = utils.SanitizeTerminalText(cause.Error())
	}
	return fmt.Sprintf("Sudo MFA listener unavailable: %s", reason)
}
