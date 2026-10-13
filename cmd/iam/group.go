package iam

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var GroupCmd = &cobra.Command{
	Use:   "group",
	Short: "Manage groups, members, and permissions",
	Long:  "Manage groups, role-based access controls, and group membership within the Alpacon workspace.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	GroupCmd.AddCommand(groupListCmd)
	GroupCmd.AddCommand(groupDetailCmd)
	GroupCmd.AddCommand(groupDeleteCmd)
	GroupCmd.AddCommand(groupCreateCmd)

	GroupCmd.AddCommand(groupUpdateCmd)

	GroupCmd.AddCommand(MemberCmd)
}
