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

	var list []SessionListItem
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

func ConnectToSession(ac *client.AlpaconClient, sessionID string) (SessionResponse, error) {
	req := &ConnectRequest{
		Session:  sessionID,
		IsMaster: false,
		ReadOnly: true,
	}
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

	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return SessionResponse{}, err
	}

	sessionRequest := BuildSessionRequest(serverID, username, groupname, height, width, workSessionID)

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
	wsClient.conn = conn

	return nil
}

// finish keeps the first outcome. err is written before done closes, so anyone who
// saw done can read it. A deliberate close ends the session rather than failing it;
// every other close code stays an error.
func (wsClient *WebsocketClient) finish(err error) {
	if websocket.IsCloseError(err, sessionEndCloseCode, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		err = nil
	}
	wsClient.finishOnce.Do(func() {
		wsClient.err = err
		close(wsClient.done)
	})
}

// OpenReadOnlyTerminal opens a read-only terminal view for watching another user's session.
// Input is not forwarded to the server. Terminal echo is suppressed via raw mode.
// Ends cleanly on the remote close, on Ctrl+C, or on a signal.
func OpenReadOnlyTerminal(ac *client.AlpaconClient, sessionResponse SessionResponse) error {
	wsClient := newWebsocketClient(ac.SetWebsocketHeader())
	if err := wsClient.dial(sessionResponse.WebsocketURL); err != nil {
		return err
	}
	defer func() { _ = wsClient.conn.Close() }()

	sigChan, stopSignals := notifySignals()
	defer stopSignals()

	restore, err := enterRawMode()
	if err != nil {
		return err
	}
	defer restore()

	go wsClient.watchInterrupt(sigChan)
	go wsClient.readCtrlC()
	go wsClient.readFromServer()

	<-wsClient.done
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
// Ends cleanly on the remote close, on Ctrl+D, or on a signal.
func OpenNewTerminal(ac *client.AlpaconClient, sessionResponse SessionResponse) error {
	wsClient := newWebsocketClient(ac.SetWebsocketHeader())
	if err := wsClient.dial(sessionResponse.WebsocketURL); err != nil {
		return err
	}
	defer func() { _ = wsClient.conn.Close() }()

	return wsClient.runWsClient()
}

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
	go wsClient.readFromServer()
	go wsClient.readUserInput(inputChan)
	go wsClient.writeToServer(inputChan)

	<-wsClient.done
	return wsClient.err
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

func (wsClient *WebsocketClient) readFromServer() {
	for {
		_, message, err := wsClient.conn.ReadMessage()
		if err != nil {
			wsClient.finish(err)
			return
		}
		_, _ = os.Stdout.Write(message)
	}
}

// readUserInput cannot be released mid-read: a goroutine parked in ReadRune stays
// there until the next keystroke, and only closing stdin would change that.
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

func (wsClient *WebsocketClient) writeToServer(inputChan <-chan string) {
	// A ticker rather than time.After, which restarts on every arriving rune and
	// so defers the flush for as long as input keeps coming.
	ticker := time.NewTicker(writeFlushInterval)
	defer ticker.Stop()

	var inputBuffer []rune
	for {
		select {
		case <-wsClient.done:
			return
		case input := <-inputChan:
			inputBuffer = append(inputBuffer, []rune(input)...)
		case <-ticker.C:
			if len(inputBuffer) > 0 {
				err := wsClient.conn.WriteMessage(websocket.BinaryMessage, []byte(string(inputBuffer)))
				if err != nil {
					wsClient.finish(err)
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
