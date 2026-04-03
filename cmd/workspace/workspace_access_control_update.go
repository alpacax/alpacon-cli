package workspace

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceAccessControlUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update workspace access control settings",
	Long: `Update workspace access control settings by opening the current settings in your editor.
Modify the desired fields, save, and close the editor to apply changes.`,
	Example: `
	alpacon workspace access-control update
	alpacon ws acl update`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		data, err := workspace.EditAccessControl(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to prepare access control update: %s.", err)
		}

		var accessControlDetail []byte
		accessControlDetail, err = workspace.PatchAccessControl(alpaconClient, data)
		if err != nil {
			err = utils.HandleCommonErrors(err, "", utils.ErrorHandlerCallbacks{
				OnMFARequired: func(_ string) error {
					cfg, loadErr := config.LoadConfig()
					if loadErr != nil {
						return fmt.Errorf("failed to load configuration: %w", loadErr)
					}
					mfaURL, mfaErr := mfa.GetWorkspaceSecurityMFALink(alpaconClient, cfg.WorkspaceName)
					if mfaErr != nil {
						return mfaErr
					}
					fmt.Fprintf(os.Stderr, "\nMFA authentication required. Please visit:\n%s\n\n", mfaURL)
					utils.OpenBrowser(mfaURL)
					return nil
				},
				CheckMFACompleted: func() (bool, error) {
					return mfa.CheckMFACompletion(alpaconClient)
				},
				RefreshToken: alpaconClient.RefreshToken,
				RetryOperation: func() error {
					accessControlDetail, err = workspace.PatchAccessControl(alpaconClient, data)
					return err
				},
			})
			if err != nil {
				utils.CliErrorWithExit("Failed to update access control settings: %s.", err)
			}
		}

		utils.CliSuccess("Access control settings updated.")
		utils.PrintJson(accessControlDetail)
	},
}
