package token

import (
	"errors"
	"github.com/spf13/cobra"
)

var AclCmd = &cobra.Command{
	Use:   "acl",
	Short: "Manages command access for API tokens.",
	Long: `
	The acl command allows you to configure access control for API tokens, specifying which commands can be executed by each token. 
	It supports creating, listing, and modifying ACL rules to fine-tune command execution permissions based on your security requirements.
	`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon token acl list', 'alpacon token acl add', or 'alpacon token acl delete' to manage access control rules. Run 'alpacon token acl --help' for more information")
	},
}

func init() {
	AclCmd.AddCommand(aclListCmd)
	AclCmd.AddCommand(aclAddCmd)
	AclCmd.AddCommand(aclDeleteCmd)
}
