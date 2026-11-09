package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclFileAddCmd = &cobra.Command{
	Use:   "add TOKEN",
	Short: "Add a file ACL rule to a token",
	Long: `Define which file paths an API token is allowed to access via cp.

Path supports wildcard matching (* = any characters).
Action: upload, download, or * for both.

For a service token, omitting --username pins the entry to the application's own
account. In --username, '' or * matches any account bound to the application on
that server; the application username matches only its own account. In
--groupname, '' or * sets no group restriction; the application username matches
its own group. Any other value is refused.

For a personal API token:
Username semantics: "" = token owner only, "*" = any user, exact name = match only.
Groupname semantics: "" = no group restriction, "*" = any group, exact name = match only.`,
	Example: `  alpacon token acl file add my-api-token --path "/home/deploy/*" --action upload
  alpacon token acl file add my-api-token --path "/var/log/*" --action download --username root
  alpacon token acl file add my-api-token --path "*" --action "*" --username "*" --groupname "*"`,
	Args: cobra.ExactArgs(1),
	Run:  runFileAclAdd,
}

func init() {
	aclFileAddCmd.Flags().String("path", "", "File path pattern (supports * wildcard)")
	aclFileAddCmd.Flags().String("action", "", "Allowed action: upload, download, or *")
	aclFileAddCmd.Flags().String("username", "", `Service tokens: omit to pin the entry to the application's own account; '' or * matches any account bound to the application on that server; the application username matches only its own account; any other value is refused. Personal tokens: "" = owner, "*" = any user`)
	aclFileAddCmd.Flags().String("groupname", "", `Group restriction (service tokens: '' or * sets no group restriction; the application username matches its own group; any other value is refused)`)
	_ = aclFileAddCmd.MarkFlagRequired("path")
	_ = aclFileAddCmd.MarkFlagRequired("action")
}

func runFileAclAdd(cmd *cobra.Command, args []string) {
	tokenArg := args[0]
	path, _ := cmd.Flags().GetString("path")
	action, _ := cmd.Flags().GetString("action")
	username, _ := cmd.Flags().GetString("username")
	groupname, _ := cmd.Flags().GetString("groupname")

	if action != security.FileAclActionUpload && action != security.FileAclActionDownload && action != security.FileAclActionAll {
		utils.CliUsageErrorWithExit("--action must be one of: upload, download, *.")
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	tokenID, err := auth.ResolveTokenID(alpaconClient, tokenArg)
	if err != nil {
		utils.CliErrorWithExit("Failed to resolve token: %v.", err)
	}

	if err = security.AddFileAcl(alpaconClient, security.FileAclRequest{
		Token:     tokenID,
		Path:      path,
		Action:    action,
		Username:  username,
		Groupname: groupname,
	}); err != nil {
		utils.CliErrorWithExit("Failed to add file ACL: %v.", err)
	}

	utils.CliSuccess("File ACL added to token %s: %s [%s]", tokenArg, path, action)
}
