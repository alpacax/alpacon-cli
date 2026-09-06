package websh

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

const (
	sessionsBaseURL     = "/api/websh/sessions/"
	userChannelsBaseURL = "/api/websh/user-channels/"

	ctrlC              = 0x03
	writeFlushInterval = 5 * time.Millisecond

	// sessionEndCloseCode is how the proxy closes a user channel at the end of a
	// session (sendCloseFrame in proxy-server internal/ws/channel.go); alpamon sends
	// the same 4000 for the same meaning. A websh session never ends with 1000, so
	// without this every normal end would be reported as a failure.
	sessionEndCloseCode = 4000

	// A close that does not mean the session ended is a dropped link—a restarted
	// service, a network interruption—and the session it was carrying is still
	// running on the server, so the client dials a new channel onto it instead of
	// ending the shell. The budget bounds a server that keeps accepting a channel
	// and dropping it: roughly 30s of waiting before the session is given up.
	maxReconnectAttempts   = 5
	reconnectBaseDelay     = 1 * time.Second
	reconnectMaxDelay      = 15 * time.Second
	reconnectBackoffFactor = 2
)

var (
	// terminalSize reports the local terminal's size, in rows and columns. A var so
	// a test can pin a size where there is no terminal to read one from.
	terminalSize = func() (rows, cols int, err error) {
		cols, rows, err = term.GetSize(int(os.Stdin.Fd()))
		return rows, cols, err
	}

	// ErrReconnectFailed ends a session whose connection dropped and could not be
	// re-established within the attempt budget.
	ErrReconnectFailed = errors.New("could not reconnect to the session")

	// ErrSessionGone ends a session the server refused a new channel on: it was
	// closed while the connection was down, so retrying only asks again.
	ErrSessionGone = errors.New("the session is no longer open")
)

// GetSessionList returns the newest tail connectable sessions. The endpoint sorts
// -added_at, so the newest ones arrive first and the walk stops as soon as tail is reached.
func GetSessionList(ac *client.AlpaconClient, tail int) ([]SessionListItem, error) {
	params := map[string]string{
		"is_connectable": "true",
	}

	sessions, err := api.FetchPagesUpTo[SessionDetailResponse](ac, sessionsBaseURL, params, tail)
	if err != nil {
		return nil, err
	}

	list := make([]SessionListItem, 0, len(sessions))
	for _, s := range sessions {
		closedAt := "-"
		if s.ClosedAt != nil {
			closedAt = *s.ClosedAt
		}
		list = append(list, SessionListItem{
			ID:       s.ID,
			Server:   s.Server.Name,
			User:     s.User.Name,
			Username: s.Username,
			RemoteIP: s.RemoteIP,
			AddedAt:  s.AddedAt,
			ClosedAt: closedAt,
		})
	}

	return list, nil
}

func GetSessionDetail(ac *client.AlpaconClient, sessionID string) ([]byte, error) {
	return ac.SendGetRequest(utils.BuildURL(sessionsBaseURL, sessionID, nil))
}

func GetSessionRecords(ac *client.AlpaconClient, sessionID, query string, limit int) ([]SessionRecord, error) {
	action := "records"
	params := map[string]string{}
	if query != "" {
		action = "search"
		params["q"] = query
	}

	endpoint := path.Join(sessionsBaseURL, sessionID, action)
	return api.FetchCursorPages[SessionRecord](ac, endpoint, params, limit)
}

func CloseSession(ac *client.AlpaconClient, sessionID string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(sessionsBaseURL, path.Join(sessionID, "close"), nil), nil)
	return err
}

func ForceCloseSession(ac *client.AlpaconClient, sessionID string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(sessionsBaseURL, path.Join(sessionID, "force-close"), nil), nil)
	return err
}

// ConnectToSession opens a read-only channel on someone else's session, which is
// what watching one is.
func ConnectToSession(ac *client.AlpaconClient, sessionID string) (SessionResponse, error) {
	return createUserChannel(ac, &ConnectRequest{
		Session:  sessionID,
		IsMaster: false,
		ReadOnly: true,
	})
}

// ReconnectToSession opens a new interactive channel on a session the caller is
// already running, so a client whose connection dropped can reattach. The
// session's PTY channel is untouched by a user channel closing, so the shell and
// everything running in it survive the gap.
func ReconnectToSession(ac *client.AlpaconClient, sessionID string) (SessionResponse, error) {
	return createUserChannel(ac, &ConnectRequest{
		Session:  sessionID,
		IsMaster: true,
		ReadOnly: false,
	})
}

