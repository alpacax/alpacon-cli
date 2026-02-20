package note

import (
	"github.com/alpacax/alpacon-cli/api/note"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	noteRequest note.NoteCreateRequest
)

var noteCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a note on the specified server",
	Long: `
	This command allows you to create a new note on a server. 
	You can specify the server name, note content, and privacy settings.",
	`,
	Example: `
	alpacon note create 
	alpacon note create -s [SERVER NAME]
	alpacon note create -s myserver -c "hello world!" -p true
	`,
	Run: func(cmd *cobra.Command, args []string) {
		if noteRequest.Server == "" {
			noteRequest.Server = utils.PromptForRequiredInput("Server Name: ")
		}
		if noteRequest.Content == "" {
			noteRequest.Content = utils.PromptForRequiredInput("Content(max 512 characters): ")
		}

		if len(noteRequest.Content) > 512 {
			utils.CliErrorWithExit("Note content exceeds the 512 character limit (current length: %d). Please shorten your note and try again", len(noteRequest.Content))
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = note.CreateNote(alpaconClient, noteRequest)
		if err != nil {
			utils.CliErrorWithExit("Failed to create the new note: %s.", err)
		}

		utils.CliSuccess("Note created on server %s", noteRequest.Server)
	},
}

func init() {
	noteCreateCmd.Flags().StringVarP(&noteRequest.Server, "server", "s", "", "Specify the server name where the note will be created.")
	noteCreateCmd.Flags().StringVarP(&noteRequest.Content, "content", "c", "", "Enter the note content (up to 512 characters).")
	noteCreateCmd.Flags().BoolVarP(&noteRequest.Private, "private", "p", false, "Set this flag to mark the note as private.")
}
