package websh

import (
	"fmt"
	"os"
	"strings"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

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
  alpacon websh my-server --share
  alpacon websh my-server --share --read-only true

  # Join an existing shared session
  alpacon websh join --url https://myws.ap1.alpacon.io/websh/shared/abcd1234 --password my-session-pass

Flags:
  -u, --username [USER_NAME]         Specify the username for command execution.
  -g, --groupname [GROUP_NAME]       Specify the group name for command execution.
  --env="KEY=VALUE"                  Set environment variable 'KEY' to 'VALUE'.
  --env="KEY"                        Use the current shell's value for 'KEY'.
  -s, --share                        Share the terminal via a temporary link.
  --url [SHARED_URL]                 URL of the shared session to join.
  -p, --password [PASSWORD]          Password for the shared session.
  --read-only [true|false]           Set shared session to read-only (default: false).

Note: All flags must be placed before the server name.
      Flags placed after the server name are treated as part of the remote command.`,
	// DisableFlagParsing is required because positional args after the server name
	// (e.g., "ls -la") would otherwise be consumed by Cobra's flag parser.
	// As a trade-off, we parse all flags manually in the Run function.
	// Flags after the server name are intentionally treated as remote command args.
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		var (
			username, groupname, serverName, url, password string
			commandArgs                                    []string
			share, readOnly                                bool
		)

		env := make(map[string]string)

		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "-s" || args[i] == "--share":
				share = true
			case args[i] == "-h" || args[i] == "--help":
				_ = cmd.Help()
				return
			case strings.HasPrefix(args[i], "-u") || strings.HasPrefix(args[i], "--username"):
				username, i = extractValue(args, i)
			case strings.HasPrefix(args[i], "-g") || strings.HasPrefix(args[i], "--groupname"):
				groupname, i = extractValue(args, i)
			case strings.HasPrefix(args[i], "--url"):
				url, i = extractValue(args, i)
			case strings.HasPrefix(args[i], "-p") || strings.HasPrefix(args[i], "--password"):
				password, i = extractValue(args, i)
			case strings.HasPrefix(args[i], "--env"):
				i = extractEnvValue(args, i, env)
			case strings.HasPrefix(args[i], "--read-only"):
				var value string
				value, i = extractValue(args, i)
				if value == "" || strings.TrimSpace(strings.ToLower(value)) == "true" {
					readOnly = true
				} else if strings.TrimSpace(strings.ToLower(value)) == "false" {
					readOnly = false
				} else {
					utils.CliErrorWithExit("The 'read only' value must be either 'true' or 'false'.")
				}
			default:
				if serverName == "" {
					serverName = args[i]
				} else {
					commandArgs = append(commandArgs, args[i])
				}
			}
		}

		if serverName == "" {
			utils.CliErrorWithExit("Server name is required.")
		}

		// Parse SSH-like syntax for user@host
		if strings.Contains(serverName, "@") && !strings.Contains(serverName, ":") {
			sshTarget := utils.ParseSSHTarget(serverName)
			if username == "" && sshTarget.User != "" {
				username = sshTarget.User
			}
			serverName = sshTarget.Host
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if serverName == "join" {
			if url == "" || password == "" {
				utils.CliErrorWithExit("Both URL and password are required.")
			}
			session, err := websh.JoinWebshSession(alpaconClient, url, password)
			if err != nil {
				utils.CliErrorWithExit("Failed to join the session: %s.", err)
			}
			_ = websh.OpenNewTerminal(alpaconClient, session)
		} else if len(commandArgs) > 0 {
			if len(commandArgs) > 1 {
				utils.CliWarning("Command without quotes may cause unexpected behavior. Consider wrapping the command in quotes.")
				confirm := utils.CommandConfirm()
				if !confirm {
					os.Exit(1)
				}
			}
			command := strings.Join(commandArgs, " ")
			result, err := event.RunCommand(alpaconClient, serverName, command, username, groupname, env)
			if err != nil {
				err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
					OnMFARequired: func(srv string) error {
						return mfa.HandleMFAError(alpaconClient, srv)
					},
					OnUsernameRequired: func() error {
						_, err := iam.HandleUsernameRequired()
						return err
					},
					RetryOperation: func() error {
						result, err = event.RunCommand(alpaconClient, serverName, command, username, groupname, env)
						return err
					},
				})

				if err != nil {
					utils.CliErrorWithExit("Failed to run the command on the '%s' server: %s.", serverName, err)
				}
			}
			fmt.Println(result)
		} else {
			session, err := websh.CreateWebshSession(alpaconClient, serverName, username, groupname, share, readOnly)

			if err != nil {
				err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
					OnMFARequired: func(srv string) error {
						return mfa.HandleMFAError(alpaconClient, srv)
					},
					OnUsernameRequired: func() error {
						_, err := iam.HandleUsernameRequired()
						return err
					},
					RetryOperation: func() error {
						session, err = websh.CreateWebshSession(alpaconClient, serverName, username, groupname, share, readOnly)
						return err
					},
				})

				if err != nil {
					utils.CliErrorWithExit("Failed to create websh session for '%s' server: %s.", serverName, err)
				}
			}
			_ = websh.OpenNewTerminal(alpaconClient, session)
		}
	},
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
