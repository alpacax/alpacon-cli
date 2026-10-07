package workspace

import (
	"errors"

	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceSwitchCmd = &cobra.Command{
	Use:   "switch WORKSPACE",
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

		if cfg.WorkspaceIdentity() == targetName {
			utils.CliInfoWithExit("Already on workspace %q.", targetName)
			return
		}

		newURL, newName, err := workspace.ValidateAndBuildWorkspaceURL(cfg, targetName)
		if err != nil {
			utils.CliErrorWithExit("%s", err)
		}

		// Save original values for rollback
		origURL := cfg.WorkspaceURL
		origName := cfg.WorkspaceIdentity()
		origHostLabel := cfg.WorkspaceName
		origSchemaName := cfg.SchemaName
		origKubernetesSurface := cfg.KubernetesSurface

		if err := config.SwitchWorkspace(newURL, newName); err != nil {
			utils.CliErrorWithExit("Failed to update config: %s", err)
		}

		// Verify connectivity to the new workspace
		_, err = client.NewAlpaconAPIClient()
		if err != nil {
			// Revert to the original workspace
			if revertErr := config.RestoreWorkspace(origURL, origHostLabel, origSchemaName, origKubernetesSurface); revertErr != nil {
				utils.CliErrorWithExit("Failed to connect to %q and could not revert config: %s (original error: %s)", newName, revertErr, err)
			}
			utils.CliErrorWithExit("Failed to connect to workspace %q: %s. Reverted to %q.", newName, err, origName)
		}

		if errors.Is(refreshKubernetesSurface(newURL, newName, cfg.Insecure), config.ErrWorkspaceChanged) {
			utils.CliErrorWithExit("Switched to workspace %q, but another alpacon process has since changed the current workspace. Run 'alpacon workspace list' to see which one is current.", newName)
		}

		utils.CliSuccess("Switched to workspace %q (%s)", newName, newURL)
	},
}

// refreshKubernetesSurface asks the workspace just switched to whether it
// exposes the Kubernetes surface; the value the login recorded belongs to the
// workspace it logged in to, and a switch can cross regions. SwitchWorkspace
// has already recorded false, so only a yes needs writing, and a failure
// warns and leaves it false rather than undoing a switch that has happened.
// It returns config.ErrWorkspaceChanged when another process has moved the
// config off this workspace meanwhile, so the switch no longer stands.
func refreshKubernetesSurface(workspaceURL, workspaceName string, insecure bool) error {
	envInfo, err := auth0.FetchAuthEnv(workspaceURL, httpclient.New(insecure))
	if err != nil {
		utils.CliWarning("Could not check Kubernetes support on workspace %q: %s. 'alpacon kube' stays hidden until the next login or switch.", workspaceName, err)
		return nil
	}

	err = config.SetKubernetesSurface(workspaceURL, envInfo.Surfaces.Kubernetes)
	if errors.Is(err, config.ErrWorkspaceChanged) {
		return err
	}
	if err != nil {
		utils.CliWarning("Could not save whether workspace %q supports Kubernetes: %s. 'alpacon kube' stays hidden until the next login or switch.", workspaceName, err)
	}
	return nil
}
