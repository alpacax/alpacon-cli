package token

import "github.com/spf13/cobra"

var aclAddCmd = &cobra.Command{
	Use:        "add",
	Short:      "Add a command ACL rule to a token (deprecated: use 'acl command add')",
	Deprecated: "use 'alpacon token acl command add' instead",
	Hidden:     true,
	Run:        runCommandAclAdd,
}

func init() {
	aclAddCmd.Flags().StringP("token", "t", "", "Token name or ID")
	aclAddCmd.Flags().StringP("command", "c", "", "Server-side shell command (supports * wildcard)")
	aclAddCmd.Flags().String("username", "", "Username restriction")
	aclAddCmd.Flags().String("groupname", "", "Groupname restriction")
}
