package note

import (
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	noteURL = "/api/servers/notes/"
)

func GetNoteList(ac *client.AlpaconClient, serverName string, pageSize int) ([]NoteDetails, error) {
	var noteList []NoteDetails
	var serverID string
	var err error

	if serverName != "" {
		serverID, err = server.GetServerIDByName(ac, serverName)
		if err != nil {
			return nil, err
		}
	}

	params := map[string]string{
		"serverID": serverID,
	}
	if pageSize > 0 {
		params["page_size"] = fmt.Sprintf("%d", pageSize)
	}
	responseBody, err := ac.SendGetRequest(utils.BuildURL(noteURL, "", params))
	if err != nil {
		return nil, err
	}

	var response api.ListResponse[NoteDetails]
	if err = json.Unmarshal(responseBody, &response); err != nil {
		return nil, err
	}

	for _, note := range response.Results {
		sName, err := server.GetServerNameByID(ac, note.Server)
		if err != nil {
			return nil, err
		}

		userName, err := iam.GetUserNameByID(ac, note.Author)
		if err != nil {
			return nil, err
		}

		noteList = append(noteList, NoteDetails{
			ID:      note.ID,
			Server:  sName,
			Author:  userName,
			Content: note.Content,
			Private: note.Private,
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

func DeleteNote(ac *client.AlpaconClient, noteID string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(noteURL, noteID, nil))
	if err != nil {
		return err
	}

	return err
}
