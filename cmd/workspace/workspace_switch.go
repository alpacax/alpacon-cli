package workspace

import (
	"fmt"

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

		// The env endpoint needs no credential, so the answer is in hand before
		// the switch and lands in the same save as the workspace it describes.
		kubernetesSurface := fetchKubernetesSurface(newURL, newName, cfg.Insecure)

		if err := commitSwitch(cfg, newURL, newName, kubernetesSurface, verifyConnection); err != nil {
			utils.CliErrorWithExit("%s", err)
		}

		utils.CliSuccess("Switched to workspace %q (%s)", newName, newURL)
	},
}

func verifyConnection() error {
	_, err := client.NewAlpaconAPIClient()
	return err
}

// commitSwitch saves the new workspace with its Kubernetes surface answer, then runs verify;
// on failure it restores orig in one save, so no save pairs a workspace with another's answer.
func commitSwitch(orig config.Config, newURL, newName string, kubernetesSurface bool, verify func() error) error {
	if err := config.SwitchWorkspace(newURL, newName, kubernetesSurface); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	if err := verify(); err != nil {
		if revertErr := config.RestoreWorkspace(orig.WorkspaceURL, orig.WorkspaceName, orig.SchemaName, orig.KubernetesSurface); revertErr != nil {
			return fmt.Errorf("failed to connect to %q and could not revert config: %w (original error: %s)", newName, revertErr, err)
		}
		return fmt.Errorf("failed to connect to workspace %q: %w; reverted to %q", newName, err, orig.WorkspaceIdentity())
	}

	return nil
}

// fetchKubernetesSurface asks the target workspace, since a switch can cross regions
// and the login's answer does not carry over; an unreachable endpoint reads as no.
func fetchKubernetesSurface(workspaceURL, workspaceName string, insecure bool) bool {
	envInfo, err := auth0.FetchAuthEnv(workspaceURL, httpclient.New(insecure))
	if err != nil {
		utils.CliWarning("Could not check Kubernetes support on workspace %q: %s. 'alpacon kube' stays hidden until you run 'alpacon login'.", workspaceName, err)
		return false
	}
	return envInfo.Surfaces.Kubernetes
}
