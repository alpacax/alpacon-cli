package websh

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	execCmd "github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// errHelpRequested signals that -h/--help was encountered during parsing.
// Callers should print help text and exit cleanly.
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

// ParseWebshArgs parses raw CLI args for `alpacon websh` (DisableFlagParsing mode).
// Returns errHelpRequested when -h/--help is seen.
//
// NOTE: --read-only must be checked before generic -r prefixes, and
// --work-session before the default fallthrough.
func ParseWebshArgs(args []string) (WebshArgs, error) {
	res := WebshArgs{Env: map[string]string{}}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-s" || args[i] == "--share":
			res.Share = true
		case args[i] == "-h" || args[i] == "--help":
			return res, errHelpRequested
		case strings.HasPrefix(args[i], "-u") || strings.HasPrefix(args[i], "--username"):
			res.Username, i = extractValue(args, i)
		case strings.HasPrefix(args[i], "-g") || strings.HasPrefix(args[i], "--groupname"):
			res.Groupname, i = extractValue(args, i)
		case args[i] == "--env" || strings.HasPrefix(args[i], "--env="):
			if errMsg := execCmd.ParseEnvArg(args[i], res.Env); errMsg != "" {
				return res, errors.New(errMsg)
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
					return res, fmt.Errorf("the --read-only value must be either 'true' or 'false'")
				}
			} else {
				// Boolean form: --read-only without =.
				// Peek ahead only for explicit "true"/"false"; otherwise treat as true.
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
				return res, fmt.Errorf("--work-session requires a value")
			}
			res.WorkSessionID = ws
			i = newI
		case args[i] == "--output" || strings.HasPrefix(args[i], "--output="):
			val, newI := extractValue(args, i)
			if val == "" {
				return res, fmt.Errorf("--output requires a value (table|json)")
			}
			res.OutputFormat = val
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
	return res, nil
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
	Use:   "websh [flags] [USER@]SERVER [COMMAND]",
	Short: "Open a websh terminal or execute a command on a server",
	Long: `Open a websh terminal for interacting with a server or execute a command directly on the server.
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
When websh runs a command, the server rejects it before it runs if its command
line carries a credential—a -p/--password flag, a KEY=VALUE secret such as
PGPASSWORD=..., or a user:pass@host connection string—with exit code 1. Pass
the secret with --env="KEY" instead; --output json emits an error envelope on
stderr with error_code command_inline_credential.
Requires an active WorkSession when using Browser login (Auth0); Token auth (API token or Service token) bypasses this requirement.`,
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
  read -rs PGPASSWORD && export PGPASSWORD
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
  alpacon websh force-close SESSION_ID      # Force close (admin only)

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
	// DisableFlagParsing is required because positional args after the server name
	// (e.g., "ls -la") would otherwise be consumed by Cobra's flag parser.
	// As a trade-off, we parse all flags manually in the Run function.
	// Flags after the server name are intentionally treated as remote command args.
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		parsed, err := ParseWebshArgs(args)
		if err != nil {
			if errors.Is(err, errHelpRequested) {
				_ = cmd.Help()
				return
			}
			utils.CliErrorWithExit("%s", err)
		}

		username := parsed.Username
		groupname := parsed.Groupname
		serverName := parsed.ServerName
		commandArgs := parsed.CommandArgs
		share := parsed.Share
		readOnly := parsed.ReadOnly
		env := parsed.Env

		if serverName == "" {
			utils.CliErrorWithExit("Server name is required.")
		}

		if share && len(commandArgs) > 0 {
			utils.CliErrorWithExit("The --share flag cannot be used with remote commands. Use --share for interactive sessions only.")
		}

		// Parse SSH-like syntax for user@host
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
				utils.CliErrorWithExit("invalid --output value %q: must be 'table' or 'json'", parsed.OutputFormat)
			}
			utils.OutputFormat = parsed.OutputFormat
		}

		workSessionID := worksession.ResolveOrExit(parsed.WorkSessionID)

		authMethod := config.ResolveAuthMethod()

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		session, err := websh.CreateWebshSession(alpaconClient, serverName, username, groupname, share, readOnly, workSessionID)

		if err != nil {
			err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
				OnMFARequired: func(srv string) error {
					return mfa.HandleMFAError(alpaconClient, srv)
				},
				OnUsernameRequired: func() error {
					_, err := iam.HandleUsernameRequired()
					return err
				},
				CheckMFACompleted: func() (bool, error) {
					return mfa.CheckMFACompletion(alpaconClient)
				},
				RefreshToken: alpaconClient.RefreshToken,
				RetryOperation: func() error {
					session, err = websh.CreateWebshSession(alpaconClient, serverName, username, groupname, share, readOnly, workSessionID)
					return err
				},
			})

			if err != nil {
				utils.HandleWorkSessionError(err, "websh", serverName, authMethod, workSessionID)
				utils.CliErrorWithExit("Failed to create websh session for '%s' server: %s.", serverName, err)
			}
		}
		// Set up sudo MFA listener in background so it doesn't delay
		// terminal open. If the user types sudo before the listener is
		// ready, the approval request will expire and they can retry.
		listenerDone := make(chan *event.SudoListener, 1)
		go func() {
			listenerDone <- setupSudoListener(alpaconClient, session.ID, serverName)
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

		_ = websh.OpenNewTerminal(alpaconClient, session)
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

// setupSudoListener creates an event session, connects the event WebSocket,
// then subscribes to sudo events for the given websh session. The server
// requires the WebSocket to be connected before allowing subscriptions.
// Returns nil if the events API is not available. Silently skips "not found"
// errors (older servers); logs a warning for other failures.
func setupSudoListener(ac *client.AlpaconClient, sessionID, serverName string) *event.SudoListener {
	eventSession, err := event.CreateEventSession(ac)
	if err != nil {
		if !isNotFoundError(err) {
			utils.CliWarning("Sudo MFA listener unavailable: %s", err)
		}
		return nil
	}

	// Start listener first — the server requires the WebSocket channel to be
	// connected before it accepts event subscriptions.
	listener := event.NewSudoListener(ac, eventSession.WebsocketURL, serverName)
	listener.Start()

	if !listener.WaitConnected(5 * time.Second) {
		listener.Stop()
		return nil
	}

	if err := event.SubscribeEvent(ac, eventSession.ChannelID, event.EventTypeSudo, sessionID); err != nil {
		listener.Stop()
		if !isNotFoundError(err) {
			utils.CliWarning("Sudo MFA listener unavailable: %s", err)
		}
		return nil
	}

	return listener
}

// isNotFoundError checks if an error message indicates a 404/not-found response.
// AlpaconClient.SendPostRequest returns the server's error detail (e.g., "Not found.")
// rather than the raw HTTP status code.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.TrimSpace(strings.ToLower(err.Error()))
	return msg == "not found" || msg == "not found." ||
		strings.HasSuffix(msg, ": not found") || strings.HasSuffix(msg, ": not found.")
}
