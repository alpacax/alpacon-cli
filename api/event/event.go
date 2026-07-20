package event

import (
	"encoding/json"
	"errors"
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

// maxGapWidth bounds how many missing seqs we enumerate, both per gap and
// cumulatively. A command's chunk count is capped well below this server-side,
// so exceeding it can only come from a buggy or hostile server; enumerating a
// server-controlled seq delta would spin the CLI and grow the skipped/missing
// slices without bound. var (not const) so tests can exercise the bound cheaply.
var maxGapWidth = 100_000

// Gap-fill backoff knobs. While a chunk seq stays missing, gap-fill re-fetches
// back off exponentially instead of firing on every WS chunk; a run of
// gapFillMaxNoProgress no-progress attempts (~34s: 0.3+0.6+1.2+2.4+4.8+5*5)
// gives up on the seq. var (not const) so tests can shorten them.
var (
	gapFillInitialInterval = 300 * time.Millisecond
	gapFillBackoffFactor   = 2
	gapFillMaxInterval     = 5 * time.Second
	gapFillMaxNoProgress   = 11
)

// gapFillNow is a seam so tests drive backoff timing deterministically.
var gapFillNow = time.Now

// gapFillState throttles gap-fill re-fetches while lastSeq is not advancing and
// records seqs given up on so they can be retried once at command end.
type gapFillState struct {
	lastAttempt time.Time
	noProgress  int
	skipped     []int
}

func gapFillInterval(noProgress int) time.Duration {
	d := gapFillInitialInterval
	for i := 1; i < noProgress; i++ {
		d *= time.Duration(gapFillBackoffFactor)
		if d >= gapFillMaxInterval {
			return gapFillMaxInterval
		}
	}
	return d
}

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
	return pollCommandExecution(ac, cmdId, execTimeout(), 1*time.Second, false)
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

// waitApproval keeps polling through the awaiting_approval hold (bounded by
// timeout, no reset) so an approved job resumes streaming; otherwise that hold
// is terminal and surfaces as a PendingApprovalError via errorFromDetails.
func pollCommandExecution(ac *client.AlpaconClient, cmdId string, timeout, tick time.Duration, waitApproval bool) (EventDetails, error) {
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
			// Approval-resume keeps polling through the hold and the transient
			// "error" compute_status the server emits in the approve→deliver window.
			if waitApproval && (IsAwaitingApprovalStatus(response.Status) || response.Status == "error") {
				continue
			}
			return response, nil
		}
	}
}

// RunCommandStreaming runs a command and streams its output to out over the
// event WebSocket, falling back to polling (runCommandFallback) when WS setup fails.
func RunCommandStreaming(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string, out io.Writer) error {
	return runCommandStreamingWithWriter(ac, serverName, command, username, groupname, env, workSessionID, out)
}

func runCommandStreamingWithWriter(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string, out io.Writer) error {
	session, err := CreateEventSession(ac)
	if err != nil {
		return runCommandFallback(ac, serverName, command, username, groupname, env, workSessionID, out, err)
	}

	listener := NewCommandOutputListener(ac, session.WebsocketURL, "")
	listener.Start()
	if !listener.WaitConnected(commandOutputConnectTimeout) {
		listener.Stop()
		return runCommandFallback(ac, serverName, command, username, groupname, env, workSessionID, out, fmt.Errorf("event websocket connect timeout"))
	}

	cmdResp, err := SubmitCommand(ac, serverName, command, username, groupname, env, workSessionID)
	if err != nil {
		listener.Stop()
		return err
	}
	listener.setCommandID(cmdResp.ID)

	return streamSubscribed(ac, session, listener, cmdResp.ID, out, execTimeout(), false)
}

// StreamApprovedCommand resubscribes to an already-submitted command and streams
// its output, waiting through the awaiting_approval hold (bounded by timeout)
// until a reviewer approves it out of band and it runs. Used by --wait after a
// PendingApprovalError; the parked job produced no output yet, so resubscribing
// loses nothing.
func StreamApprovedCommand(ac *client.AlpaconClient, cmdID string, out io.Writer, timeout time.Duration) error {
	session, err := CreateEventSession(ac)
	if err != nil {
		return runCommandFallbackFromID(ac, cmdID, out, true, err)
	}
	listener := NewCommandOutputListener(ac, session.WebsocketURL, cmdID)
	listener.Start()
	if !listener.WaitConnected(commandOutputConnectTimeout) {
		listener.Stop()
		return runCommandFallbackFromID(ac, cmdID, out, true, fmt.Errorf("event websocket connect timeout"))
	}
	return streamSubscribed(ac, session, listener, cmdID, out, timeout, true)
}

