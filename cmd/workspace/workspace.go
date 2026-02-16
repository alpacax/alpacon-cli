package workspace

import (
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var WorkspaceCmd = &cobra.Command{
	Use:     "workspace",
	Aliases: []string{"ws"},
	Short:   "Commands to manage workspaces",
	Long:    "View, list, and switch between workspaces associated with your account.",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig()
		if err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' first.")
		}

		utils.CliInfo("Current workspace: %s (%s)", cfg.WorkspaceName, cfg.WorkspaceURL)

		if cfg.IsMultiWorkspaceMode() {
			utils.CliInfo("Base domain: %s", cfg.BaseDomain)
			utils.CliInfo("Run 'alpacon workspace ls' to list all workspaces")
			utils.CliInfo("Run 'alpacon workspace switch <name>' to switch workspaces")
		}
	},
}

func init() {
	WorkspaceCmd.AddCommand(workspaceListCmd)
	WorkspaceCmd.AddCommand(workspaceSwitchCmd)
}
