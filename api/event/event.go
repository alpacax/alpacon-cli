package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	getEventURL = "/api/events/commands/"
)

func GetEventList(ac *client.AlpaconClient, pageSize int, serverName string, userName string) ([]EventAttributes, error) {
	var serverID, userID string
	var err error
	if serverName != "" {
		serverID, err = server.GetServerIDByName(ac, serverName)
		if err != nil {
			return nil, err
		}
	}
	if userName != "" {
		userID, err = iam.GetUserIDByName(ac, userName)
		if err != nil {
			return nil, err
		}
	}

	relativePath := path.Join(serverID, userID)
	params := map[string]string{
		"page_size": fmt.Sprintf("%d", pageSize),
	}
	responseBody, err := ac.SendGetRequest(utils.BuildURL(getEventURL, relativePath, params))
	if err != nil {
		return nil, err
	}

	var response api.ListResponse[EventDetails]
	if err = json.Unmarshal(responseBody, &response); err != nil {
		return nil, err
	}

	var eventList []EventAttributes
	for _, event := range response.Results {
		eventList = append(eventList, EventAttributes{
			Server:      event.ServerName,
			Shell:       event.Shell,
			Command:     event.Line,
			Result:      utils.TruncateString(event.Result, 70),
			Status:      utils.BoolPointerToString(event.Success),
			Operator:    event.RequestedByName,
			RequestedAt: utils.TimeUtils(event.AddedAt),
		})
	}
	return eventList, nil
}

func RunCommand(ac *client.AlpaconClient, serverName, command string, username, groupname string, env map[string]string) (string, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return "", err
	}

	commandRequest := &CommandRequest{
		Shell:     "system",
		Line:      command,
		Env:       env,
		Username:  username,
		Groupname: groupname,
		Server:    serverID,
		RunAfter:  []string{},
	}
	respBody, err := ac.SendPostRequest(getEventURL, commandRequest)
	if err != nil {
		return "", err
	}

	// TODO: CLI currently supports only single-command response.
	//       If the response contains a list, we parse only the first command result for now.
	//       Support for handling multiple responses should be added later.
	var cmdResponse []CommandResponse

	err = json.Unmarshal(respBody, &cmdResponse)
	if err != nil {
		return "", err
	}

	result, err := PollCommandExecution(ac, cmdResponse[0].Id)
	if err != nil {
		return "", err
	}

	if result.Status["text"] == "Stuck" || result.Status["text"] == "Error" {
		return result.Status["message"].(string), nil
	}

	return result.Result, nil
}

func PollCommandExecution(ac *client.AlpaconClient, cmdId string) (EventDetails, error) {
	var response EventDetails

	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			return response, errors.New("command execution timed out")
		case <-ticker.C:
			responseBody, err := ac.SendGetRequest(utils.BuildURL(getEventURL, cmdId, nil))
			if err != nil {
				continue
			}
			if err = json.Unmarshal(responseBody, &response); err != nil {
				return response, err
			}

			switch response.Status["text"] {
			case "Acked":
				timer.Reset(5 * time.Minute)
				continue
			default:
				return response, nil
			}
		}
	}
}
