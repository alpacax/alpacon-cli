package iam

import (
	"errors"
	"github.com/spf13/cobra"
)

var GroupCmd = &cobra.Command{
	Use:   "group",
	Short: "Manage Group resources",
	Long: `
	The 'group' command is used to manage IAM group resources,
	including groups, and permissions within the Alpacon.
	`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon group list', 'alpacon group create', 'alpacon group detail', 'alpacon group delete', or 'alpacon group member' to manage groups. Run 'alpacon group --help' for more information")
	},
}

func init() {
	GroupCmd.AddCommand(groupListCmd)
	GroupCmd.AddCommand(groupDetailCmd)
	GroupCmd.AddCommand(groupDeleteCmd)
	GroupCmd.AddCommand(groupCreateCmd)

	GroupCmd.AddCommand(MemberCmd)
}
