package server

import (
	"github.com/spf13/cobra"
)

var tokenListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List server registration tokens",
	Run: func(cmd *cobra.Command, args []string) {
		// TODO: implement in Phase 5
	},
}
