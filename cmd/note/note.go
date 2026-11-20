package note

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewNoteCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "note",
		Aliases: []string{"notes"},
		Short:   "Manage notes attached to servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newNoteListCmd(newClient, ios),
		newNoteCreateCmd(newClient, ios),
		newNoteDeleteCmd(newClient, ios),
		newNoteDetailCmd(newClient, ios),
		newNoteUpdateCmd(newClient, ios),
	)
	return cmd
}
