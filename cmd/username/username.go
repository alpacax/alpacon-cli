package username

import (
	"errors"

	"github.com/spf13/cobra"
)

var UsernameCmd = &cobra.Command{
	Use:   "username",
	Short: "Manage the username for your account's server access",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cmd.Help(); err != nil {
			return err
		}
		return errors.New("a subcommand is required. Run 'alpacon username --help' for more information")
	},
}

func init() {
	UsernameCmd.AddCommand(usernameGetCmd)
	UsernameCmd.AddCommand(usernameSetCmd)
}
