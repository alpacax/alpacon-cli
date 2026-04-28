package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	serverapi "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclServerDeleteCmd = &cobra.Command{
	Use:     "delete ACL-ID-OR-TOKEN",
	Aliases: []string{"rm"},
	Short:   "Delete server ACL rule(s)",
	Long: `Delete a server ACL by its ID (single delete), or revoke a token's access
to multiple servers at once using --servers (bulk delete).`,
	Example: `  # Single delete by ACL ID
  alpacon token acl server delete 550e8400-e29b-41d4-a716-446655440000

  # Bulk delete: revoke token access to named servers
  alpacon token acl server delete my-api-token --servers web-01,web-02`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		arg := args[0]
		serversCSV, _ := cmd.Flags().GetString("servers")
		yes, _ := cmd.Flags().GetBool("yes")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if serversCSV != "" {
			if !yes {
				utils.ConfirmAction("Revoke server ACLs for token '%s' on servers [%s]?", arg, serversCSV)
			}

			tokenID := arg
			if !utils.IsUUID(tokenID) {
				tokenID, err = auth.GetAPITokenIDByName(alpaconClient, arg)
				if err != nil {
					utils.CliErrorWithExit("Failed to resolve token: %v.", err)
				}
			}

			names := utils.SplitAndTrim(serversCSV, ",")
			serverIDs := make([]string, 0, len(names))
			for _, name := range names {
				id, err := serverapi.GetServerIDByName(alpaconClient, name)
				if err != nil {
					utils.CliErrorWithExit("Failed to resolve server '%s': %v.", name, err)
				}
				serverIDs = append(serverIDs, id)
			}

			if err = security.BulkDeleteServerAcl(alpaconClient, security.ServerAclBulkDeleteRequest{
				Token:   tokenID,
				Servers: serverIDs,
			}); err != nil {
				utils.CliErrorWithExit("Failed to bulk-delete server ACLs: %v.", err)
			}
			utils.CliSuccess("Server ACLs revoked: token %s no longer has access to [%s]", arg, serversCSV)
			return
		}

		if !yes {
			utils.ConfirmAction("Delete server ACL '%s'?", arg)
		}
		if err = security.DeleteServerAcl(alpaconClient, arg); err != nil {
			utils.CliErrorWithExit("Failed to delete server ACL: %s.", err)
		}
		utils.CliSuccess("Server ACL deleted: %s", arg)
	},
}

func init() {
	aclServerDeleteCmd.Flags().String("servers", "", "Comma-separated server names (bulk delete)")
	aclServerDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
