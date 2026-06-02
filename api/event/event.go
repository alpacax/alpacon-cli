package event

import (
	"encoding/json"
	"fmt"
	"io"
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

// RunCommandStreaming runs a command and streams its output to stdout over the
// event WebSocket, falling back to RunCommand (polling) when WS setup fails.
func RunCommandStreaming(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string) error {
	return runCommandStreamingWithWriter(ac, serverName, command, username, groupname, env, workSessionID, os.Stdout)
}

func runCommandStreamingWithWriter(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string, out io.Writer) error {
	session, err := CreateEventSession(ac)
	if err != nil {
		return runCommandFallback(ac, serverName, command, username, groupname, env, workSessionID, out, err)
	}

	listener := NewCommandOutputListener(ac, session.WebsocketURL, "")
	listener.Start()
	if !listener.WaitConnected(5 * time.Second) {
		listener.Stop()
		return runCommandFallback(ac, serverName, command, username, groupname, env, workSessionID, out, fmt.Errorf("event websocket connect timeout"))
	}

	cmdResp, err := SubmitCommand(ac, serverName, command, username, groupname, env, workSessionID)
	if err != nil {
		listener.Stop()
		return err
	}
	listener.setCommandID(cmdResp.ID)

	if err := SubscribeCommandOutput(ac, session.ChannelID, cmdResp.ID); err != nil {
		listener.Stop()
		return runCommandFallbackFromID(ac, cmdResp.ID, out, err)
	}

	// Warm-fire: drain any chunks already persisted. Advance lastSeq only over
	// contiguous seqs and stop at the first gap, so a later chunk filling that
	// gap (e.g. arriving over the WS) is still written instead of being skipped
	// as a duplicate. Chunks past the gap are picked up by applyChunk or the
	// terminal drain once the gap is filled.
	lastSeq := -1
	if existing, err := GetCommandChunks(ac, cmdResp.ID, 0); err == nil {
		for _, c := range existing {
			if c.Seq != lastSeq+1 {
				break
			}
			_, _ = fmt.Fprint(out, c.Content)
			lastSeq = c.Seq
		}
	}

	pollResult := make(chan EventDetails, 1)
	pollErr := make(chan error, 1)
	go func() {
		details, err := PollCommandExecution(ac, cmdResp.ID)
		if err != nil {
			pollErr <- err
			return
		}
		pollResult <- details
	}()

	for {
		select {
		case chunk := <-listener.Chunks():
			lastSeq = applyChunk(ac, cmdResp.ID, lastSeq, chunk, out)
		case details := <-pollResult:
			lastSeq = drainRemainingChunks(ac, cmdResp.ID, lastSeq, out)
			listener.Stop()
			// If nothing was ever streamed (no WS chunks, none persisted), fall back
			// to the buffered Result so output is never silently dropped. On a normal
			// streamed run lastSeq has advanced and this is skipped.
			if lastSeq < 0 && details.Result != "" {
				_, _ = fmt.Fprint(out, details.Result)
			}
			// Output is already streamed; errorFromDetails keeps it on the error
			// for inspection (e.g. sudo-denial hint) but cmd/exec never reprints it.
			return errorFromDetails(details)
		case err := <-pollErr:
			listener.Stop()
			return err
		}
	}
}

// applyChunk skips duplicates, fills gaps via REST, and writes content in seq
// order, returning the new lastSeq. When REST already returned the incoming
// chunk's seq, the final clause is intentionally skipped to avoid reprinting it.
func applyChunk(ac *client.AlpaconClient, cmdID string, lastSeq int, chunk ChunkEvent, out io.Writer) int {
	if chunk.Seq <= lastSeq {
		return lastSeq
	}
	if chunk.Seq > lastSeq+1 {
		missing, err := GetCommandChunks(ac, cmdID, lastSeq+1)
		if err != nil {
			utils.CliWarning("failed to fetch missing chunks (seq %d..%d): %v; output may be incomplete",
				lastSeq+1, chunk.Seq-1, err)
		} else {
			// Advance only over contiguous seqs, stopping at the first hole, so a
			// gap-fill racing ahead of persistence can't skip a not-yet-stored seq.
			for _, c := range missing {
				if c.Seq != lastSeq+1 || c.Seq > chunk.Seq {
					break
				}
				_, _ = fmt.Fprint(out, c.Content)
				lastSeq = c.Seq
			}
		}
	}
	if chunk.Seq == lastSeq+1 {
		_, _ = fmt.Fprint(out, chunk.Content)
		lastSeq = chunk.Seq
	}
	return lastSeq
}

func drainRemainingChunks(ac *client.AlpaconClient, cmdID string, lastSeq int, out io.Writer) int {
	final, err := GetCommandChunks(ac, cmdID, lastSeq+1)
	if err != nil {
		utils.CliWarning("failed to fetch trailing chunks (from seq %d): %v; output may be incomplete",
			lastSeq+1, err)
		return lastSeq
	}
	for _, c := range final {
		if c.Seq > lastSeq {
			_, _ = fmt.Fprint(out, c.Content)
			lastSeq = c.Seq
		}
	}
	return lastSeq
}

// errorFromDetails maps a terminal command status to an error, mirroring
// RunCommand so unrecognized statuses are not masked as success.
func errorFromDetails(d EventDetails) error {
	switch d.Status {
	case "completed", "success", "failed":
		if d.Success != nil && !*d.Success {
			exitCode := 1
			if d.ExitCode != nil {
				exitCode = *d.ExitCode
			}
			phase := ""
			if d.ErrorPhase != nil {
				phase = *d.ErrorPhase
			}
			return &RemoteCommandError{Output: d.Result, ExitCode: exitCode, ErrorPhase: phase}
		}
		return nil
	case "stuck", "error", "cancelled":
		phase := ""
		if d.ErrorPhase != nil {
			phase = *d.ErrorPhase
		}
		if phase == "" {
			return fmt.Errorf("command failed with status: %s", d.Status)
		}
		return fmt.Errorf("command failed: [%s] %s (status=%s)", phase, DescribePhase(phase), d.Status)
	default:
		return fmt.Errorf("unexpected command status: %s (command may still be running)", d.Status)
	}
}

// runCommandFallback warns the user and delegates to the existing polling flow.
func runCommandFallback(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string, out io.Writer, cause error) error {
	cmdResp, err := SubmitCommand(ac, serverName, command, username, groupname, env, workSessionID)
	if err != nil {
		// Surface MFA/auth errors so RunCommandWithRetry's callbacks can handle them.
		return err
	}
	return runCommandFallbackFromID(ac, cmdResp.ID, out, cause)
}

// runCommandFallbackFromID polls an already-submitted command by ID (instead of
// re-submitting) and writes its buffered result to out. Used when streaming
// setup fails after SubmitCommand has created the command.
func runCommandFallbackFromID(ac *client.AlpaconClient, cmdID string, out io.Writer, cause error) error {
	utils.CliWarning("real-time output unavailable (%v); falling back to polling", cause)
	details, err := PollCommandExecution(ac, cmdID)
	if err != nil {
		return err
	}
	if details.Result != "" {
		_, _ = fmt.Fprint(out, details.Result)
	}
	return errorFromDetails(details)
}
