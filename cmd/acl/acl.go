// Package acl implements 'alpacon acl', which manages the command pre-approval
// ledger held by the command signer.
package acl

import (
	"errors"
	"strings"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var AclCmd = &cobra.Command{
	Use:   "acl",
	Short: "Manage pre-approved commands",
	Long: `Manage the command pre-approval ledger: the pre-approved commands a service
token may run without each run being judged again, and the Command ACL admins
who may change them. The web console shows the same entries under
Pre-approved commands on a service token's page. A personal API token has no
command list on the ledger.

The ledger is kept by the command signer rather than the workspace, so the
first acl command asks you to authorize it in the browser, the same way
'alpacon login' does. That authorization is not renewed in the background:
when it expires, the next acl command asks for it again.

Only a Command ACL admin of the workspace may read or change the ledger, and
only with their own sign-in—an API token cannot. Workspaces that do not use the
ledger keep managing command ACLs with 'alpacon token acl command'.

Set ALPACON_AI_URL to reach a self-hosted command signer; Alpacon Cloud
workspaces need no configuration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	AclCmd.AddCommand(entryCmd)
	AclCmd.AddCommand(adminCmd)
}

// errEmptyServerScope refuses an empty server reference. An empty scope means
// every server in the workspace, so dropping the empty value would widen the
// entry past what the caller wrote.
var errEmptyServerScope = errors.New("a server value is empty; name each server, or leave the scope out on purpose to cover every server")

// resolveServiceToken turns a service token name or id into the id the ledger
// stores, looked up in the session's workspace. An id is confirmed to be a
// service token's, so a personal API token's id is refused.
func (s *ledgerSession) resolveServiceToken(ref string) (string, error) {
	ac, err := s.workspaceAPI()
	if err != nil {
		return "", err
	}
	return auth.ResolveServiceTokenID(ac, ref)
}

// listFilterID is resolveServiceToken for a listing filter: an id passes
// through, since filtering by an id that is not a service token's only lists
// nothing, and existing entries of a personal API token stay listable.
func (s *ledgerSession) listFilterID(ref string) (string, error) {
	if ref = strings.TrimSpace(ref); utils.IsUUID(ref) {
		return ref, nil
	}
	return s.resolveServiceToken(ref)
}

// resolveServerScope turns --server values into the ids the ledger stores. An
// id passes through, because a listed entry prints ids where a server has been
// deleted; anything else is a server name resolved through the session's
// workspace API.
func (s *ledgerSession) resolveServerScope(refs []string) ([]string, error) {
	ids, names, err := partitionServerRefs(refs)
	if err != nil || len(names) == 0 {
		return ids, err
	}

	ac, err := s.workspaceAPI()
	if err != nil {
		return nil, err
	}
	resolved, err := server.ResolveServerNames(ac, names)
	if err != nil {
		return nil, err
	}
	return append(ids, resolved...), nil
}

// partitionServerRefs splits --server values into ids and names. An empty
// value is refused rather than dropped: an empty scope means every server in
// the workspace, so dropping a stray empty value could leave a scope that
// covers far more than the caller wrote.
func partitionServerRefs(refs []string) (ids []string, names []string, err error) {
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		switch {
		case ref == "":
			return nil, nil, errEmptyServerScope
		case utils.IsUUID(ref):
			ids = append(ids, ref)
		default:
			names = append(names, ref)
		}
	}
	return ids, names, nil
}

// workspaceNames maps service token and server ids to names for a listing. It
// is best effort: a listing is still correct with ids, so a failure here only
// costs readability and is reported under ALPACON_DEBUG.
func (s *ledgerSession) workspaceNames() (tokenNames, serverNames map[string]string) {
	ac, err := s.workspaceAPI()
	if err != nil {
		utils.CliDebug("showing ids instead of names: %v", err)
		return nil, nil
	}
	if tokenNames, err = auth.GetServiceTokenNames(ac); err != nil {
		utils.CliDebug("showing service token ids instead of names: %v", err)
	}
	if serverNames, err = server.GetServerNames(ac); err != nil {
		utils.CliDebug("showing server ids instead of names: %v", err)
	}
	return tokenNames, serverNames
}
