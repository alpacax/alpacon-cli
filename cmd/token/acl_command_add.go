package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclCommandAddCmd = &cobra.Command{
	Use:   "add TOKEN",
	Short: "Add a command ACL rule to a token",
	Long: `Define which server-side shell commands an API token is allowed to execute
via websh or exec (e.g., "whoami", "systemctl status *", "docker compose *").

Use * as a wildcard to match any arguments. Without a wildcard, only the exact
command string is matched.

For a service token, the account is its application account; * or an empty value in
--username or --groupname means the same account. Any other account is refused.
Omit --username to let the server fill it in.

For a personal API token:
Username semantics: "" = token owner only, "*" = any user, exact name = match only.
Groupname semantics: "" = no group restriction, "*" = any group, exact name = match only.

On servers that manage command ACLs on the command pre-approval ledger, this
write is refused with command_acl_moved; a Command ACL admin issues the entry
under Pre-approved commands on the service token's page in the web console, and a
personal API token has no command list on the ledger.`,
	Example: `  alpacon token acl command add my-api-token --command="whoami"
  alpacon token acl command add my-api-token --command="docker *" --username=root
  alpacon token acl command add my-api-token --command="systemctl status *" --username="*" --groupname="*"`,
	Args: cobra.ExactArgs(1),
	Run:  runCommandAclAdd,
}

func init() {
	aclCommandAddCmd.Flags().StringP("command", "c", "", "Server-side shell command (supports * wildcard)")
	aclCommandAddCmd.Flags().String("username", "", `Service tokens: application account, * or an empty value for the same account (default: the server fills it in); personal tokens: "" = owner, "*" = any user`)
	aclCommandAddCmd.Flags().String("groupname", "", `Group restriction (service tokens: application account's own group, * or an empty value for the same group; any other group is refused)`)
	_ = aclCommandAddCmd.MarkFlagRequired("command")
}

func runCommandAclAdd(cmd *cobra.Command, args []string) {
	tokenArg := args[0]
	command, _ := cmd.Flags().GetString("command")
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

	req := security.CommandAclRequest{
		Token:     tokenID,
		Command:   command,
		Username:  username,
		Groupname: groupname,
	}
	if err = security.AddCommandAcl(alpaconClient, req); err != nil {
		utils.CliErrorWithExit("Failed to add the command ACL: %v.", err)
	}

	utils.CliSuccess("Command ACL added to token %s: %s", tokenArg, command)
}
