package server

import (
	serverAPI "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenDeleteCmd = &cobra.Command{
	Use:     "delete TOKEN",
	Aliases: []string{"rm"},
	Short:   "Delete a server registration token",
	Long: `Delete a server registration token by name.
The token is resolved by name—UUID input is not supported.`,
	Example: `
    alpacon server token delete my-token
    alpacon server token rm my-token
    alpacon server token delete my-token -y`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete registration token '%s'?", name)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := serverAPI.DeleteRegistrationToken(alpaconClient, name); err != nil {
			utils.CliErrorWithExit("Failed to delete the registration token: %s.", err)
		}

		utils.CliSuccess("Registration token deleted: %s", name)
	},
}

func init() {
	tokenDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
