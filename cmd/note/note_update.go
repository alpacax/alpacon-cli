package note

import (
	"github.com/alpacax/alpacon-cli/api/note"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var noteUpdateCmd = &cobra.Command{
	Use:   "update NOTE_ID",
	Short: "Update a note",
	Long: `
	Update an existing note in the Alpacon.
	This command opens your editor with the current note data, allowing you to modify fields such as
	content and privacy settings. After saving, the updated note information is displayed for verification.
	`,
	Example: `
	alpacon note update 550e8400-e29b-41d4-a716-446655440000
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		noteID := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		noteDetail, err := note.UpdateNote(alpaconClient, noteID)
		if err != nil {
			utils.CliErrorWithExit("Failed to update the note: %s.", err)
		}

		utils.CliSuccess("Note updated: %s", noteID)
		utils.PrintJson(noteDetail)
	},
}
