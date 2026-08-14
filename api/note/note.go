package note

import (
	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	noteURL = "/api/servers/notes/"
)

func GetNoteList(ac *client.AlpaconClient, serverName string, tail int, pinnedOnly bool) ([]NoteDetails, error) {
	// The server sorts -pinned first, which would let old pinned notes take slots
	// from the newest tail entries.
	params := map[string]string{
		"ordering": "-added_at",
	}
	if serverName != "" {
		serverID, err := server.GetServerIDByName(ac, serverName)
		if err != nil {
			return nil, err
		}
		params["server"] = serverID
	}
	if pinnedOnly {
		params["pinned"] = "true"
	}

	notes, err := api.FetchPagesUpTo[NoteResponse](ac, noteURL, params, tail)
	if err != nil {
		return nil, err
	}

	var noteList []NoteDetails
	for _, note := range notes {
		noteList = append(noteList, NoteDetails{
			ID:      note.ID,
			Server:  note.Server.Name,
			Author:  note.Author.Name,
			Content: note.Content,
			Private: note.Private,
			Pinned:  note.Pinned,
			AddedAt: utils.TimeUtils(note.AddedAt),
		})
	}

	return noteList, nil
}

func CreateNote(ac *client.AlpaconClient, noteRequest NoteCreateRequest) error {
	serverID, err := server.GetServerIDByName(ac, noteRequest.Server)
	if err != nil {
		return err
	}

	noteRequest.Server = serverID
	noteRequest.Pinned = false // The default value for the alpacon API server is currently false

	_, err = ac.SendPostRequest(noteURL, noteRequest)
	if err != nil {
		return err
	}

	return nil
}

func GetNoteDetail(ac *client.AlpaconClient, noteID string) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(noteURL, noteID, nil))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func UpdateNote(ac *client.AlpaconClient, noteID string) ([]byte, error) {
	responseBody, err := GetNoteDetail(ac, noteID)
	if err != nil {
		return nil, err
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(utils.BuildURL(noteURL, noteID, nil), data)
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func DeleteNote(ac *client.AlpaconClient, noteID string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(noteURL, noteID, nil))
	if err != nil {
		return err
	}

	return nil
}