// SendTerminalSize tells the server the terminal's current size. The server
// resizes the remote PTY on every update, so this also redraws a shell that spent
// a reconnect talking to nobody, even when the size itself has not changed.
func SendTerminalSize(ac *client.AlpaconClient, sessionID string) error {
	rows, cols, err := terminalSize()
	if err != nil {
		return err
	}

	_, err = ac.SendPatchRequest(
		utils.BuildURL(sessionsBaseURL, sessionID, nil),
		&SessionSizeRequest{Rows: rows, Cols: cols},
	)
	return err
}

func createUserChannel(ac *client.AlpaconClient, req *ConnectRequest) (SessionResponse, error) {
	responseBody, err := ac.SendPostRequest(userChannelsBaseURL, req)
	if err != nil {
		return SessionResponse{}, err
	}
	var response SessionResponse
	if err = json.Unmarshal(responseBody, &response); err != nil {
		return SessionResponse{}, err
	}
	return response, nil
}

func InviteToSession(ac *client.AlpaconClient, sessionID string, emails []string, readOnly bool) error {
	req := &InviteRequest{
		Emails:   emails,
		ReadOnly: readOnly,
	}
	_, err := ac.SendPostRequest(utils.BuildURL(sessionsBaseURL, path.Join(sessionID, "invite"), nil), req)
	return err
}

func JoinWebshSession(ac *client.AlpaconClient, sharedURL, password string) (SessionResponse, error) {
	parsedURL, err := url.Parse(sharedURL)
	if err != nil {
		return SessionResponse{}, err
	}

	channelID := parsedURL.Query().Get("channel")
	if channelID == "" {
		return SessionResponse{}, errors.New("invalid URL format")
	}
	joinRequest := &JoinRequest{
		Password: password,
	}

	relativePath := path.Join(channelID, "join")
	responseBody, err := ac.SendPostRequest(utils.BuildURL(userChannelsBaseURL, relativePath, nil), joinRequest)
	if err != nil {
		return SessionResponse{}, err
	}
	var response SessionResponse
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return SessionResponse{}, err
	}

	return response, nil
}

// BuildSessionRequest assembles the JSON body for a websh session create call.
// Empty workSessionID is omitted from the wire request via omitempty on the field.
func BuildSessionRequest(serverID, username, groupname string, rows, cols int, workSessionID string) *SessionRequest {
	return &SessionRequest{
		Server:      serverID,
		Username:    username,
		Groupname:   groupname,
		Rows:        rows,
		Cols:        cols,
		WorkSession: workSessionID,
	}
}

// Create new websh session
func CreateWebshSession(ac *client.AlpaconClient, serverName, username, groupname string, share, readOnly bool, workSessionID string) (SessionResponse, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return SessionResponse{}, err
	}

	rows, cols, err := terminalSize()
	if err != nil {
		return SessionResponse{}, err
	}

	sessionRequest := BuildSessionRequest(serverID, username, groupname, rows, cols, workSessionID)

	responseBody, err := ac.SendPostRequest(sessionsBaseURL, sessionRequest)
	if err != nil {
		return SessionResponse{}, err
	}

	var response SessionResponse
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return SessionResponse{}, err
	}

	if share {
		shareRequest := &ShareRequest{
			ReadOnly: readOnly,
		}
		var shareResponse ShareResponse
		relativePath := path.Join(response.ID, "share")
		responseBody, err = ac.SendPostRequest(utils.BuildURL(sessionsBaseURL, relativePath, nil), shareRequest)
		if err != nil {
			return SessionResponse{}, err
		}
		err = json.Unmarshal(responseBody, &shareResponse)
		if err != nil {
			return SessionResponse{}, err
		}
		sharingInfo(shareResponse)
	}

	return response, nil
}

func newWebsocketClient(header http.Header) *WebsocketClient {
	return &WebsocketClient{
		header: header,
		done:   make(chan struct{}),
	}
}

func (wsClient *WebsocketClient) dial(websocketURL string) error {
	conn, resp, err := websocket.DefaultDialer.Dial(websocketURL, wsClient.header)
	if err != nil {
		// The handshake response carries the reason a bad handshake alone never names.
		if resp == nil {
			return fmt.Errorf("websocket connection failed: %w", err)
		}
		return fmt.Errorf("websocket connection failed: %w (status %s)", err, utils.SanitizeTerminalText(resp.Status))
	}
	wsClient.conn = newConnection(conn)

	return nil
}

