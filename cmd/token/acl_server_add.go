package token

import (
	"errors"
	"strings"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	serverapi "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newAclServerAddCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add TOKEN",
		Short: "Grant a token access to one or more servers",
		Long: `Grant an API token access to servers. Without a ServerACL entry,
the token is denied access to all servers (deny-by-default).

Use --server for a single server or --servers for bulk operations.`,
		Example: `  alpacon token acl server add my-api-token --server my-server
  alpacon token acl server add my-api-token --servers web-01,web-02,web-03`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runServerAclAdd(ios, cmd, args, newClient)
		}),
	}
	cmd.Flags().String("server", "", "Server name (single)")
	cmd.Flags().String("servers", "", "Comma-separated server names (bulk)")
	return cmd
}

func runServerAclAdd(ios *utils.IOStreams, cmd *cobra.Command, args []string, newClient clientFactory) error {
	tokenArg := args[0]
	serverName, _ := cmd.Flags().GetString("server")
	serversCSV, _ := cmd.Flags().GetString("servers")

	serverName, serversCSV, err := validateServerAclFlags(serverName, serversCSV)
	if err != nil {
		return utils.NewUsageExitError("%s.", err)
	}

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	tokenID, err := auth.ResolveTokenID(alpaconClient, tokenArg)
	if err != nil {
		return utils.NewExitError("Failed to resolve token: %v.", err)
	}

	if serverName != "" {
		serverID, err := serverapi.GetServerIDByName(alpaconClient, serverName)
		if err != nil {
			return utils.NewExitError("Failed to resolve server '%s': %v.", serverName, err)
		}
		if err = security.AddServerAcl(alpaconClient, security.ServerAclRequest{
			Token:  tokenID,
			Server: serverID,
		}); err != nil {
			return utils.NewExitError("Failed to add server ACL: %v.", err)
		}
		ios.Success("Server ACL added: token %s can access %s", tokenArg, serverName)
		return nil
	}

	names := utils.SplitAndTrim(serversCSV, ",")
	if len(names) == 0 {
		return utils.NewUsageExitError("--servers must contain at least one server name.")
	}

	serverIDs, err := resolveServerIDs(alpaconClient, names)
	if err != nil {
		return utils.NewExitError("%v.", err)
	}

	if err = security.BulkAddServerAcl(alpaconClient, security.ServerAclBulkRequest{
		Token:   tokenID,
		Servers: serverIDs,
	}); err != nil {
		return utils.NewExitError("Failed to bulk-add server ACLs: %v.", err)
	}
	ios.Success("Server ACLs added: token %s can access [%s]", tokenArg, strings.Join(names, ", "))
	return nil
}

// validateServerAclFlags trims both values so a whitespace-only flag counts as absent, and
// hands the trimmed ones back so the caller resolves the same name it validated.
func validateServerAclFlags(serverName, serversCSV string) (string, string, error) {
	serverName = strings.TrimSpace(serverName)
	serversCSV = strings.TrimSpace(serversCSV)

	if serverName == "" && serversCSV == "" {
		return serverName, serversCSV, errors.New("one of --server or --servers is required")
	}
	if serverName != "" && serversCSV != "" {
		return serverName, serversCSV, errors.New("use either --server or --servers, not both")
	}

	return serverName, serversCSV, nil
}
