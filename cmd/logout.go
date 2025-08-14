package cmd

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out of Alpacon",
	Long:  "Log out of Alpacon. This command removes your authentication credentials stored locally on your system.",
	Example: `
	alpacon logout
	`,
	Run: func(cmd *cobra.Command, args []string) {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon failed: %s. Consider re-logging.", err)
		}

		err = auth.LogoutAndDeleteCredentials(alpaconClient)
		if err != nil {
			utils.CliError("Log out from Alpacon failed: %s.", err)
		}
		fmt.Println("Logout succeeded!")
	},
}
