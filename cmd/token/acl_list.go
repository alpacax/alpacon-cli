package token

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newAclListCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:        "ls TOKEN",
		Aliases:    []string{"list"},
		Short:      "List command ACL rules for a token",
		Deprecated: "use 'alpacon token acl command ls' instead",
		Hidden:     true,
		Args:       cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runCommandAclList(ios, cmd, args, newClient)
		}),
	}
}
