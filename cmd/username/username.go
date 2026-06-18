package username

import (
	"errors"

	"github.com/spf13/cobra"
)

// opGet is the operation identifier carried in JSON error envelopes (context.operation).
const opGet = "get"

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
