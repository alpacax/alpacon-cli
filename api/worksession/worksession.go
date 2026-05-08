package worksession

import (
	"encoding/json"
	"fmt"
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
	for _, s := range sessions {
		serverNames := make([]string, len(s.Servers))
		for i, srv := range s.Servers {
			serverNames[i] = srv.Name
		}
		result = append(result, WorkSessionAttributes{
			ID:          s.ID,
			Description: s.Description,
			Status:      s.Status,
			Scopes:      strings.Join(s.Scopes, ", "),
			Servers:     strings.Join(serverNames, ", "),
			ExpiresAt:   utils.TimeUtils(s.ExpiresAt),
		})
	}
	return result, nil
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
	url := fmt.Sprintf("%s%s/activate/", workSessionURL, id)
	_, err := ac.SendPostRequest(url, struct{}{})
	return err
}

func CompleteWorkSession(ac *client.AlpaconClient, id string) error {
	url := fmt.Sprintf("%s%s/complete/", workSessionURL, id)
	_, err := ac.SendPostRequest(url, struct{}{})
	return err
}

func ExtendWorkSession(ac *client.AlpaconClient, id string, req WorkSessionExtendRequest) error {
	url := fmt.Sprintf("%s%s/extend/", workSessionURL, id)
	_, err := ac.SendPostRequest(url, req)
	return err
}
