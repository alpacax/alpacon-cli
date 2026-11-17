package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newAclFileAddCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add TOKEN",
		Short: "Add a file ACL rule to a token",
		Long: `Define which file paths an API token is allowed to access via cp.

Path supports wildcard matching (* = any characters).
Action: upload, download, or * for both.

Username semantics: "" = token owner only, "*" = any user, exact name = match only.
Groupname semantics: "" = no group restriction, "*" = any group, exact name = match only.`,
		Example: `  alpacon token acl file add my-api-token --path "/home/deploy/*" --action upload
  alpacon token acl file add my-api-token --path "/var/log/*" --action download --username root
  alpacon token acl file add my-api-token --path "*" --action "*" --username "*" --groupname "*"`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runFileAclAdd(ios, cmd, args, newClient)
		}),
	}
	cmd.Flags().String("path", "", "File path pattern (supports * wildcard)")
	cmd.Flags().String("action", "", "Allowed action: upload, download, or *")
	cmd.Flags().String("username", "", `Username restriction: "" = token owner only, "*" = any user`)
	cmd.Flags().String("groupname", "", `Groupname restriction: "" = no restriction, "*" = any group`)
	_ = cmd.MarkFlagRequired("path")
	_ = cmd.MarkFlagRequired("action")
	return cmd
}

func runFileAclAdd(ios *utils.IOStreams, cmd *cobra.Command, args []string, newClient clientFactory) error {
	tokenArg := args[0]
	path, _ := cmd.Flags().GetString("path")
	action, _ := cmd.Flags().GetString("action")
	username, _ := cmd.Flags().GetString("username")
	groupname, _ := cmd.Flags().GetString("groupname")

	if action != security.FileAclActionUpload && action != security.FileAclActionDownload && action != security.FileAclActionAll {
		return utils.NewUsageExitError("--action must be one of: upload, download, *.")
	}

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	tokenID, err := auth.ResolveTokenID(alpaconClient, tokenArg)
	if err != nil {
		return utils.NewExitError("Failed to resolve token: %v.", err)
	}

	if err = security.AddFileAcl(alpaconClient, security.FileAclRequest{
		Token:     tokenID,
		Path:      path,
		Action:    action,
		Username:  username,
		Groupname: groupname,
	}); err != nil {
		return utils.NewExitError("Failed to add file ACL: %v.", err)
	}

	ios.Success("File ACL added to token %s: %s [%s]", tokenArg, path, action)
	return nil
}
