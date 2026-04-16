package server

import (
	"errors"

	"github.com/spf13/cobra"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage server registration tokens",
	Long:  "Create, list, and delete registration tokens used by servers to self-register via alpamon.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cmd.Help(); err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon server token create', 'alpacon server token ls', or 'alpacon server token delete'. Run 'alpacon server token --help' for more information")
	},
}

func init() {
	tokenCmd.AddCommand(tokenCreateCmd)
	tokenCmd.AddCommand(tokenListCmd)
	tokenCmd.AddCommand(tokenDeleteCmd)
}
