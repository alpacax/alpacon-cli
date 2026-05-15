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

var (
	pollIdleTimeout          = 5 * time.Minute
	pollAbsoluteTimeout      = 30 * time.Minute
	pollTickInterval         = 1 * time.Second
	pollMaxConsecutiveErrors = 10
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
	params := map[string]string{}
	if pageSize > 0 {
		params["page_size"] = fmt.Sprintf("%d", pageSize)
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
			Server:      event.Server.Name,
			Shell:       event.Shell,
			Command:     event.Line,
			Result:      utils.TruncateString(event.Result, 70),
			Status:      utils.BoolPointerToString(event.Success),
			Operator:    event.RequestedBy.Name,
			RequestedAt: utils.TimeUtils(event.AddedAt),
		})
	}
	return eventList, nil
}

func RunCommand(ac *client.AlpaconClient, serverName, command string, username, groupname string, env map[string]string, workSessionID string) (string, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return "", err
	}

	commandRequest := &CommandRequest{
		Shell:       "system",
		Line:        command,
		Env:         env,
		Username:    username,
		Groupname:   groupname,
		Server:      serverID,
		RunAfter:    []string{},
		WorkSession: workSessionID,
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

	result, err := PollCommandExecution(ac, cmdResponse[0].ID)
	if err != nil {
		return "", err
	}

	if result.Status == "stuck" || result.Status == "error" {
		return fmt.Sprintf("command failed with status: %s", result.Status), nil
	}

	return result.Result, nil
}

func PollCommandExecution(ac *client.AlpaconClient, cmdID string) (EventDetails, error) {
	var response EventDetails
	var lastErr error
	consecutiveErrors := 0

	idleTimer := time.NewTimer(pollIdleTimeout)
	defer idleTimer.Stop()
	absoluteTimer := time.NewTimer(pollAbsoluteTimeout)
	defer absoluteTimer.Stop()
	ticker := time.NewTicker(pollTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-absoluteTimer.C:
			if lastErr != nil {
				return response, fmt.Errorf("command execution timed out (absolute timeout): %w", lastErr)
			}
			return response, errors.New("command execution timed out (absolute timeout)")
		case <-idleTimer.C:
			if lastErr != nil {
				return response, fmt.Errorf("command execution timed out (idle timeout): %w", lastErr)
			}
			return response, errors.New("command execution timed out (idle timeout)")
		case <-ticker.C:
			responseBody, err := ac.SendGetRequest(utils.BuildURL(getEventURL, cmdID, nil))
			if err != nil {
				lastErr = err
				consecutiveErrors++
				if consecutiveErrors >= pollMaxConsecutiveErrors {
					return response, fmt.Errorf("polling stopped after %d consecutive errors: %w", consecutiveErrors, lastErr)
				}
				continue
			}
			consecutiveErrors = 0
			lastErr = nil
			if err = json.Unmarshal(responseBody, &response); err != nil {
				return response, err
			}
			switch response.Status {
			case "queued", "scheduled", "delivered", "verifying", "running", "acked":
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(pollIdleTimeout)
				continue
			default:
				return response, nil
			}
		}
	}
}
