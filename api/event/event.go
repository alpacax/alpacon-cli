package event

import (
	"encoding/json"
	"fmt"
	"os"
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

func SubmitCommand(ac *client.AlpaconClient, serverName, command string, username, groupname string, env map[string]string, workSessionID string) (CommandResponse, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return CommandResponse{}, err
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
		return CommandResponse{}, err
	}
	var cmdResponse []CommandResponse
	if err = json.Unmarshal(respBody, &cmdResponse); err != nil {
		return CommandResponse{}, err
	}
	if len(cmdResponse) == 0 {
		return CommandResponse{}, fmt.Errorf("server returned empty command list")
	}
	return cmdResponse[0], nil
}

func RunCommand(ac *client.AlpaconClient, serverName, command string, username, groupname string, env map[string]string, workSessionID string) (string, error) {
	cmdResponse, err := SubmitCommand(ac, serverName, command, username, groupname, env, workSessionID)
	if err != nil {
		return "", err
	}

	result, err := PollCommandExecution(ac, cmdResponse.ID)
	if err != nil {
		return "", err
	}

	if result.Status == "stuck" || result.Status == "error" || result.Status == "cancelled" {
		if result.ErrorPhase != nil && *result.ErrorPhase != "" {
			return "", fmt.Errorf("command failed: [%s] %s (status=%s)",
				*result.ErrorPhase, DescribePhase(*result.ErrorPhase), result.Status)
		}
		return "", fmt.Errorf("command failed with status: %s", result.Status)
	}

	if result.Success != nil && !*result.Success {
		// Trust the server contract: alpamon sets success=(exitCode==0), so a
		// non-nil exit_code is propagated as-is.
		exitCode := 1
		if result.ExitCode != nil {
			exitCode = *result.ExitCode
		}
		errorPhase := ""
		if result.ErrorPhase != nil {
			errorPhase = *result.ErrorPhase
		}
		return result.Result, &RemoteCommandError{
			Output:     result.Result,
			ExitCode:   exitCode,
			ErrorPhase: errorPhase,
		}
	}

	if result.Success == nil && result.Status != "completed" && result.Status != "success" {
		return result.Result, fmt.Errorf("command ended with unrecognised status: %s", result.Status)
	}

	return result.Result, nil
}

func GetCommandByID(ac *client.AlpaconClient, cmdID string) (EventDetails, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(getEventURL, cmdID, nil))
	if err != nil {
		return EventDetails{}, err
	}
	var response EventDetails
	if err = json.Unmarshal(responseBody, &response); err != nil {
		return EventDetails{}, err
	}
	return response, nil
}

// PollCommandExecution polls with default timeout/tick; tests use pollCommandExecution directly.
func PollCommandExecution(ac *client.AlpaconClient, cmdId string) (EventDetails, error) {
	return pollCommandExecution(ac, cmdId, execTimeout(), 1*time.Second)
}

func execTimeout() time.Duration {
	if v := os.Getenv("ALPACON_EXEC_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		utils.CliWarning("ALPACON_EXEC_TIMEOUT=%q is not a valid duration (e.g. \"30m\", \"1h\"), using default 30m", v)
	}
	return 30 * time.Minute
}

func pollCommandExecution(ac *client.AlpaconClient, cmdId string, timeout, tick time.Duration) (EventDetails, error) {
	var response EventDetails

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			return response, &ClientTimeoutError{}
		case <-ticker.C:
			responseBody, err := ac.SendGetRequest(utils.BuildURL(getEventURL, cmdId, nil))
			if err != nil {
				continue
			}
			if err = json.Unmarshal(responseBody, &response); err != nil {
				return response, err
			}

			if IsRunningStatus(response.Status) {
				// Drain timer.C before Reset to prevent a spurious ClientTimeoutError if the timer fires between Stop and Reset.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
				continue
			}
			return response, nil
		}
	}
}