// streamSubscribed subscribes to cmdID's output channel, warm-fires persisted
// chunks, then writes live chunks to out until the command reaches a terminal
// state. Shared by the fresh-submit and approval-resume paths.
func streamSubscribed(ac *client.AlpaconClient, session *EventSessionResponse, listener *CommandOutputListener, cmdID string, out io.Writer, timeout time.Duration, waitApproval bool) error {
	if err := SubscribeCommandOutput(ac, session.ChannelID, cmdID); err != nil {
		listener.Stop()
		return runCommandFallbackFromID(ac, cmdID, out, waitApproval, err)
	}

	// Warm-fire: drain any chunks already persisted. Advance lastSeq only over
	// contiguous seqs and stop at the first gap, so a later chunk filling that
	// gap (e.g. arriving over the WS) is still written instead of being skipped
	// as a duplicate. Chunks past the gap are picked up by applyChunk or the
	// terminal drain once the gap is filled.
	lastSeq := -1
	if existing, err := GetCommandChunks(ac, cmdID, 0); err == nil {
		for _, c := range existing {
			if c.Seq != lastSeq+1 {
				break
			}
			_, _ = fmt.Fprint(out, c.Content)
			lastSeq = c.Seq
		}
	}

	gap := &gapFillState{}

	pollResult := make(chan EventDetails, 1)
	pollErr := make(chan error, 1)
	go func() {
		details, err := pollCommandExecution(ac, cmdID, timeout, 1*time.Second, waitApproval)
		if err != nil {
			pollErr <- err
			return
		}
		pollResult <- details
	}()

	for {
		select {
		case chunk := <-listener.Chunks():
			lastSeq = applyChunk(ac, cmdID, lastSeq, chunk, out, gap)
		case details := <-pollResult:
			lastSeq = drainRemainingChunks(ac, cmdID, lastSeq, out)
			recoverSkippedChunks(ac, cmdID, gap, out)
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
			// --wait elapsed while still parked: keep the exit-4 pending contract
			// instead of surfacing a generic client timeout.
			var timeout *ClientTimeoutError
			if waitApproval && errors.As(err, &timeout) {
				return &PendingApprovalError{CommandID: cmdID}
			}
			return err
		}
	}
}

// applyChunk skips duplicates, fills gaps via REST, and writes content in seq
// order, returning the new lastSeq. While a gap does not close, re-fetches back
// off; after gapFillMaxNoProgress no-progress attempts the missing seq is
// skipped (recorded in g.skipped) so live streaming resumes.
func applyChunk(ac *client.AlpaconClient, cmdID string, lastSeq int, chunk ChunkEvent, out io.Writer, g *gapFillState) int {
	if chunk.Seq <= lastSeq {
		return lastSeq
	}
	if chunk.Seq > lastSeq+1 {
		// Throttle: while lastSeq is stuck, re-fetch only after the backoff window.
		if g.noProgress > 0 && gapFillNow().Sub(g.lastAttempt) < gapFillInterval(g.noProgress) {
			return lastSeq
		}
		before := lastSeq
		missing, err := GetCommandChunks(ac, cmdID, lastSeq+1)
		g.lastAttempt = gapFillNow()
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
		if lastSeq > before {
			g.noProgress = 0 // progress: a real gap-fill stays responsive
		} else {
			g.noProgress++
			if g.noProgress >= gapFillMaxNoProgress {
				lastSeq = g.giveUpGap(lastSeq, chunk, missing, out)
			}
		}
	}
	if chunk.Seq == lastSeq+1 {
		_, _ = fmt.Fprint(out, chunk.Content)
		lastSeq = chunk.Seq
	}
	return lastSeq
}

func chunkContent(chunks []Chunk) map[int]string {
	m := make(map[int]string, len(chunks))
	for _, c := range chunks {
		m[c.Seq] = c.Content
	}
	return m
}

// giveUpGap advances past a hole that never filled: it prints any persisted
// chunks between lastSeq and chunk.Seq, records the still-missing seqs in
// g.skipped for a final retry at command end, and resets the backoff.
func (g *gapFillState) giveUpGap(lastSeq int, chunk ChunkEvent, missing []Chunk, out io.Writer) int {
	if chunk.Seq-lastSeq-1 > maxGapWidth {
		utils.CliWarning("chunk seq(s) %d..%d not arrived after %d attempts; skipping for now (gap too large to enumerate)",
			lastSeq+1, chunk.Seq-1, gapFillMaxNoProgress)
		g.noProgress = 0
		g.lastAttempt = time.Time{}
		return chunk.Seq - 1
	}
	bySeq := chunkContent(missing)
	var lost []int
	recorded := 0
	for s := lastSeq + 1; s < chunk.Seq; s++ {
		if content, ok := bySeq[s]; ok {
			_, _ = fmt.Fprint(out, content)
		} else {
			lost = append(lost, s)
			// g.skipped is retried once at command end; bound its total growth so
			// a hostile server can't accumulate unbounded skips via many
			// sub-maxGapWidth gaps over a long stream.
			if len(g.skipped) < maxGapWidth {
				g.skipped = append(g.skipped, s)
				recorded++
			}
		}
		lastSeq = s
	}
	if recorded == len(lost) {
		utils.CliWarning("chunk seq(s) %v not arrived after %d attempts; skipping for now (will retry at command end)",
			lost, gapFillMaxNoProgress)
	} else {
		utils.CliWarning("chunk seq(s) %v not arrived after %d attempts; skipping (skip budget exhausted, no retry); output may be incomplete",
			lost, gapFillMaxNoProgress)
	}
	g.noProgress = 0
	g.lastAttempt = time.Time{}
	return lastSeq
}

