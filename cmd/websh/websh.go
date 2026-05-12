package websh

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	execCmd "github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
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
		case strings.HasPrefix(args[i], "--env"):
			i = extractEnvValue(args, i, res.Env)
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

var WebshCmd = &cobra.Command{
	Use:   "websh [flags] [USER@]SERVER [COMMAND]",
	Short: "Open a websh terminal or execute a command on a server",
	Long: `Open a websh terminal for interacting with a server or execute a command directly on the server.
Supports SSH-like user@host syntax for specifying the username inline.
For executing commands, it is highly recommended to wrap the entire command string in quotes
to ensure it is interpreted correctly on the remote server.`,
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

  # Set environment variables
  alpacon websh --env="KEY1=VALUE1" --env="KEY2=VALUE2" my-server "echo $KEY1"

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
  --env="KEY=VALUE"                  Set environment variable 'KEY' to 'VALUE'.
  --env="KEY"                        Use the current shell's value for 'KEY'.
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

		workSessionID := worksession.ResolveAndAnnounce(parsed.WorkSessionID)

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if len(commandArgs) > 0 {
			if len(commandArgs) > 1 {
				utils.CliWarning("Command without quotes may cause unexpected behavior. Consider wrapping the command in quotes.")
				if !utils.CommandConfirm() {
					return
				}
			}
			command := strings.Join(commandArgs, " ")
			result, err := execCmd.RunCommandWithRetry(alpaconClient, serverName, command, username, groupname, env, workSessionID)
			if err != nil {
				utils.CliErrorWithExit("%s", err)
			}
			fmt.Println(result)
		} else {
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

	if err := event.SubscribeSudoEvent(ac, eventSession.ChannelID, sessionID); err != nil {
		listener.Stop()
		if !isNotFoundError(err) {
			utils.CliWarning("Sudo MFA listener unavailable: %s", err)
		}
		return nil
	}

	return listener
}

func extractEnvValue(args []string, i int, env map[string]string) int {
	envString := strings.TrimPrefix(args[i], "--env=")
	envString = strings.Trim(envString, "\"")

	parts := strings.SplitN(envString, "=", 2)
	if len(parts) == 2 {
		env[parts[0]] = parts[1]
	} else if len(parts) == 1 {
		value, exists := os.LookupEnv(parts[0])
		if !exists {
			utils.CliWarning("No environment variable found for key '%s'\n", parts[0])
		} else {
			env[parts[0]] = value
		}
	} else {
		utils.CliErrorWithExit("Invalid format for --env flag. Expected '--env=KEY=VALUE', but got '%s'. Please use the format: --env=MY_VAR=my_value", args[i])
	}

	return i
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
