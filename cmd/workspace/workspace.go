package workspace

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewWorkspaceCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "View, list, and switch workspaces",
		Long:    "View the current workspace, list available workspaces, and switch between them.",
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig()
			if err != nil {
				return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
			}

			ios.Info("Current workspace: %s (%s)", cfg.WorkspaceName, cfg.WorkspaceURL)

			if cfg.IsMultiWorkspaceMode() {
				ios.Info("Base domain: %s", cfg.BaseDomain)
				ios.Info("Run 'alpacon workspace ls' to list all workspaces")
				ios.Info("Run 'alpacon workspace switch <name>' to switch workspaces")
			}
			return nil
		}),
	}
	cmd.AddCommand(
		newWorkspaceListCmd(ios),
		newWorkspaceSwitchCmd(newClient, ios),
		newWorkspacePreferencesCmd(newClient, ios),
		newWorkspaceAccessControlCmd(newClient, ios),
		newWorkspaceAuthenticationCmd(newClient, ios),
		newWorkspaceMFAMethodsCmd(newClient, ios),
		newWorkspaceUsageCmd(newClient, ios),
	)
	return cmd
}
