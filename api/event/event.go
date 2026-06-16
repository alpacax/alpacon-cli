package event

import (
	"encoding/json"
	"errors"
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

	return classifyCommandResult(result)
}

// classifyCommandResult maps a polled command's terminal (or held) state to a
// (output, error) pair. It is shared by RunCommand and WaitForCommandApproval so
// the synchronous run and the --wait approval poll classify outcomes identically.
func classifyCommandResult(result EventDetails) (string, error) {
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

	// HITL: the command is parked server-side pending human approval. Surface a
	// typed signal so the caller can emit the pending-approval contract (exit 4)
	// or, with --wait, poll this same job until it is approved.
	if IsAwaitingApprovalStatus(result.Status) {
		return result.Result, &PendingApprovalError{CommandID: result.ID}
	}

	// A reviewer rejected the approval request: the command will never run.
	if result.Status == "rejected" {
		return result.Result, fmt.Errorf("command was rejected by a reviewer")
	}

	if result.Success == nil && result.Status != "completed" && result.Status != "success" {
		return result.Result, fmt.Errorf("command ended with unrecognised status: %s", result.Status)
	}

	return result.Result, nil
}

// isApprovalTransitionStatus reports whether status is a state the held command
// legitimately passes through while waiting for and applying an approval, so the
// approval poll keeps waiting instead of resolving. Besides "awaiting_approval"
// it includes "error": the server's documented-unreachable compute_status
// fallback, which surfaces transiently in the sub-millisecond window after an
// approval flips verification to "approved" but before the command is delivered
// (delivered_at is set). Treating it as transient here keeps the approve→deliver
// step from being misreported as a failure; the real failure states the command
// can reach (stuck, failed, cancelled, rejected) are classified normally.
func isApprovalTransitionStatus(status string) bool {
	return IsAwaitingApprovalStatus(status) || status == "error"
}

// WaitForCommandApproval polls a command parked at "awaiting_approval" until a
// reviewer approves it out of band (the same job then runs and resolves) or the
// timeout elapses while it is still parked. It polls the existing job rather than
// re-submitting, so the original command runs exactly once on approval. The
// approval wait is bounded by timeout (the timer is not reset while parked);
// once the command starts running the timer resets per tick so a long execution
// is not cut off. On timeout while still parked it returns a PendingApprovalError
// so the caller emits the standard pending-approval signal.
func WaitForCommandApproval(ac *client.AlpaconClient, commandID string, timeout, tick time.Duration) (string, error) {
	result, err := pollCommand(ac, commandID, timeout, tick, true)
	if err != nil {
		var clientTimeout *ClientTimeoutError
		if errors.As(err, &clientTimeout) && IsAwaitingApprovalStatus(result.Status) {
			return result.Result, &PendingApprovalError{CommandID: commandID}
		}
		return result.Result, err
	}
	return classifyCommandResult(result)
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
	return pollCommand(ac, cmdId, timeout, tick, false)
}

// pollCommand polls a command until it leaves the in-progress states. A running
// command resets the timer each tick so a long execution is never cut off. When
// awaitApproval is true, a command parked at "awaiting_approval" keeps being
// polled too, but the timer is NOT reset for it, so timeout bounds the wait for
// the out-of-band approval; when awaitApproval is false the parked status is
// returned immediately (the caller decides whether to wait).
func pollCommand(ac *client.AlpaconClient, cmdId string, timeout, tick time.Duration, awaitApproval bool) (EventDetails, error) {
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
			if awaitApproval && isApprovalTransitionStatus(response.Status) {
				// Keep waiting through the approval transition, but let timeout
				// bound this phase: do not reset the timer.
				continue
			}
			return response, nil
		}
	}
}
