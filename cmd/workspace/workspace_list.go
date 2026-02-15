package workspace

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List available workspaces",
	Long:    "Display all workspaces associated with your account.",
	Example: `
	alpacon workspace ls
	alpacon ws list
	`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig()
		if err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' first.")
		}

		if !cfg.IsMultiWorkspaceMode() {
			fmt.Printf("Current workspace: %s (%s)\n", cfg.WorkspaceName, cfg.WorkspaceURL)
			fmt.Println("Workspace listing is available for Auth0-based logins only.")
			return
		}

		entries, err := workspace.GetWorkspaceList(cfg)
		if err != nil {
			utils.CliErrorWithExit("Failed to list workspaces: %s", err)
		}

		utils.PrintTable(entries)
	},
}
