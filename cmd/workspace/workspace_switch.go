package workspace

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var errRefreshRejected = errors.New("refresh token refused")

var workspaceSwitchCmd = &cobra.Command{
	Use:   "switch WORKSPACE",
	Short: "Switch to a different workspace",
	Long:  "Switch to another workspace in your account. A workspace your access token does not list yet, such as one created after the token was issued, is looked up again after one token renewal.",
	Example: `
	alpacon workspace switch my-other-workspace
	alpacon ws switch staging
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		targetName := args[0]

		cfg, err := config.LoadConfig()
		if err != nil {
			return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
		}

		if !cfg.IsMultiWorkspaceMode() {
			return utils.NewExitError("Workspace switching is available for Auth0-based logins only. Re-login with 'alpacon login <workspace_url>' to enable multi-workspace mode.")
		}

		if cfg.WorkspaceIdentity() == targetName {
			utils.CliInfo("Already on workspace %q.", targetName)
			return nil
		}

		newURL, newName, renewed, err := resolveSwitchTarget(cfg, targetName, func(cfg config.Config) (*config.RenewedToken, error) {
			return renewWorkspaceClaim(cfg, httpclient.New(cfg.Insecure))
		})
		if err != nil {
			return utils.NewExitError("%s", err)
		}

		// The env endpoint needs no credential, so the answer is in hand before
		// the switch and lands in the same save as the workspace it describes.
		kubernetesSurface := fetchKubernetesSurface(newURL, newName, cfg.Insecure)

		if err := commitSwitch(cfg, newURL, newName, kubernetesSurface, renewed, verifyConnection); err != nil {
			return utils.NewExitError("%s", err)
		}

		utils.CliSuccess("Switched to workspace %q (%s)", newName, newURL)
		return nil
	}),
}

// resolveSwitchTarget looks targetName up in the token's workspaces claim, fixed when the token is issued,
// so a miss renews the token once, when a refresh token exists, and looks again; renewed is that token.
func resolveSwitchTarget(cfg config.Config, targetName string, renew func(config.Config) (*config.RenewedToken, error)) (newURL, newName string, renewed *config.RenewedToken, err error) {
	newURL, newName, err = workspace.ResolveWorkspaceURL(cfg.AccessToken, targetName, cfg.BaseDomain)
	if !errors.Is(err, workspace.ErrWorkspaceNotInToken) {
		return newURL, newName, nil, err
	}
	if cfg.RefreshToken == "" {
		return "", "", nil, staleWorkspaceList(err)
	}

	renewed, renewErr := renew(cfg)
	if errors.Is(renewErr, errRefreshRejected) {
		return "", "", nil, fmt.Errorf("%w: renewing the access token failed: %w", staleWorkspaceList(err), renewErr)
	}
	if renewErr != nil {
		return "", "", nil, fmt.Errorf("%w, and renewing the access token to look again failed. Please retry: %w", err, renewErr)
	}
	newURL, newName, err = workspace.ResolveWorkspaceURL(renewed.AccessToken, targetName, cfg.BaseDomain)
	if errors.Is(err, workspace.ErrWorkspaceNotInToken) {
		return "", "", nil, fmt.Errorf("%w after renewing the access token. Check the name, or if the workspace was just created, run 'alpacon login' and retry", err)
	}
	if err != nil {
		return "", "", nil, fmt.Errorf("could not read the renewed access token: %w. Run 'alpacon login' and retry", err)
	}
	return newURL, newName, renewed, nil
}

func staleWorkspaceList(err error) error {
	return fmt.Errorf("%w. Your workspace list reflects your last sign-in; run 'alpacon login' and retry", err)
}

// renewWorkspaceClaim runs the refresh-token grant. Its own save is best effort, so the switch saves the token again.
func renewWorkspaceClaim(cfg config.Config, httpClient *http.Client) (*config.RenewedToken, error) {
	spinner := utils.NewSpinner("Refreshing access token...")
	spinner.Start()
	tokenRes, err := auth0.RefreshAccessToken(cfg.WorkspaceURL, httpClient, cfg)
	spinner.Stop()
	if auth0.IsRefreshTokenRejected(err) {
		return nil, fmt.Errorf("%w: %w", errRefreshRejected, err)
	}
	if err != nil {
		return nil, err
	}
	return &config.RenewedToken{AccessToken: tokenRes.AccessToken, ExpiresIn: tokenRes.ExpiresIn}, nil
}

func verifyConnection() error {
	_, err := client.NewAlpaconAPIClient()
	return err
}

// commitSwitch saves the new workspace with its Kubernetes surface answer and any renewed token, then runs verify;
// on failure it restores orig's workspace in one save unless another login or switch has replaced the new one since.
func commitSwitch(orig config.Config, newURL, newName string, kubernetesSurface bool, renewed *config.RenewedToken, verify func() error) error {
	if err := config.SwitchWorkspace(orig, newURL, newName, kubernetesSurface, renewed); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	if err := verify(); err != nil {
		revertErr := config.RestoreWorkspace(newURL, orig)
		if errors.Is(revertErr, config.ErrWorkspaceChanged) || errors.Is(revertErr, config.ErrLoginChanged) {
			return fmt.Errorf("failed to connect to %q: %w; left the config as another login or switch set it since: %w", newName, err, revertErr)
		}
		if revertErr != nil {
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