func drainRemainingChunks(ac *client.AlpaconClient, cmdID string, lastSeq int, out io.Writer) int {
	final, err := GetCommandChunks(ac, cmdID, lastSeq+1)
	if err != nil {
		utils.CliWarning("failed to fetch trailing chunks (from seq %d): %v; output may be incomplete",
			lastSeq+1, err)
		return lastSeq
	}
	var missing []int
	for _, c := range final {
		if c.Seq <= lastSeq {
			continue
		}
		// Any seqs between lastSeq and this chunk never persisted: deliver what
		// is here but report the holes instead of silently jumping past them.
		// Bound the enumeration both per-gap and cumulatively so a hostile server
		// can't grow missing without limit via one huge or many summed gaps.
		if c.Seq-lastSeq-1 > maxGapWidth || len(missing) >= maxGapWidth {
			utils.CliWarning("chunk seq(s) %d..%d never arrived; output may be incomplete", lastSeq+1, c.Seq-1)
		} else {
			for s := lastSeq + 1; s < c.Seq; s++ {
				missing = append(missing, s)
			}
		}
		_, _ = fmt.Fprint(out, c.Content)
		lastSeq = c.Seq
	}
	if len(missing) > 0 {
		utils.CliWarning("chunk seq(s) %v never arrived; output may be incomplete", missing)
	}
	return lastSeq
}

// recoverSkippedChunks makes a final attempt to fetch seqs that were skipped
// mid-stream (giveUpGap). Late-persisted content is printed out of order; seqs
// still absent are reported as permanently lost. g.skipped is ascending.
func recoverSkippedChunks(ac *client.AlpaconClient, cmdID string, g *gapFillState, out io.Writer) {
	if len(g.skipped) == 0 {
		return
	}
	final, err := GetCommandChunks(ac, cmdID, g.skipped[0])
	if err != nil {
		utils.CliWarning("failed to fetch skipped chunks (seq %v): %v; output may be incomplete", g.skipped, err)
		return
	}
	bySeq := chunkContent(final)
	var recovered, lost []int
	for _, s := range g.skipped {
		if content, ok := bySeq[s]; ok {
			_, _ = fmt.Fprint(out, content)
			recovered = append(recovered, s)
		} else {
			lost = append(lost, s)
		}
	}
	if len(recovered) > 0 {
		utils.CliWarning("late chunk seq(s) %v recovered at command end (printed out of order)", recovered)
	}
	if len(lost) > 0 {
		utils.CliWarning("chunk seq(s) %v never arrived; output may be incomplete", lost)
	}
}

// errorFromDetails maps a terminal command status to an error so unrecognized
// statuses are not masked as success.
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
	case "awaiting_approval":
		return &PendingApprovalError{CommandID: d.ID}
	case "rejected":
		return fmt.Errorf("command was rejected by a reviewer")
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
	return runCommandFallbackFromID(ac, cmdResp.ID, out, false, cause)
}

// runCommandFallbackFromID polls an already-submitted command by ID (instead of
// re-submitting) and writes its output to out. Used when streaming setup fails
// after SubmitCommand has created the command. waitApproval keeps the poll
// blocking through an awaiting_approval hold so --wait is honored on this path.
func runCommandFallbackFromID(ac *client.AlpaconClient, cmdID string, out io.Writer, waitApproval bool, cause error) error {
	utils.CliWarning("real-time output unavailable (%v); falling back to polling", cause)
	details, err := pollCommandExecution(ac, cmdID, execTimeout(), 1*time.Second, waitApproval)
	if err != nil {
		return err
	}
	// Command has finished: reconstruct output from chunks best-effort, falling
	// back to Result when chunks are empty or unavailable. No warning on failure—
	// the polling-fallback warning above already covers it.
	output := details.Result
	if reconstructed, oerr := GetCommandOutput(ac, cmdID); oerr == nil && reconstructed != "" {
		output = reconstructed
	}
	if output != "" {
		_, _ = fmt.Fprint(out, output)
	}
	return errorFromDetails(details)
}
