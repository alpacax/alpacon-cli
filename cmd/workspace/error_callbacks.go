package workspace

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

// errorCallbacks returns the callback set the workspace update commands hand to
// utils.HandleCommonErrors. It differs from mfa.ErrorCallbacks in that these
// commands take no username and their MFA link is workspace-scoped rather than
// server-scoped. The retry function re-runs the operation that failed.
func errorCallbacks(ac *client.AlpaconClient, retry func() error) utils.ErrorHandlerCallbacks {
	return utils.ErrorHandlerCallbacks{
		OnMFARequired: func(_ string) error {
			cfg, err := config.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}
			mfaURL, err := mfa.GetWorkspaceSecurityMFALink(ac, cfg.WorkspaceName)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "\nMFA authentication required. Please visit:\n%s\n\n", mfaURL)
			utils.OpenBrowser(mfaURL)
			return nil
		},
		CheckMFACompleted: func() (bool, error) {
			return mfa.CheckMFACompletion(ac)
		},
		RefreshToken:   ac.RefreshToken,
		RetryOperation: retry,
	}
}
