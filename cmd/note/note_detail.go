package note

import (
	"github.com/alpacax/alpacon-cli/api/note"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var noteDetailCmd = &cobra.Command{
	Use:     "describe NOTE_ID",
	Aliases: []string{"desc"},
	Short:   "Show details of a note",
	Long: `
	The describe command fetches and displays detailed information about a specific note,
	including its content, server, author, and privacy settings.
	`,
	Example: `
	alpacon note describe 550e8400-e29b-41d4-a716-446655440000
	alpacon note desc 550e8400-e29b-41d4-a716-446655440000
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		noteID := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		noteDetail, err := note.GetNoteDetail(alpaconClient, noteID)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the note details: %s.", err)
		}

		utils.PrintJson(noteDetail)
		return nil
	}),
}
