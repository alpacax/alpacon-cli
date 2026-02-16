package websh

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

const (
	sessionsBaseURL     = "/api/websh/sessions/"
	userChannelsBaseURL = "/api/websh/user-channels/"
)

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

// Create new websh session
func CreateWebshSession(ac *client.AlpaconClient, serverName, username, groupname string, share, readOnly bool) (SessionResponse, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return SessionResponse{}, err
	}

	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return SessionResponse{}, err
	}

	sessionRequest := &SessionRequest{
		Server:    serverID,
		Username:  username,
		Groupname: groupname,
		Rows:      height,
		Cols:      width,
	}

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

// Handles graceful termination of the websh terminal.
// Exits on error without further error handling.
func OpenNewTerminal(ac *client.AlpaconClient, sessionResponse SessionResponse) error {
	wsClient := &WebsocketClient{
		Header: ac.SetWebsocketHeader(),
		Done:   make(chan error, 1),
	}

	var err error
	wsClient.conn, _, err = websocket.DefaultDialer.Dial(sessionResponse.WebsocketURL, wsClient.Header)
	if err != nil {
		utils.CliErrorWithExit("websocket connection failed %v", err)
	}
	defer func() { _ = wsClient.conn.Close() }()

	err = wsClient.runWsClient()
	if err != nil {
		return err
	}

	return nil
}

func (wsClient *WebsocketClient) runWsClient() error {
	oldState, err := checkTerminal()
	if err != nil {
		utils.CliErrorWithExit("websocket connection failed %v", err)
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	inputChan := make(chan string, 1)

	go wsClient.readFromServer()
	go wsClient.readUserInput(inputChan)
	go wsClient.writeToServer(inputChan)

	return <-wsClient.Done
}

func checkTerminal() (*term.State, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("websh command should be a terminal")
	}
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}

	return oldState, nil
}

func (wsClient *WebsocketClient) readFromServer() {
	for {
		_, message, err := wsClient.conn.ReadMessage()
		if err != nil {
			wsClient.Done <- err
			return
		}
		fmt.Print(string(message))
	}
}

func (wsClient *WebsocketClient) readUserInput(inputChan chan<- string) {
	reader := bufio.NewReader(os.Stdin)
	for {
		char, _, err := reader.ReadRune()
		if err != nil {
			if err == io.EOF {
				wsClient.Done <- nil
				return
			}
			wsClient.Done <- err
			return
		}
		inputChan <- string(char)
	}
}

func (wsClient *WebsocketClient) writeToServer(inputChan <-chan string) {
	var inputBuffer []rune
	for {
		select {
		case input := <-inputChan:
			inputBuffer = append(inputBuffer, []rune(input)...)
		case <-time.After(time.Millisecond * 5):
			if len(inputBuffer) > 0 {
				err := wsClient.conn.WriteMessage(websocket.BinaryMessage, []byte(string(inputBuffer)))
				if err != nil {
					wsClient.Done <- err
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
