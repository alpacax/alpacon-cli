package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityUpdateCmd = &cobra.Command{
	Use:   "update [AUTHORITY ID]",
	Short: "Update the authority information",
	Long: `
	Update the certificate authority information in the Alpacon.
	This command opens your editor with the current authority data, allowing you to modify fields such as
	valid days, organization, and other configuration. Not all fields may be editable depending on your permissions.
	After saving, the updated authority information is displayed for verification.
	`,
	Example: `
	alpacon authority update 550e8400-e29b-41d4-a716-446655440000
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		authorityId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authorityDetail, err := cert.UpdateAuthority(alpaconClient, authorityId)
		if err != nil {
			utils.CliErrorWithExit("Failed to update the authority info: %s.", err)
		}

		utils.CliSuccess("Authority updated: %s", authorityId)
		utils.PrintJson(authorityDetail)
	},
}
