package note

import (
	"github.com/alpacax/alpacon-cli/api/note"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var noteListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "Display a list of the newest notes",
	Long: `Display notes stored on Alpacon, newest first.

Use --tail to limit the output to the newest N notes, --server to scope to one server,
and --pinned to show only pinned notes.`,
	Example: `  alpacon note ls
  alpacon note ls --tail 100
  alpacon note ls --server my-server
  alpacon note ls --pinned`,
	Run: func(cmd *cobra.Command, _ []string) {
		tail, _ := cmd.Flags().GetInt("tail")
		serverName, _ := cmd.Flags().GetString("server")
		pinnedOnly, _ := cmd.Flags().GetBool("pinned")

		runNoteList(tail, serverName, pinnedOnly)
	},
}

func init() {
	noteListCmd.Flags().IntP("tail", "t", 25, "Number of notes to show, newest first")
	noteListCmd.Flags().StringP("server", "s", "", "Specify server for notes")
	noteListCmd.Flags().Bool("pinned", false, "Show only pinned notes")
}

func runNoteList(tail int, serverName string, pinnedOnly bool) {
	utils.RequirePositiveInt("tail", tail)

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	noteList, err := note.GetNoteList(alpaconClient, serverName, tail, pinnedOnly)
	if err != nil {
		utils.CliErrorWithExit("Failed to retrieve the notes: %s.", err)
	}

	utils.PrintTable(noteList)
}
