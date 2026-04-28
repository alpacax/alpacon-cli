package token

import "github.com/spf13/cobra"

var aclListCmd = &cobra.Command{
	Use:        "ls TOKEN",
	Aliases:    []string{"list"},
	Short:      "List command ACL rules for a token (deprecated: use 'acl command ls')",
	Deprecated: "use 'alpacon token acl command ls' instead",
	Hidden:     true,
	Args:       cobra.ExactArgs(1),
	Run:        runCommandAclList,
}
