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
	You can specify the server name, note content, and privacy settings.
	`,
	Example: `
	alpacon note create
	alpacon note create -s my-server -c "hello world!" -p true
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		var err error
		if noteRequest.Server == "" {
			if noteRequest.Server, err = utils.ReadRequiredInput("Server Name: "); err != nil {
				return err
			}
		}
		if noteRequest.Content == "" {
			if noteRequest.Content, err = utils.ReadRequiredInput("Content(max 512 characters): "); err != nil {
				return err
			}
		}

		if len(noteRequest.Content) > 512 {
			const tooLong = "Note content exceeds the 512 character limit (current length: %d). Please shorten your note and try again"
			if cmd.Flags().Changed("content") {
				return utils.NewUsageExitError(tooLong, len(noteRequest.Content))
			}
			return utils.NewExitError(tooLong, len(noteRequest.Content))
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = note.CreateNote(alpaconClient, noteRequest)
		if err != nil {
			return utils.NewExitError("Failed to create the new note: %s.", err)
		}

		utils.CliSuccess("Note created on server %s", noteRequest.Server)
		return nil
	}),
}

func init() {
	noteCreateCmd.Flags().StringVarP(&noteRequest.Server, "server", "s", "", "Specify the server name where the note will be created.")
	noteCreateCmd.Flags().StringVarP(&noteRequest.Content, "content", "c", "", "Enter the note content (up to 512 characters).")
	noteCreateCmd.Flags().BoolVarP(&noteRequest.Private, "private", "p", false, "Set this flag to mark the note as private.")
}
