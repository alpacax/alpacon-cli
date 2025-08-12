package server

import (
	"errors"
	"github.com/spf13/cobra"
)

var ServerCmd = &cobra.Command{
	Use:     "server",
	Aliases: []string{"servers"},
	Short:   "Commands to manage and interact with servers",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon server list', 'alpacon server create', 'alpacon server detail', or 'alpacon server delete'. Run 'alpacon server --help' for more information")
	},
}

func init() {
	ServerCmd.AddCommand(serverListCmd)
	ServerCmd.AddCommand(serverDetailCmd)
	ServerCmd.AddCommand(serverCreateCmd)
	ServerCmd.AddCommand(serverDeleteCmd)
}
