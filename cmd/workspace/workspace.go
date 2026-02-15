package workspace

import (
	"fmt"

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

		fmt.Printf("Current workspace: %s\n", cfg.WorkspaceName)
		fmt.Printf("URL: %s\n", cfg.WorkspaceURL)

		if cfg.IsMultiWorkspaceMode() {
			fmt.Printf("Base domain: %s\n", cfg.BaseDomain)
			fmt.Println("\nUse 'alpacon workspace ls' to list all workspaces.")
			fmt.Println("Use 'alpacon workspace switch <name>' to switch workspaces.")
		}
	},
}

func init() {
	WorkspaceCmd.AddCommand(workspaceListCmd)
	WorkspaceCmd.AddCommand(workspaceSwitchCmd)
}
