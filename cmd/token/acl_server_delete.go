package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclServerDeleteCmd = &cobra.Command{
	Use:     "delete ACL_ID_OR_TOKEN",
	Aliases: []string{"rm"},
	Short:   "Delete server ACL rule(s)",
	Long: `Delete a server ACL by its ID (single delete), or revoke a token's access
to multiple servers at once using --servers (bulk delete).`,
	Example: `  # Single delete by ACL ID
  alpacon token acl server delete 550e8400-e29b-41d4-a716-446655440000

  # Bulk delete: revoke token access to named servers
  alpacon token acl server delete my-api-token --servers web-01,web-02`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(runServerAclDelete),
}

func init() {
	aclServerDeleteCmd.Flags().String("servers", "", "Comma-separated server names (bulk delete)")
	aclServerDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}

func runServerAclDelete(cmd *cobra.Command, args []string) error {
	arg := args[0]
	serversCSV, _ := cmd.Flags().GetString("servers")
	yes, _ := cmd.Flags().GetBool("yes")

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	if serversCSV != "" {
		if !yes {
			if proceed, err := utils.Confirm("Revoke server ACLs for token '%s' on servers [%s]?", arg, serversCSV); err != nil || !proceed {
				return err
			}
		}

		tokenID, err := auth.ResolveTokenID(alpaconClient, arg)
		if err != nil {
			return utils.NewExitError("Failed to resolve token: %v.", err)
		}

		names := utils.SplitAndTrim(serversCSV, ",")
		if len(names) == 0 {
			return utils.NewUsageExitError("--servers must contain at least one server name.")
		}
		serverIDs, err := resolveServerIDs(alpaconClient, names)
		if err != nil {
			return utils.NewExitError("%v.", err)
		}

		if err = security.BulkDeleteServerAcl(alpaconClient, security.ServerAclBulkRequest{
			Token:   tokenID,
			Servers: serverIDs,
		}); err != nil {
			return utils.NewExitError("Failed to bulk-delete server ACLs: %v.", err)
		}
		utils.CliSuccess("Server ACLs revoked: token %s no longer has access to [%s]", arg, serversCSV)
		return nil
	}

	if !yes {
		if proceed, err := utils.Confirm("Delete server ACL '%s'?", arg); err != nil || !proceed {
			return err
		}
	}
	if err = security.DeleteServerAcl(alpaconClient, arg); err != nil {
		return utils.NewExitError("Failed to delete server ACL: %v.", err)
	}
	utils.CliSuccess("Server ACL deleted: %s", arg)
	return nil
}
