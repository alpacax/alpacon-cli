package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userUpdateCmd = &cobra.Command{
	Use:   "update [USER NAME]",
	Short: "Update the user information",
	Long: `
	Update the user information in the Alpacon.
	This command allows you to update the user's details, such as email, and other personal information within the Alpacon system. 
	However, due to permission restrictions or certain fields being read-only, not all submitted changes may be applied. 
	It's important to understand that modifications to certain information may require higher user privileges.
	Due to these factors, after successfully executing the update command, the return of user information allows for verification of the modifications.
	`,
	Example: `
	alpacon user update [USER_NAME]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		userName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		userDetail, err := iam.UpdateUser(alpaconClient, userName)
		if err != nil {
			utils.CliError("Failed to update the user info: %s.", err)
		}

		utils.CliInfo("%s user successfully updated to alpacon.", userName)
		utils.PrintJson(userDetail)
	},
}
