package server

import (
	"github.com/spf13/cobra"
)

var tokenDeleteCmd = &cobra.Command{
	Use:     "delete TOKEN",
	Aliases: []string{"rm"},
	Short:   "Delete a server registration token",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// TODO: implement in Phase 6
	},
}
