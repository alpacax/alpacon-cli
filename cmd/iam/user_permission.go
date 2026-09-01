package iam

import (
	"errors"

	"github.com/spf13/cobra"
)

var userPermissionCmd = &cobra.Command{
	Use:     "permission",
	Aliases: []string{"permissions"},
	Short:   "Inspect what a user is allowed to do",
	Long: `Inspect the permissions a user's roles add up to.

Role membership and effective access are different questions. 'alpacon user role
ls' answers which roles someone holds; this answers what those roles let them do,
and where each one came from.`,
	Example: `  alpacon user permission ls john
  alpacon user permission can-i john server:update`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon user permission ls' or 'alpacon user permission can-i'. Run 'alpacon user permission --help' for more information")
	},
}

func init() {
	userPermissionCmd.AddCommand(userPermissionListCmd)
	userPermissionCmd.AddCommand(userPermissionCanICmd)
}
