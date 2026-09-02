package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

	// Base gap the pacing above multiplies, for the poll running behind a live
	// stream.
	streamPollTick = time.Second
)

// maxGapWidth caps enumerated missing seqs (per gap and cumulatively) so a buggy or
// hostile server can't spin the CLI or grow the skipped/missing slices without bound.
// var, not const, so tests can lower it.
var maxGapWidth = 100_000

// Gap-fill re-fetches back off exponentially while a seq stays missing, then give up
// after gapFillMaxNoProgress no-progress attempts (~34s: 0.3+0.6+1.2+2.4+4.8+5*5).
// var, not const, so tests can shorten them.
var (
	gapFillInitialInterval = 300 * time.Millisecond
	gapFillBackoffFactor   = 2
	gapFillMaxInterval     = 5 * time.Second
	gapFillMaxNoProgress   = 11
)

// gapFillNow is a seam so tests drive backoff timing deterministically.
var gapFillNow = time.Now

// errPollCancelled ends a poll whose caller has already finished—a stream that
// exited on the fin event—so it stops instead of spending another request of the
// throttle budget this pacing exists to protect.
var errPollCancelled = errors.New("poll cancelled")

// pollSeams are the hooks only a stream or a test supplies: cancellation, and a
// clock in place of real time. The zero value polls uncancellably on the real one.
// Passed rather than kept in package vars—a poll outlives the stream that started
// it by an in-flight request, and a test swapping a var would swap it under a
// loop still running.
type pollSeams struct {
	cancel <-chan struct{}
	now    func() time.Time
	after  func(time.Duration) <-chan time.Time
}

func (s pollSeams) Now() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// After hands back a channel rather than blocking, so a wait can lose to cancel.
func (s pollSeams) After(d time.Duration) <-chan time.Time {
	if s.after == nil {
		return time.After(d)
	}
	return s.after(d)
}

// gapFillState throttles gap-fill re-fetches while lastSeq is not advancing and
// records seqs given up on so they can be retried once at command end.
type gapFillState struct {
	lastAttempt time.Time
	noProgress  int
	skipped     []int
}

// giveUpGap advances past a never-filled hole, delivering the chunks it has and
// recording the still-missing seqs for one retry at command end. nextSeq is the
// live chunk that exposed the hole.
func (g *gapFillState) giveUpGap(lastSeq, nextSeq int, missing []Chunk, out io.Writer) int {
	if nextSeq-lastSeq-1 > maxGapWidth {
		// Gap too wide to track per-seq (buggy/hostile server): skip recording, but
		// still deliver fetched chunks (missing is response-bounded, the range isn't).
		for _, c := range missing {
			if c.Seq > lastSeq && c.Seq < nextSeq {
				_, _ = fmt.Fprint(out, c.Content)
			}
		}
		utils.CliWarning("chunk seq(s) %d..%d not arrived after %d attempts; skipping (gap too large to recover); output may be incomplete",
			lastSeq+1, nextSeq-1, gapFillMaxNoProgress)
		g.noProgress = 0
		return nextSeq - 1
	}
	bySeq := chunkContent(missing)
	var lost []int
	recorded := 0
	for s := lastSeq + 1; s < nextSeq; s++ {
		if content, ok := bySeq[s]; ok {
			_, _ = fmt.Fprint(out, content)
		} else {
			lost = append(lost, s)
			// Bound g.skipped's total growth so a hostile server can't accumulate
			// unbounded skips via many sub-maxGapWidth gaps over a long stream.
			if len(g.skipped) < maxGapWidth {
				g.skipped = append(g.skipped, s)
				recorded++
			}
		}
		lastSeq = s
	}
	switch {
	case recorded == len(lost):
		utils.CliWarning("chunk seq(s) %s not arrived after %d attempts; skipping for now (will retry at command end)",
			formatSeqs(lost), gapFillMaxNoProgress)
	case recorded > 0:
		// Budget filled mid-gap: only the recorded prefix gets the command-end retry.
		utils.CliWarning("chunk seq(s) %s not arrived after %d attempts; skipping (recorded %d of %d for retry at command end, skip budget exhausted); output may be incomplete",
			formatSeqs(lost), gapFillMaxNoProgress, recorded, len(lost))
	default:
		utils.CliWarning("chunk seq(s) %s not arrived after %d attempts; skipping (skip budget exhausted, no retry); output may be incomplete",
			formatSeqs(lost), gapFillMaxNoProgress)
	}
	g.noProgress = 0
	return lastSeq
}

