package workspace

import (
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var WorkspaceCmd = &cobra.Command{
	Use:     "workspace",
	Aliases: []string{"ws"},
	Short:   "View, list, and switch workspaces",
	Long:    "View the current workspace, list available workspaces, and switch between them.",
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
		}

		utils.CliInfo("Current workspace: %s (%s)", cfg.WorkspaceName, cfg.WorkspaceURL)

		if cfg.IsMultiWorkspaceMode() {
			utils.CliInfo("Base domain: %s", cfg.BaseDomain)
			utils.CliInfo("Run 'alpacon workspace ls' to list all workspaces")
			utils.CliInfo("Run 'alpacon workspace switch <name>' to switch workspaces")
		}
		return nil
	}),
}

func init() {
	WorkspaceCmd.AddCommand(workspaceListCmd)
	WorkspaceCmd.AddCommand(workspaceSwitchCmd)
	WorkspaceCmd.AddCommand(workspacePreferencesCmd)
	WorkspaceCmd.AddCommand(workspaceAccessControlCmd)
	WorkspaceCmd.AddCommand(workspaceAuthenticationCmd)
	WorkspaceCmd.AddCommand(workspaceMFAMethodsCmd)
	WorkspaceCmd.AddCommand(workspaceUsageCmd)
}
