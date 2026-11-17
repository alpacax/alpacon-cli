package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newAclAddCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:        "add [TOKEN]",
		Short:      "Add a command ACL rule to a token",
		Deprecated: "use 'alpacon token acl command add' instead",
		Hidden:     true,
		Args:       cobra.RangeArgs(0, 1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runLegacyAclAdd(ios, cmd, args, newClient)
		}),
	}
	cmd.Flags().StringP("token", "t", "", "Token name or ID")
	cmd.Flags().StringP("command", "c", "", "Server-side shell command (supports * wildcard)")
	cmd.Flags().String("username", "", "Username restriction")
	cmd.Flags().String("groupname", "", "Groupname restriction")
	return cmd
}

func runLegacyAclAdd(ios *utils.IOStreams, cmd *cobra.Command, args []string, newClient clientFactory) error {
	tokenFlag, _ := cmd.Flags().GetString("token")
	var tokenArg string
	switch {
	case tokenFlag != "":
		tokenArg = tokenFlag
	case len(args) > 0:
		tokenArg = args[0]
	default:
		return utils.NewUsageExitError("token name or ID is required (use --token or positional argument)")
	}

	command, _ := cmd.Flags().GetString("command")
	if command == "" {
		return utils.NewUsageExitError("--command is required")
	}
	username, _ := cmd.Flags().GetString("username")
	groupname, _ := cmd.Flags().GetString("groupname")

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	tokenID, err := auth.ResolveTokenID(alpaconClient, tokenArg)
	if err != nil {
		return utils.NewExitError("Failed to resolve token: %v.", err)
	}

	if err = security.AddCommandAcl(alpaconClient, security.CommandAclRequest{
		Token:     tokenID,
		Command:   command,
		Username:  username,
		Groupname: groupname,
	}); err != nil {
		return utils.NewExitError("Failed to add the command ACL: %v.", err)
	}

	ios.Success("Command ACL added to token %s: %s", tokenArg, command)
	return nil
}
