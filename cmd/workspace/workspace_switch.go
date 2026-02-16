package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceSwitchCmd = &cobra.Command{
	Use:   "switch <workspace-name>",
	Short: "Switch to a different workspace",
	Long:  "Switch to another workspace in your account. The workspace must exist in your JWT token.",
	Example: `
	alpacon workspace switch my-other-workspace
	alpacon ws switch staging
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetName := args[0]

		cfg, err := config.LoadConfig()
		if err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' first.")
		}

		if !cfg.IsMultiWorkspaceMode() {
			utils.CliErrorWithExit("Workspace switching is available for Auth0-based logins only. Re-login with 'alpacon login <workspace_url>' to enable multi-workspace mode.")
		}

		if cfg.WorkspaceName == targetName {
			utils.CliInfoWithExit("Already on workspace %q.", targetName)
			return
		}

		newURL, newName, err := workspace.ValidateAndBuildWorkspaceURL(cfg, targetName)
		if err != nil {
			utils.CliErrorWithExit("%s", err)
		}

		// Save original values for rollback
		origURL := cfg.WorkspaceURL
		origName := cfg.WorkspaceName

		if err := config.SwitchWorkspace(newURL, newName); err != nil {
			utils.CliErrorWithExit("Failed to update config: %s", err)
		}

		// Verify connectivity to the new workspace
		_, err = client.NewAlpaconAPIClient()
		if err != nil {
			// Revert to the original workspace
			if revertErr := config.SwitchWorkspace(origURL, origName); revertErr != nil {
				utils.CliErrorWithExit("Failed to connect to %q and could not revert config: %s (original error: %s)", newName, revertErr, err)
			}
			utils.CliErrorWithExit("Failed to connect to workspace %q: %s. Reverted to %q.", newName, err, origName)
		}

		utils.CliSuccess("Switched to workspace %q (%s)", newName, newURL)
	},
}
