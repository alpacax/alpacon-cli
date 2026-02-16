package note

import (
	"github.com/alpacax/alpacon-cli/api/note"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var noteDeleteCmd = &cobra.Command{
	Use:     "delete [NOTE ID]",
	Aliases: []string{"rm"},
	Short:   "Delete a specified note",
	Long: `
	This command permanently deletes a specified note from the Alpacon server. 
	It's important to verify that you have the necessary permissions to delete a note before using this command. 
	The command requires an exact note ID as its argument.
	`,
	Example: ` 
	alpacon server delete [NOTE ID]	
	alpacon server rm [NOTE ID]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		noteID := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete note '%s'?", noteID)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = note.DeleteNote(alpaconClient, noteID)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the note: %s.", err)
		}

		utils.CliSuccess("Note deleted: %s", noteID)
	},
}

func init() {
	noteDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
