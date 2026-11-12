package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityUpdateCmd = &cobra.Command{
	Use:   "update AUTHORITY",
	Short: "Update the authority information",
	Long: `
	Update the certificate authority information in the Alpacon.
	This command opens your editor with the current authority data, allowing you to modify fields such as
	valid days, organization, and other configuration. Not all fields may be editable depending on your permissions.
	After saving, the updated authority information is displayed for verification.
	`,
	Example: `
	alpacon authority update "Root CA"
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		authorityName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authorityID, err := cert.GetAuthorityIDByName(alpaconClient, authorityName)
		if err != nil {
			return utils.NewExitError("Failed to find authority: %s.", err)
		}

		authorityDetail, err := cert.UpdateAuthority(alpaconClient, authorityID)
		if err != nil {
			if utils.ReportNoChanges(err) {
				return nil
			}
			return utils.NewExitError("Failed to update the authority info: %s.", err)
		}

		utils.CliSuccess("Authority updated: %s", authorityName)
		return utils.PrintJson(authorityDetail)
	}),
}
