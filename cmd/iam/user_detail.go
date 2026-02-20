package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userDetailCmd = &cobra.Command{
	Use:     "describe [USER NAME]",
	Aliases: []string{"desc"},
	Short:   "Display detailed information about a specific user",
	Long: `
	The describe command fetches and displays detailed information about a specific user, 
	including its description, shell and other relevant attributes. 
	`,
	Example: `
	alpacon user describe john
	alpacon user desc john
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		userName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		userId, err := iam.GetUserIDByName(alpaconClient, userName)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the user details: %s. Please check if the username is correct and try again.", err)
		}

		userDetail, err := iam.GetUserDetail(alpaconClient, userId)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the user details: %s.", err)
		}

		utils.PrintJson(userDetail)
	},
}