func newConnection(ws *websocket.Conn) *connection {
	return &connection{ws: ws, ended: make(chan struct{})}
}

// finish keeps the first ending. err is written before ended closes, so anyone
// who saw ended can read it.
func (c *connection) finish(err error) {
	c.endOnce.Do(func() {
		c.err = err
		close(c.ended)
	})
}

// close ends the connection and waits for its pumps, so a reconnect never starts
// with a goroutine still reading or writing the socket it replaced.
func (c *connection) close() {
	_ = c.ws.Close()
	c.pumps.Wait()
}

// startReader reads server output for as long as the connection lasts.
func (c *connection) startReader() {
	c.pumps.Add(1)
	go func() {
		defer c.pumps.Done()
		c.readFromServer()
	}()
}

// startWriter forwards user input for as long as the connection lasts. The input
// channel outlives the connection: one stdin reader feeds every connection of the
// session, since a goroutine parked in a terminal read cannot be released.
func (c *connection) startWriter(inputChan <-chan string) {
	c.pumps.Add(1)
	go func() {
		defer c.pumps.Done()
		c.writeToServer(inputChan)
	}()
}

// closeConn releases the current connection, if the session ever had one.
func (wsClient *WebsocketClient) closeConn() {
	if wsClient.conn != nil {
		wsClient.conn.close()
	}
}

// finish keeps the first outcome. err is written before done closes, so anyone who
// saw done can read it. A deliberate close ends the session rather than failing it;
// every other close code stays an error.
func (wsClient *WebsocketClient) finish(err error) {
	if endsSession(err) {
		err = nil
	}
	wsClient.finishOnce.Do(func() {
		wsClient.err = err
		close(wsClient.done)
	})
}

// endsSession reports whether a connection error means the session itself ended:
// the proxy's session-end code, or one of the two normal WebSocket closes. Every
// other ending—1012 on a restarted service, 1006 on a link that went away, a
// transport error with no close frame at all—leaves the session running on the
// server, so it is a connection to re-dial rather than a shell to close.
func endsSession(err error) bool {
	if err == nil {
		return true
	}
	return websocket.IsCloseError(err, sessionEndCloseCode, websocket.CloseNormalClosure, websocket.CloseGoingAway)
}

// reconnectDelay returns the wait before the attempt after the given number of
// failed ones: the base doubled once per failure, capped.
func reconnectDelay(failures int, base time.Duration) time.Duration {
	delay := min(base, reconnectMaxDelay)
	for range failures {
		delay *= reconnectBackoffFactor
		if delay >= reconnectMaxDelay {
			return reconnectMaxDelay
		}
	}
	return delay
}

// OpenReadOnlyTerminal opens a read-only terminal view for watching another user's session.
// Input is not forwarded to the server. Terminal echo is suppressed via raw mode.
// Ends cleanly on the remote close, on Ctrl+C, or on a signal.
func OpenReadOnlyTerminal(ac *client.AlpaconClient, sessionResponse SessionResponse) error {
	wsClient := newWebsocketClient(ac.SetWebsocketHeader())
	if err := wsClient.dial(sessionResponse.WebsocketURL); err != nil {
		return err
	}
	defer wsClient.closeConn()

	sigChan, stopSignals := notifySignals()
	defer stopSignals()

	restore, err := enterRawMode()
	if err != nil {
		return err
	}
	defer restore()

	conn := wsClient.conn
	go wsClient.watchInterrupt(sigChan)
	go wsClient.readCtrlC()
	conn.startReader()

	// A watcher does not reconnect: it holds no session of its own, and the one
	// it is watching may well have been what ended.
	select {
	case <-wsClient.done:
	case <-conn.ended:
		wsClient.finish(conn.err)
	}
	return wsClient.err
}

// watchInterrupt ends the session on a signal, and leaves once anything else has ended it.
func (wsClient *WebsocketClient) watchInterrupt(sigChan <-chan os.Signal) {
	select {
	case <-sigChan:
		wsClient.finish(nil)
	case <-wsClient.done:
	}
}

// readCtrlC ends the session on Ctrl+C — raw mode suppresses SIGINT, so it only
// ever arrives as a byte. Once anything else has ended the session it stops
// consuming stdin, though not before the read it is already parked in returns.
func (wsClient *WebsocketClient) readCtrlC() {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || (n > 0 && buf[0] == ctrlC) {
			wsClient.finish(nil)
			return
		}
		select {
		case <-wsClient.done:
			return
		default:
		}
	}
}

