package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclCommandAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a command ACL rule to a token",
	Long: `Define which server-side shell commands an API token is allowed to execute
via websh or exec (e.g., "whoami", "systemctl status *", "docker compose *").

Use * as a wildcard to match any arguments. Without a wildcard, only the exact
command string is matched.

Username semantics: "" = token owner only, "*" = any user, exact name = match only.
Groupname semantics: "" = no group restriction, "*" = any group, exact name = match only.`,
	Example: `  alpacon token acl command add --token=my-api-token --command="whoami"
  alpacon token acl command add --token=my-api-token --command="docker *" --username=root
  alpacon token acl command add --token=my-api-token --command="systemctl status *" --username="*" --groupname="*"`,
	Run: runCommandAclAdd,
}

func init() {
	aclCommandAddCmd.Flags().StringP("token", "t", "", "Token name or ID")
	aclCommandAddCmd.Flags().StringP("command", "c", "", "Server-side shell command (supports * wildcard)")
	aclCommandAddCmd.Flags().String("username", "", `Username restriction: "" = token owner only, "*" = any user`)
	aclCommandAddCmd.Flags().String("groupname", "", `Groupname restriction: "" = no restriction, "*" = any group`)
}

func runCommandAclAdd(cmd *cobra.Command, _ []string) {
	token, _ := cmd.Flags().GetString("token")
	command, _ := cmd.Flags().GetString("command")
	username, _ := cmd.Flags().GetString("username")
	groupname, _ := cmd.Flags().GetString("groupname")

	var req security.CommandAclRequest
	if token == "" || command == "" {
		req = promptForCommandAcl()
	} else {
		req = security.CommandAclRequest{
			Token:     token,
			Command:   command,
			Username:  username,
			Groupname: groupname,
		}
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	req.Token, err = auth.ResolveTokenID(alpaconClient, req.Token)
	if err != nil {
		utils.CliErrorWithExit("Failed to add the command ACL: %v.", err)
	}

	if err = security.AddCommandAcl(alpaconClient, req); err != nil {
		utils.CliErrorWithExit("Failed to add the command ACL: %v.", err)
	}

	utils.CliSuccess("Command ACL added to token %s: %s", req.Token, req.Command)
}

func promptForCommandAcl() security.CommandAclRequest {
	return security.CommandAclRequest{
		Token:   utils.PromptForRequiredInput("Token ID or name: "),
		Command: utils.PromptForRequiredInput("Command: "),
	}
}
