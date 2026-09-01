package workspace

import (
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// errorCallbacks is the callback set the workspace update commands hand to
// utils.HandleCommonErrors. It is mfa.WorkspaceErrorCallbacks under a local name:
// the two update commands here shared it before any other package needed the same
// shape, and 'alpacon user role' now does.
func errorCallbacks(ac *client.AlpaconClient, retry func() error) utils.ErrorHandlerCallbacks {
	return mfa.WorkspaceErrorCallbacks(ac, retry)
}
