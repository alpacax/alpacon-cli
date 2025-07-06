package note

import (
	"github.com/alpacax/alpacon-cli/api/note"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var noteListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "all"},
	Short:   "Display a list of all notes",
	Long: `
	This command displays a comprehensive list of all notes stored on the Alpacon. 
	It provides details such as note IDs, authors, and contents. 
	This command is useful for quickly reviewing all notes and their key information. 
	`,
	Example: `
	alpacon note ls
	alpacon note list
	alpacon note all
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		noteList, err := note.GetNoteList(alpaconClient, serverName, pageSize)
		if err != nil {
			utils.CliError("Failed to retrieve the notes: %s.", err)
		}

		utils.PrintTable(noteList)
	},
}

func init() {
	noteListCmd.Flags().IntVarP(&pageSize, "tail", "t", 25, "Number of log entries to show from the end")
	noteListCmd.Flags().StringVarP(&serverName, "server", "s", "", "Specify server for notes")
}
