package token

import "github.com/spf13/cobra"

var aclDeleteCmd = &cobra.Command{
	Use:        "delete ACL-ID",
	Aliases:    []string{"rm"},
	Short:      "Delete a command ACL rule (deprecated: use 'acl command delete')",
	Deprecated: "use 'alpacon token acl command delete' instead",
	Hidden:     true,
	Args:       cobra.ExactArgs(1),
	Run:        runCommandAclDelete,
}

func init() {
	aclDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
