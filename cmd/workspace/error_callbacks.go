package workspace

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// errorCallbacks is mfa.ErrorCallbacks for the workspace update commands: no
// username, and a workspace-scoped MFA link instead of a server-scoped one.
//
// The workspace name comes off ac, pinned there beside BaseURL. Reading config
// here instead would let another shell's 'alpacon ws use' point the link at a
// workspace this client is not talking to.
func errorCallbacks(ac *client.AlpaconClient, retry func() error) utils.ErrorHandlerCallbacks {
	return utils.ErrorHandlerCallbacks{
		OnMFARequired: func(_ string) error {
			mfaURL, err := mfa.GetWorkspaceSecurityMFALink(ac)
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
