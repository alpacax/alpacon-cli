package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclCommandListCmd = newAclCommandListCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newAclCommandListCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:     "ls TOKEN",
		Aliases: []string{"list"},
		Short:   "List command ACL rules for a token",
		Example: `  alpacon token acl command ls my-api-token
  alpacon token acl command list my-api-token`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runCommandAclList(ios, cmd, args, newClient)
		}),
	}
}

func runCommandAclList(ios *utils.IOStreams, _ *cobra.Command, args []string, newClient clientFactory) error {
	tokenID := args[0]

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	tokenID, err = auth.ResolveTokenID(alpaconClient, tokenID)
	if err != nil {
		return utils.NewExitError("Failed to resolve token: %v.", err)
	}

	acls, err := security.GetCommandAclList(alpaconClient, tokenID)
	if err != nil {
		return utils.NewExitError("Failed to retrieve command ACLs: %v.", err)
	}

	return ios.PrintTable(acls)
}
