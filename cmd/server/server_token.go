package server

import (
	"errors"
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage server registration tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Run 'alpacon server token --help' for more information")
	},
}

var serverTokenRegenerateCmd = &cobra.Command{
	Use:     "regenerate SERVER",
	Aliases: []string{"regen"},
	Short:   "Regenerate a registration token for a server",
	Long: `
	Regenerate the registration token for the given server.
	The old token is revoked and a new one is issued. The new token is shown only once—save it immediately.
	`,
	Example: `alpacon server token regenerate my-server`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serverName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		response, err := server.RegenerateRegistrationToken(alpaconClient, serverName)
		if err != nil {
			utils.CliErrorWithExit("Failed to regenerate the registration token: %s.", err)
		}

		printRegistrationTokenInfo(response)
	},
}

func init() {
	serverTokenCmd.AddCommand(serverTokenRegenerateCmd)
}

func printRegistrationTokenInfo(response server.ServerCreatedResponse) {
	fmt.Fprintln(os.Stderr)
	utils.PrintHeader("Server registration token created")
	fmt.Fprintf(os.Stderr, "Name:  %s\n", response.Name)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Registration token (shown only once—save it now):")
	fmt.Fprintln(os.Stderr, utils.Green(response.Key))
	fmt.Fprintln(os.Stderr)
	utils.CliWarning("Use this token to register an Alpamon agent. After leaving this page, you cannot retrieve the token again.")
}
