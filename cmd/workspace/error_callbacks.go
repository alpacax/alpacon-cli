package workspace

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// errorCallbacks returns the callback set the workspace update commands hand to
// utils.HandleCommonErrors. It differs from mfa.ErrorCallbacks in that these
// commands take no username and their MFA link is workspace-scoped rather than
// server-scoped. The retry function re-runs the operation that failed.
//
// workspaceName comes from the caller's own config read rather than a fresh one
// inside the callback: ac pins its BaseURL at construction from that same read,
// and an editor session sits between it and the MFA prompt. Re-reading here
// would point the link at a workspace the request is no longer going to.
func errorCallbacks(ac *client.AlpaconClient, workspaceName string, retry func() error) utils.ErrorHandlerCallbacks {
	return utils.ErrorHandlerCallbacks{
		OnMFARequired: func(_ string) error {
			mfaURL, err := mfa.GetWorkspaceSecurityMFALink(ac, workspaceName)
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
