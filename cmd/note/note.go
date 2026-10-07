package note

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var NoteCmd = &cobra.Command{
	Use:     "note",
	Aliases: []string{"notes"},
	Short:   "Manage notes attached to servers",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	NoteCmd.AddCommand(noteListCmd)
	NoteCmd.AddCommand(noteCreateCmd)
	NoteCmd.AddCommand(noteDeleteCmd)
	NoteCmd.AddCommand(noteDetailCmd)
	NoteCmd.AddCommand(noteUpdateCmd)
}
