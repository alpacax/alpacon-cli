package token

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newAclDeleteCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:        "delete ACL_ID",
		Aliases:    []string{"rm"},
		Short:      "Delete a command ACL rule",
		Deprecated: "use 'alpacon token acl command delete' instead",
		Hidden:     true,
		Args:       cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runCommandAclDelete(ios, cmd, args, newClient)
		}),
	}
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	return cmd
}