// OpenNewTerminal opens an interactive terminal on the session.
// Input is forwarded to the server. Terminal echo is suppressed via raw mode.
// Ends cleanly on the remote close, on Ctrl+D, or on a signal. A connection that
// drops for any other reason is re-dialed onto a new channel of the same session.
func OpenNewTerminal(ac *client.AlpaconClient, sessionResponse SessionResponse) error {
	return openInteractiveTerminal(ac, sessionResponse, newReconnector(ac, sessionResponse.ID))
}

// OpenSharedTerminal opens the interactive terminal of a session joined through a
// shared link. It does not reconnect: only the session's owner may open a new
// channel on it, the invite channel is single-use, and the join response names no
// session to ask about. A drop ends the terminal, and the link can be joined again.
func OpenSharedTerminal(ac *client.AlpaconClient, sessionResponse SessionResponse) error {
	return openInteractiveTerminal(ac, sessionResponse, nil)
}

func openInteractiveTerminal(ac *client.AlpaconClient, sessionResponse SessionResponse, reconnect *reconnector) error {
	var header http.Header
	if reconnect != nil {
		header = ac.SetWebsocketHeaderWithCapabilities(client.CapabilityWebsocketReconnect)
	} else {
		header = ac.SetWebsocketHeader()
	}

	wsClient := newWebsocketClient(header)
	wsClient.reconnect = reconnect

	if err := wsClient.dial(sessionResponse.WebsocketURL); err != nil {
		return err
	}
	defer wsClient.closeConn()

	return wsClient.runWsClient()
}

func newReconnector(ac *client.AlpaconClient, sessionID string) *reconnector {
	return &reconnector{
		provision: func() (string, error) {
			session, err := ReconnectToSession(ac, sessionID)
			if err != nil {
				return "", err
			}
			return session.WebsocketURL, nil
		},
		resendSize:  func() error { return SendTerminalSize(ac, sessionID) },
		notice:      os.Stderr,
		baseDelay:   reconnectBaseDelay,
		maxAttempts: maxReconnectAttempts,
	}
}

// runWsClient runs the session. Raw mode and the stdin reader are entered once and
// span every connection the session goes through: leaving raw mode between
// attempts would echo back whatever was typed into the gap, and re-entering it
// would repaint over the shell the reconnect is trying to restore.
func (wsClient *WebsocketClient) runWsClient() error {
	sigChan, stopSignals := notifySignals()
	defer stopSignals()

	restore, err := enterRawMode()
	if err != nil {
		return err
	}
	defer restore()

	inputChan := make(chan string, 1)

	go wsClient.watchInterrupt(sigChan)
	go wsClient.readUserInput(inputChan)

	wsClient.serveConnections(inputChan)

	return wsClient.err
}

// serveConnections runs the current connection until it ends, then either records
// the session's outcome or replaces the connection. It returns once the session
// has an outcome, which is always recorded by the time it does.
func (wsClient *WebsocketClient) serveConnections(inputChan <-chan string) {
	for {
		conn := wsClient.conn
		conn.startReader()
		conn.startWriter(inputChan)

		select {
		case <-wsClient.done:
			return
		case <-conn.ended:
		}

		// A connection ending and the session ending can land together—the remote
		// closing while the user types Ctrl+D. The session's own outcome wins.
		select {
		case <-wsClient.done:
			return
		default:
		}

		conn.close()

		if wsClient.reconnect == nil || endsSession(conn.err) {
			wsClient.finish(conn.err)
			return
		}
		if !wsClient.redial() {
			return
		}
	}
}

// redial replaces a dropped connection with a new channel on the same session,
// within a bounded number of attempts. It reports whether the session has a live
// connection again; on false the session's outcome is already recorded.
func (wsClient *WebsocketClient) redial() bool {
	r := wsClient.reconnect
	// The terminal is still in raw mode, where a bare newline leaves the cursor
	// where it stood, so the notice carries its own carriage returns.
	_, _ = fmt.Fprint(r.notice, "\r\nConnection lost, reconnecting...\r\n")

	for failures := range r.maxAttempts {
		if !wsClient.wait(reconnectDelay(failures, r.baseDelay)) {
			return false // the session ended while waiting
		}

		websocketURL, err := r.provision()
		if err != nil {
			// A 4xx is the server refusing rather than failing: the session was
			// closed while the link was down, and asking again only asks again.
			if utils.IsFatalClientError(utils.HTTPStatusCode(err)) {
				wsClient.finish(ErrSessionGone)
				return false
			}
			continue
		}
		if err := wsClient.dial(websocketURL); err != nil {
			continue
		}
		// Before the pumps, so the redraw the resize provokes lands on a socket
		// that is already being read. A size the server would not take is not
		// worth ending a working session over—the shell is usable, if possibly
		// drawn at the width it had before.
		if r.resendSize != nil {
			_ = r.resendSize()
		}
		return true
	}

	wsClient.finish(ErrReconnectFailed)
	return false
}

