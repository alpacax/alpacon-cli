// Package acl implements 'alpacon acl', which manages the command pre-approval
// ledger held by the command signer.
package acl

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
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

// workspaceClient connects to the workspace API, which resolves the names the
// ledger stores as ids.
func workspaceClient() (*client.AlpaconClient, error) {
	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		return nil, fmt.Errorf("connection to Alpacon API failed: %w", err)
	}
	return ac, nil
}

// resolveServiceToken turns a service token name into the id the ledger
// stores; an id passes through without a lookup.
func resolveServiceToken(ref string) (string, error) {
	if utils.IsUUID(strings.TrimSpace(ref)) {
		return strings.TrimSpace(ref), nil
	}
	ac, err := workspaceClient()
	if err != nil {
		return "", err
	}
	return auth.ResolveServiceTokenID(ac, ref)
}

// resolveServerScope turns --server values into the ids the ledger stores. An
// id passes through, because a listed entry prints ids where a server has been
// deleted; anything else is a server name resolved through the workspace API.
func resolveServerScope(refs []string) ([]string, error) {
	ids, names := partitionServerRefs(refs)
	if len(names) == 0 {
		return ids, nil
	}

	ac, err := workspaceClient()
	if err != nil {
		return nil, err
	}
	resolved, err := server.ResolveServerNames(ac, names)
	if err != nil {
		return nil, err
	}
	return append(ids, resolved...), nil
}

// partitionServerRefs splits --server values into ids and names, dropping the
// empty strings a trailing comma leaves behind.
func partitionServerRefs(refs []string) (ids []string, names []string) {
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		switch {
		case ref == "":
			continue
		case utils.IsUUID(ref):
			ids = append(ids, ref)
		default:
			names = append(names, ref)
		}
	}
	return ids, names
}

// workspaceNames maps service token and server ids to names for a listing. It
// is best effort: a listing is still correct with ids, so a failure here only
// costs readability and is reported under ALPACON_DEBUG.
func workspaceNames() (tokenNames, serverNames map[string]string) {
	ac, err := workspaceClient()
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
