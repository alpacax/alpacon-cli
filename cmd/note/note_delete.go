package note

import (
	"github.com/alpacax/alpacon-cli/api/note"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var noteDeleteCmd = &cobra.Command{
	Use:     "delete NOTE_ID",
	Aliases: []string{"rm"},
	Short:   "Delete a specified note",
	Long: `
	This command permanently deletes a specified note from the Alpacon server. 
	It's important to verify that you have the necessary permissions to delete a note before using this command. 
	The command requires an exact note ID as its argument.
	`,
	Example: `
	alpacon note delete 550e8400-e29b-41d4-a716-446655440000
	alpacon note rm 550e8400-e29b-41d4-a716-446655440000
	alpacon note delete 550e8400-e29b-41d4-a716-446655440000 -y
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		noteID := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			if proceed, err := utils.Confirm("Delete note '%s'?", noteID); err != nil || !proceed {
				return err
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = note.DeleteNote(alpaconClient, noteID)
		if err != nil {
			return utils.NewExitError("Failed to delete the note: %s.", err)
		}

		utils.CliSuccess("Note deleted: %s", noteID)
		return nil
	}),
}

func init() {
	noteDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
