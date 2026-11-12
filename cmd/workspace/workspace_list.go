package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List available workspaces",
	Long:    "Display the workspaces your current access token lists. A workspace created after the token was issued appears once the token is renewed, which 'alpacon workspace switch' to it does, or after 'alpacon login'.",
	Example: `
	alpacon workspace ls
	alpacon ws list
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
		}

		if !cfg.IsMultiWorkspaceMode() {
			utils.CliInfo("Current workspace: %s (%s)", cfg.WorkspaceName, cfg.WorkspaceURL)
			utils.CliInfo("Workspace listing is available for Auth0-based logins only.")
			return nil
		}

		entries, err := workspace.GetWorkspaceList(cfg)
		if err != nil {
			return utils.NewExitError("Failed to list workspaces: %s", err)
		}

		utils.PrintTable(entries)
		return nil
	}),
}