// wait blocks for delay, and reports whether the session outlasted it.
func (wsClient *WebsocketClient) wait(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-wsClient.done:
		return false
	case <-timer.C:
		return true
	}
}

// notifySignals returns the signal channel and the stop for the caller to defer.
// Defer the stop before the raw-mode restore so LIFO runs it after: a signal
// arriving mid-teardown lands in the buffered channel instead of killing the
// process with the terminal still in raw mode.
func notifySignals() (chan os.Signal, func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	return sigChan, func() { signal.Stop(sigChan) }
}

// enterRawMode returns the restore for the caller to defer.
func enterRawMode() (func(), error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("stdin is not a terminal")
	}
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to enter raw mode: %w", err)
	}

	return func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }, nil
}

func (c *connection) readFromServer() {
	for {
		_, message, err := c.ws.ReadMessage()
		if err != nil {
			c.finish(err)
			return
		}
		_, _ = os.Stdout.Write(message)
	}
}

// readUserInput cannot be released mid-read: a goroutine parked in ReadRune stays
// there until the next keystroke, and only closing stdin would change that. That
// is why one reader serves the whole session rather than one per connection—a
// reader left over from a dropped connection would race the next one for the
// user's keystrokes.
//
// Between connections nothing is draining inputChan, so this parks on the send
// until the session either reconnects or ends. Nothing is buffered on its behalf.
func (wsClient *WebsocketClient) readUserInput(inputChan chan<- string) {
	reader := bufio.NewReader(os.Stdin)
	for {
		char, _, err := reader.ReadRune()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil // the user closing stdin ends the session rather than failing it
			}
			wsClient.finish(err)
			return
		}
		// After teardown writeToServer is gone, so an unguarded send would park here.
		select {
		case inputChan <- string(char):
		case <-wsClient.done:
			return
		}
	}
}

func (c *connection) writeToServer(inputChan <-chan string) {
	// A ticker rather than time.After, which restarts on every arriving rune and
	// so defers the flush for as long as input keeps coming.
	ticker := time.NewTicker(writeFlushInterval)
	defer ticker.Stop()

	var inputBuffer []rune
	for {
		select {
		case <-c.ended:
			// Whatever is still buffered goes with the connection: nothing here
			// re-sends it, so input typed as the link dropped is lost. Replaying it
			// would submit lines to a shell the user could no longer see.
			return
		case input := <-inputChan:
			inputBuffer = append(inputBuffer, []rune(input)...)
		case <-ticker.C:
			if len(inputBuffer) > 0 {
				err := c.ws.WriteMessage(websocket.BinaryMessage, []byte(string(inputBuffer)))
				if err != nil {
					c.finish(err)
					return
				}
				inputBuffer = []rune{}
			}
		}
	}
}

func sharingInfo(response ShareResponse) {
	// Sanitize credentials display based on environment
	displayPassword := response.Password
	hideCredentials := os.Getenv("ALPACON_HIDE_CREDENTIALS") == "true"
	if hideCredentials {
		displayPassword = "********"
	}

	fmt.Fprintf(os.Stderr, "\nSession shared. The invitee must enter the password to access the terminal.\n\n")
	fmt.Fprintf(os.Stderr, "To join, run:\n")
	fmt.Fprintf(os.Stderr, "  alpacon websh join --url=\"%s\" --password=\"%s\"\n\n", response.SharedURL, displayPassword)
	fmt.Fprintf(os.Stderr, "Or open the URL in a browser.\n\n")
	fmt.Fprintf(os.Stderr, "Share URL:   %s\n", response.SharedURL)
	fmt.Fprintf(os.Stderr, "Password:    %s\n", displayPassword)
	fmt.Fprintf(os.Stderr, "Read Only:   %v\n", response.ReadOnly)
	fmt.Fprintf(os.Stderr, "Expiration:  %s\n", utils.TimeUtils(response.Expiration))

	if hideCredentials {
		fmt.Fprintf(os.Stderr, "\nNote: Credentials are hidden. Set ALPACON_HIDE_CREDENTIALS=false to display.\n")
	}
}
