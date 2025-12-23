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
	Use:   "websh [SERVER NAME] [COMMAND]",
	Short: "Open a websh terminal or execute a command on a server",
	Long: ` 
	This command either opens a websh terminal for interacting with the specified server or executes a specified command directly on the server.
	It provides a terminal interface for managing and controlling the server remotely or for executing commands and retrieving their output directly.
	For executing commands, it is highly recommended to wrap the entire command string in quotes to ensure it is interpreted correctly on the remote server.
	`,
	Example: `
	// Open a websh terminal for a server
	alpacon websh [SERVER_NAME]

	// Execute a command directly on a server and retrieve the output
	alpacon websh [SERVER_NAME] [COMMAND]
	
	// Set the environment variable 'KEY' to 'VALUE' for the command.
	alpacon websh --env="KEY1=VALUE1" --env="KEY2=VALUE2" [SERVER NAME] [COMMAND]

	// Use the current shell's value for the environment variable 'KEY'.
	alpacon websh --env="KEY" [SERVER NAME] [COMMAND]

	// Open a websh terminal as a root user
	alpacon websh -r [SERVER_NAME]
	alapcon websh -u root [SERVER_NAME]
	
	// Open a websh terminal specifying username and groupname
	alpacon websh -u [USER_NAME] -g [GROUP_NAME] [SERVER_NAME]

	// Run a command as [USER_NAME]/[GROUP_NAME]
	alpacon websh -u [USER_NAME] -g [GROUP_NAME] [SERVER_NAME] [COMMAND]
	
	// Open a websh terminal and share the current terminal to others via a temporary link
	alpacon websh [SERVER NAME] --share
	alpacon websh [SERVER NAME] --share --read-only true
	
	// Join an existing shared session 
	alpacon websh join --url [SHARED_URL] --password [PASSWORD]

	Flags:
	-r          					   Run the websh terminal as the root user.
	-u / --username [USER_NAME]        Specify the username under which the command should be executed.
	-g / --groupname [GROUP_NAME]      Specify the group name under which the command should be executed.
	--env="KEY=VALUE"                  Set the environment variable 'KEY' to 'VALUE' for the command.
	--env="KEY"                        Use the current shell's value for the environment variable 'KEY'.	
	
	-s, --share                        Share the current terminal to others via a temporary link.
	--url [SHARED_URL]                 Specify the URL of the shared session to join.
	-p, --password [PASSWORD]          Specify the password required to access the shared session.
	--read-only [true|false]           Set the shared session to read-only mode (default is false).

	Note:
	- All flags must be placed before the [SERVER_NAME].
	- The -u (or --username) and -g (or --groupname) flags require an argument specifying the user or group name, respectively.
	`,
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
			case args[i] == "-r" || args[i] == "--root":
				username = "root"
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
