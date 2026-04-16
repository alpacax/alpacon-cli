package server

import (
	"github.com/spf13/cobra"
)

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new server registration token",
	Run: func(cmd *cobra.Command, args []string) {
		// TODO: implement in Phase 4
	},
}
