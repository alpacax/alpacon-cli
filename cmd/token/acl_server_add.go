package token

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	serverapi "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclServerAddCmd = &cobra.Command{
	Use:   "add TOKEN",
	Short: "Grant a token access to one or more servers",
	Long: `Grant an API token access to servers. Without a ServerACL entry,
the token is denied access to all servers (deny-by-default).

Use --server for a single server or --servers for bulk operations.`,
	Example: `  alpacon token acl server add my-api-token --server my-server
  alpacon token acl server add my-api-token --servers web-01,web-02,web-03`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tokenArg := args[0]
		serverName, _ := cmd.Flags().GetString("server")
		serversCSV, _ := cmd.Flags().GetString("servers")

		if serverName == "" && serversCSV == "" {
			utils.CliErrorWithExit("One of --server or --servers is required.")
		}
		if serverName != "" && serversCSV != "" {
			utils.CliErrorWithExit("Use either --server or --servers, not both.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		tokenID, err := auth.ResolveTokenID(alpaconClient, tokenArg)
		if err != nil {
			utils.CliErrorWithExit("Failed to resolve token: %v.", err)
		}

		if serverName != "" {
			serverID, err := serverapi.GetServerIDByName(alpaconClient, serverName)
			if err != nil {
				utils.CliErrorWithExit("Failed to resolve server '%s': %v.", serverName, err)
			}
			if err = security.AddServerAcl(alpaconClient, security.ServerAclRequest{
				Token:  tokenID,
				Server: serverID,
			}); err != nil {
				utils.CliErrorWithExit("Failed to add server ACL: %v.", err)
			}
			utils.CliSuccess("Server ACL added: token %s can access %s", tokenArg, serverName)
			return
		}

		names := utils.SplitAndTrim(serversCSV, ",")
		if len(names) == 0 {
			utils.CliErrorWithExit("--servers must contain at least one server name.")
		}

		serverIDs := resolveServerIDs(alpaconClient, names)

		if err = security.BulkAddServerAcl(alpaconClient, security.ServerAclBulkRequest{
			Token:   tokenID,
			Servers: serverIDs,
		}); err != nil {
			utils.CliErrorWithExit("Failed to bulk-add server ACLs: %v.", err)
		}
		utils.CliSuccess("Server ACLs added: token %s can access %s", tokenArg, fmt.Sprintf("[%s]", serversCSV))
	},
}

func init() {
	aclServerAddCmd.Flags().String("server", "", "Server name (single)")
	aclServerAddCmd.Flags().String("servers", "", "Comma-separated server names (bulk)")
}
