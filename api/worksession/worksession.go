package worksession

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const workSessionURL = "/api/work-sessions/sessions/"

func GetWorkSessionList(ac *client.AlpaconClient, status, requesterType string) ([]WorkSessionAttributes, error) {
	params := map[string]string{}
	if status != "" {
		params["status"] = status
	}
	if requesterType != "" {
		params["requester_type"] = requesterType
	}

	sessions, err := api.FetchAllPages[WorkSession](ac, workSessionURL, params)
	if err != nil {
		return nil, err
	}

	var result []WorkSessionAttributes
	for i := range sessions {
		result = append(result, ProjectAttributes(&sessions[i]))
	}
	return result, nil
}

// ProjectAttributes converts a full WorkSession into the WorkSessionAttributes
// shape used by table outputs (ls, current). Single source of truth for column projection.
func ProjectAttributes(ws *WorkSession) WorkSessionAttributes {
	serverNames := make([]string, len(ws.Servers))
	for i, srv := range ws.Servers {
		serverNames[i] = srv.Name
	}
	return WorkSessionAttributes{
		ID:          ws.ID,
		Description: utils.TruncateString(ws.Description, 70),
		Status:      ws.Status,
		Scopes:      strings.Join(ws.Scopes, ", "),
		Servers:     strings.Join(serverNames, ", "),
		ExpiresAt:   ws.ExpiresAt.Local().Format("2006-01-02 15:04"),
	}
}

func CreateWorkSession(ac *client.AlpaconClient, req WorkSessionCreateRequest) (*WorkSession, error) {
	body, err := ac.SendPostRequest(workSessionURL, req)
	if err != nil {
		return nil, err
	}
	var session WorkSession
	if err = json.Unmarshal(body, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func GetWorkSession(ac *client.AlpaconClient, id string) (*WorkSession, error) {
	body, err := ac.SendGetRequest(utils.BuildURL(workSessionURL, id, nil))
	if err != nil {
		return nil, err
	}
	var session WorkSession
	if err = json.Unmarshal(body, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func ActivateWorkSession(ac *client.AlpaconClient, id string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "activate"), nil), struct{}{})
	return err
}

func CompleteWorkSession(ac *client.AlpaconClient, id string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "complete"), nil), struct{}{})
	return err
}

func ExtendWorkSession(ac *client.AlpaconClient, id string, req WorkSessionExtendRequest) error {
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "extend"), nil), req)
	return err
}

func RejectWorkSession(ac *client.AlpaconClient, id string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "reject"), nil), struct{}{})
	return err
}

func RevokeWorkSession(ac *client.AlpaconClient, id string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "revoke"), nil), struct{}{})
	return err
}

func ApproveWorkSession(ac *client.AlpaconClient, id string, req WorkSessionApproveRequest) error {
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "approve"), nil), req)
	return err
}

func GetWorkSessionRaw(ac *client.AlpaconClient, id string) ([]byte, error) {
	return ac.SendGetRequest(utils.BuildURL(workSessionURL, id, nil))
}
