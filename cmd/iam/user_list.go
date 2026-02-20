package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "Display a list of all users",
	Long: `
	Display a detailed list of all users registered in the Alpacon.
	This command provides information such as name, email, status, and other relevant details.
	`,
	Example: `
	alpacon user ls
	alpacon user list
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		userList, err := iam.GetUserList(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the user list: %s.", err)
		}

		utils.PrintTable(userList)
	},
}