// recoverSkipped makes a final fetch for seqs skipped mid-stream (giveUpGap),
// printing late content out of order and reporting still-absent seqs as lost.
// g.skipped is ascending, so the fetch is bounded to [first seq, last seq].
func (g *gapFillState) recoverSkipped(ac *client.AlpaconClient, cmdID string, out io.Writer) {
	if len(g.skipped) == 0 {
		return
	}
	final, err := getCommandChunks(ac, cmdID, g.skipped[0], g.skipped[len(g.skipped)-1])
	if err != nil {
		utils.CliWarning("failed to fetch skipped chunks (seq %s): %v; output may be incomplete", formatSeqs(g.skipped), err)
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
		utils.CliWarning("late chunk seq(s) %s recovered at command end (printed out of order)", formatSeqs(recovered))
	}
	if len(lost) > 0 {
		utils.CliWarning("chunk seq(s) %s never arrived; output may be incomplete", formatSeqs(lost))
	}
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

// GetEventList returns the newest tail commands. The endpoint sorts -scheduled_at,
// so the newest ones arrive first and the walk stops as soon as tail is reached.
func GetEventList(ac *client.AlpaconClient, tail int, serverName string, userName string) ([]EventAttributes, error) {
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

	endpoint := path.Join(getEventURL, serverID, userID)
	events, err := api.FetchPagesUpTo[EventDetails](ac, endpoint, nil, tail)
	if err != nil {
		return nil, err
	}

	eventList := make([]EventAttributes, 0, len(events))
	for _, event := range events {
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

func SubmitCommand(ac *client.AlpaconClient, serverName, command string, username, groupname string, env map[string]string, workSessionID, purpose string) (CommandResponse, error) {
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
		Purpose:     purpose,
		// Declared on every submission, including --detach: the demand is
		// reported to the caller the moment it is seen rather than waited out,
		// so nothing here can be left stalling for an answer it cannot give.
		PurposeDemandSupported: true,
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

// AnswerPurposeDemand states what a parked command is for and sends it back
// through verification (ADR 0052). The server re-judges it once with the purpose
// in hand; whatever that second verdict is—run, hold for a human, or deny—is
// reached by exactly the path an un-parked command takes.
//
// The server refuses a command that is not parked, and refuses an answer from
// anyone but the requester, with the same error either way: whether a given
// command is parked is not something a bystander needs to learn. So a failure
// here cannot be read as "the deadline passed" specifically.
func AnswerPurposeDemand(ac *client.AlpaconClient, cmdID, purpose string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(getEventURL, cmdID+"/purpose", nil), CommandPurposeRequest{Purpose: purpose})
	return err
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
func PollCommandExecution(ac *client.AlpaconClient, cmdID string) (EventDetails, error) {
	return pollCommandExecution(ac, cmdID, execTimeout(), 1*time.Second, false, pollSeams{})
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

// isPollWaitStatus reports a status the poll keeps waiting on: running, or—when
// resuming through an approval hold—the hold itself and the transient "error"
// compute_status the server emits in the approve→deliver window.
func isPollWaitStatus(status string, waitApproval bool) bool {
	return IsRunningStatus(status) ||
		(waitApproval && (IsAwaitingApprovalStatus(status) || status == "error"))
}

// waitApproval polls through the awaiting_approval hold—bounded by timeout, which
// the hold never resets and throttled waits extend by at most one timeout or
// utils.PollMaxThrottleExtensions grants, whichever binds first, plus one backoff
// wait—so an approved job resumes streaming. Without it the hold is terminal
// (PendingApprovalError). A closed seams.cancel ends the poll with errPollCancelled.
func pollCommandExecution(ac *client.AlpaconClient, cmdID string, timeout, tick time.Duration, waitApproval bool, seams pollSeams) (EventDetails, error) {
	var response EventDetails

	started := seams.Now()
	deadline := started.Add(timeout)
	delay := tick
	failures := 0
	budget := utils.NewThrottleBudget(timeout)

	for {
		select {
		case <-seams.cancel:
			return response, errPollCancelled
		default:
		}
		// Never sleep past the deadline, or the timeout lands a whole gap late—10
		// ticks at the slow pace, 60 once throttled. An extended deadline always
		// leaves the whole wait, so a throttled retry still gets its poll.
		if wait := min(delay, deadline.Sub(seams.Now())); wait > 0 {
			select {
			case <-seams.After(wait):
			case <-seams.cancel:
				return response, errPollCancelled
			}
		}
		if !seams.Now().Before(deadline) {
			return response, &ClientTimeoutError{}
		}

		responseBody, err := ac.SendGetRequest(utils.BuildURL(getEventURL, cmdID, nil))
		if err != nil {
			delay = utils.NextPollBackoff(tick, failures, utils.RetryAfter(err))
			failures++
			if utils.HTTPStatusCode(err) == http.StatusTooManyRequests {
				if budget.ShouldWarn() {
					// The wait can reach two timeouts, and nothing else on this path
					// prints while it lasts.
					utils.CliWarning("rate limited by the server, retrying in %s", delay)
				}
				// The server is alive: the command may be done, only its result GET refused.
				// Budgeted so a token stuck over quota gives up—extending by exactly the
				// wait would keep the deadline ahead forever.
				if newDeadline, extended := budget.Extend(deadline, delay); extended {
					deadline = newDeadline
				}
			}
			continue
		}
		failures = 0
		if err = json.Unmarshal(responseBody, &response); err != nil {
			return response, err
		}

		if IsRunningStatus(response.Status) {
			deadline = seams.Now().Add(timeout)
			budget.Reset()
			delay = utils.NextPollTick(tick, seams.Now().Sub(started))
			continue
		}
		// Running is handled above, so what is left here is the approval hold.
		if isPollWaitStatus(response.Status, waitApproval) {
			delay = utils.NextPollTick(tick, seams.Now().Sub(started))
			continue
		}
		return response, nil
	}
}

// RunCommandStreaming runs a command and streams its output to out over the
// event WebSocket, falling back to polling (runCommandFallback) when WS setup fails.
func RunCommandStreaming(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID, purpose string, out io.Writer) error {
	return runCommandStreamingWithWriter(ac, serverName, command, username, groupname, env, workSessionID, purpose, out)
}

func runCommandStreamingWithWriter(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID, purpose string, out io.Writer) error {
	listener := NewCommandOutputListener(ac)
	listener.Start()
	if !listener.WaitConnected(commandOutputConnectTimeout) {
		listener.Stop()
		return runCommandFallback(ac, serverName, command, username, groupname, env, workSessionID, purpose, out, listenerFailure(listener))
	}

	cmdResp, err := SubmitCommand(ac, serverName, command, username, groupname, env, workSessionID, purpose)
	if err != nil {
		listener.Stop()
		return err
	}

	return streamSubscribed(ac, listener, cmdResp.ID, cmdResp.Server.ID, out, execTimeout(), streamPollTick, false)
}

// StreamApprovedCommand resubscribes to an already-submitted command and streams
// its output, waiting through the awaiting_approval hold (bounded by timeout)
// until a reviewer approves it out of band and it runs. Used by --wait after a
// PendingApprovalError; the parked job produced no output yet, so resubscribing
// loses nothing.
func StreamApprovedCommand(ac *client.AlpaconClient, cmdID string, out io.Writer, timeout time.Duration) error {
	listener := NewCommandOutputListener(ac)
	listener.Start()
	if !listener.WaitConnected(commandOutputConnectTimeout) {
		listener.Stop()
		return runCommandFallbackFromID(ac, cmdID, out, true, listenerFailure(listener))
	}
	// The fin event targets the server, which only the command itself names here.
	// A failed read just skips the subscription; the poll still ends the run.
	var serverID string
	if details, err := GetCommandByID(ac, cmdID); err == nil {
		serverID = details.Server.ID
	}
	return streamSubscribed(ac, listener, cmdID, serverID, out, timeout, streamPollTick, true)
}

// listenerFailure names why the listener never connected: the last error a session
// attempt recorded, or the connect budget when no attempt got that far.
func listenerFailure(listener *CommandOutputListener) error {
	if err := listener.Err(); err != nil {
		return err
	}
	return fmt.Errorf("event websocket connect timeout")
}

// streamSubscribed subscribes to cmdID's output channel and to serverID's fin
// channel, warm-fires persisted chunks, then writes live chunks to out until the
// fin event or the poll reports a terminal state. Shared by the fresh-submit and
// approval-resume paths.
func streamSubscribed(ac *client.AlpaconClient, listener *CommandOutputListener, cmdID, serverID string, out io.Writer, timeout, tick time.Duration, waitApproval bool) error {
	if err := listener.subscribeTo(cmdID, serverID); err != nil {
		listener.Stop()
		return runCommandFallbackFromID(ac, cmdID, out, waitApproval, err)
	}

	// Warm-fire: drain any chunks already persisted. Advance lastSeq only over
	// contiguous seqs and stop at the first gap, so a later chunk filling that
	// gap (e.g. arriving over the WS) is still written instead of being skipped
	// as a duplicate. Chunks past the gap are picked up by applyChunk or the
	// terminal drain once the gap is filled.
	lastSeq := -1
	if existing, err := getCommandChunks(ac, cmdID, 0, noSeqBound); err == nil {
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
	// The poll ends when this function does: an exit on the fin event would
	// otherwise leave it polling a command that is already over.
	pollCancel := make(chan struct{})
	defer close(pollCancel)
	go func() {
		details, err := pollCommandExecution(ac, cmdID, timeout, tick, waitApproval, pollSeams{cancel: pollCancel})
		switch {
		case errors.Is(err, errPollCancelled): // the stream is gone; nobody reads these
		case err != nil:
			pollErr <- err
		default:
			pollResult <- details
		}
	}()

	// Runs on whichever terminal signal arrives first, the fin event or the poll.
	finish := func(details EventDetails) error {
		lastSeq = drainRemainingChunks(ac, cmdID, lastSeq, out)
		gap.recoverSkipped(ac, cmdID, out)
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
	}

	for {
		select {
		case chunk := <-listener.Chunks():
			lastSeq = applyChunk(ac, cmdID, lastSeq, chunk, out, gap)
		case <-listener.Finished():
			// fin says only that it is over; the exit code lives on the command. A
			// failed read, or one racing a state not yet written, leaves the poll as
			// the net rather than reporting a half-finished run.
			details, err := GetCommandByID(ac, cmdID)
			if err != nil || isPollWaitStatus(details.Status, waitApproval) {
				continue
			}
			return finish(details)
		case details := <-pollResult:
			return finish(details)
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

// applyChunk skips duplicates, fills gaps via REST, and writes content in seq order,
// returning the new lastSeq. While a gap stays open re-fetches back off; after
// gapFillMaxNoProgress attempts the missing seq is skipped so streaming resumes.
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
		missing, err := getCommandChunks(ac, cmdID, lastSeq+1, chunk.Seq-1)
		g.lastAttempt = gapFillNow()
		if err != nil {
			// Fetch failed: back off (noProgress++ keeps the throttle window
			// growing) but don't give up. Skipping requires a successful fetch
			// confirming the seq is absent, else a transient error would drop
			// output and warn "not arrived" for seqs that may exist.
			g.noProgress++
			utils.CliWarning("failed to fetch missing chunks (seq %d..%d): %v; output may be incomplete",
				lastSeq+1, chunk.Seq-1, err)
		} else {
			// Advance only over contiguous seqs, stopping at the first hole, so a
			// gap-fill racing ahead of persistence can't skip a not-yet-stored seq.
			// The c.Seq > chunk.Seq clip caps consumption at the live chunk's seq
			// (one past the requested seq__lte), so an old server that ignores
			// seq__lte and returns the whole tail can't overrun the live chunk.
			for _, c := range missing {
				if c.Seq != lastSeq+1 || c.Seq > chunk.Seq {
					break
				}
				_, _ = fmt.Fprint(out, c.Content)
				lastSeq = c.Seq
			}
			if lastSeq > before {
				g.noProgress = 0 // progress: a real gap-fill stays responsive
			} else {
				g.noProgress++
				if g.noProgress >= gapFillMaxNoProgress {
					lastSeq = g.giveUpGap(lastSeq, chunk.Seq, missing, out)
				}
			}
		}
	}
	if chunk.Seq == lastSeq+1 {
		_, _ = fmt.Fprint(out, chunk.Content)
		lastSeq = chunk.Seq
	}
	return lastSeq
}

// formatSeqs lists seqs verbatim up to a small limit, then collapses to a
// range summary so a near-maxGapWidth gap can't dump thousands of ints to stderr.
func formatSeqs(seqs []int) string {
	if len(seqs) <= 20 {
		return fmt.Sprintf("%v", seqs)
	}
	return fmt.Sprintf("%d..%d (%d seqs)", seqs[0], seqs[len(seqs)-1], len(seqs))
}

func chunkContent(chunks []Chunk) map[int]string {
	m := make(map[int]string, len(chunks))
	for _, c := range chunks {
		m[c.Seq] = c.Content
	}
	return m
}

func drainRemainingChunks(ac *client.AlpaconClient, cmdID string, lastSeq int, out io.Writer) int {
	final, err := getCommandChunks(ac, cmdID, lastSeq+1, noSeqBound)
	if err != nil {
		utils.CliWarning("failed to fetch trailing chunks (from seq %d): %v; output may be incomplete",
			lastSeq+1, err)
		return lastSeq
	}
	var missing []int
	truncated := false
	for _, c := range final {
		if c.Seq <= lastSeq {
			continue
		}
		// Report the hole before this chunk, but bound the total enumerated so a
		// hostile server can't grow missing without limit; excess is summarized.
		// Budget is compared by subtraction (len(missing) <= maxGapWidth, so no
		// overflow) rather than adding to a server-controlled gap.
		if gap := c.Seq - lastSeq - 1; gap > 0 {
			if gap > maxGapWidth-len(missing) {
				truncated = true
			} else {
				for s := lastSeq + 1; s < c.Seq; s++ {
					missing = append(missing, s)
				}
			}
		}
		_, _ = fmt.Fprint(out, c.Content)
		lastSeq = c.Seq
	}
	switch {
	case len(missing) > 0 && truncated:
		utils.CliWarning("chunk seq(s) %s and further gaps never arrived; output may be incomplete", formatSeqs(missing))
	case len(missing) > 0:
		utils.CliWarning("chunk seq(s) %s never arrived; output may be incomplete", formatSeqs(missing))
	case truncated:
		utils.CliWarning("chunk seq(s) never arrived (gap too large to list); output may be incomplete")
	}
	return lastSeq
}

// errorFromDetails maps a terminal command status to an error so unrecognized
// statuses are not masked as success.
func errorFromDetails(d EventDetails) error {
	// A switch case cannot call a predicate, so the two approval statuses are
	// matched ahead of it—a server-side rename then lands in types.go alone.
	if IsAwaitingPurposeStatus(d.Status) {
		return &AwaitingPurposeError{CommandID: d.ID, ExpiresAt: d.PurposeExpiresAt}
	}
	if IsAwaitingApprovalStatus(d.Status) {
		return &PendingApprovalError{CommandID: d.ID}
	}
	if IsRejectedStatus(d.Status) {
		return &CommandRejectedError{CommandID: d.ID}
	}
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
			return &RemoteCommandError{Output: d.Result, ExitCode: exitCode, ErrorPhase: phase, CommandID: d.ID}
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
func runCommandFallback(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID, purpose string, out io.Writer, cause error) error {
	cmdResp, err := SubmitCommand(ac, serverName, command, username, groupname, env, workSessionID, purpose)
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
	details, err := pollCommandExecution(ac, cmdID, execTimeout(), 1*time.Second, waitApproval, pollSeams{})
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
