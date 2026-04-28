package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclAddCmd = &cobra.Command{
	Use:        "add",
	Short:      "Add a command ACL rule to a token (deprecated: use 'acl command add')",
	Deprecated: "use 'alpacon token acl command add' instead",
	Hidden:     true,
	Args:       cobra.RangeArgs(0, 1),
	Run:        runLegacyAclAdd,
}

func init() {
	aclAddCmd.Flags().StringP("token", "t", "", "Token name or ID")
	aclAddCmd.Flags().StringP("command", "c", "", "Server-side shell command (supports * wildcard)")
	aclAddCmd.Flags().String("username", "", "Username restriction")
	aclAddCmd.Flags().String("groupname", "", "Groupname restriction")
}

func runLegacyAclAdd(cmd *cobra.Command, args []string) {
	tokenFlag, _ := cmd.Flags().GetString("token")
	var tokenArg string
	switch {
	case tokenFlag != "":
		tokenArg = tokenFlag
	case len(args) > 0:
		tokenArg = args[0]
	default:
		utils.CliErrorWithExit("token name or ID is required (use --token or positional argument)")
	}

	command, _ := cmd.Flags().GetString("command")
	if command == "" {
		utils.CliErrorWithExit("--command is required")
	}
	username, _ := cmd.Flags().GetString("username")
	groupname, _ := cmd.Flags().GetString("groupname")

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	tokenID, err := auth.ResolveTokenID(alpaconClient, tokenArg)
	if err != nil {
		utils.CliErrorWithExit("Failed to resolve token: %v.", err)
	}

	if err = security.AddCommandAcl(alpaconClient, security.CommandAclRequest{
		Token:     tokenID,
		Command:   command,
		Username:  username,
		Groupname: groupname,
	}); err != nil {
		utils.CliErrorWithExit("Failed to add the command ACL: %v.", err)
	}

	utils.CliSuccess("Command ACL added to token %s: %s", tokenArg, command)
}
