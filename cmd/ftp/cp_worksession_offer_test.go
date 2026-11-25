package ftp

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cpGateError struct{}

func (cpGateError) Error() string       { return "work session required" }
func (cpGateError) ErrorCode() string   { return utils.WorkSessionRequired }
func (cpGateError) ErrorSource() string { return "" }

func TestCpUploadResendsWithTheCreatedWorkSession(t *testing.T) {
	var sentIDs []string
	var create wsapi.WorkSessionCreateRequest
	ac, ios := setupCpOffer(t, &create)
	uploadCopyObject = func(_ *client.AlpaconClient, _ []string, _ string, _, _ string, _, _ bool, id string) (string, error) {
		sentIDs = append(sentIDs, id)
		if len(sentIDs) == 1 {
			return "web-1", utils.MarkSubmission(cpGateError{})
		}
		return "web-1", nil
	}

	cmd := NewCpCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
	cmd.SetArgs([]string{"local.txt", "web-1:/tmp/"})
	err := cmd.Execute()

	require.NoError(t, err)
	assert.Equal(t, []string{"old-session", "new-session"}, sentIDs)
	assert.Equal(t, []string{utils.OperationWebFTP}, create.Scopes)
	assert.Equal(t, []string{"server-1"}, create.Servers)
}

func TestCpDownloadResendsWithTheCreatedWorkSession(t *testing.T) {
	var sentIDs []string
	var create wsapi.WorkSessionCreateRequest
	ac, ios := setupCpOffer(t, &create)
	downloadCopyObject = func(_ *client.AlpaconClient, _ []string, _ string, _, _ string, _ bool, id string) (string, error) {
		sentIDs = append(sentIDs, id)
		if len(sentIDs) == 1 {
			return "web-1", utils.MarkSubmission(cpGateError{})
		}
		return "web-1", nil
	}

	cmd := NewCpCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
	cmd.SetArgs([]string{"web-1:/tmp/a.txt", t.TempDir()})
	err := cmd.Execute()

	require.NoError(t, err)
	assert.Equal(t, []string{"old-session", "new-session"}, sentIDs)
	assert.Equal(t, []string{utils.OperationWebFTP}, create.Scopes)
	assert.Equal(t, []string{"server-1"}, create.Servers)
}

func setupCpOffer(t *testing.T, create *wsapi.WorkSessionCreateRequest) (*client.AlpaconClient, *utils.IOStreams) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig("https://workspace.example", "workspace", "", "", "", "", "", 0, false))
	replaceCpStdin(t, "y\ncp files\n1h\n")
	ios, _, _ := utils.TestIOStreams()
	active := wsapi.WorkSession{
		ID:            "new-session",
		Status:        wsapi.StatusActive,
		RequesterType: "user",
		Scopes:        []string{utils.OperationWebFTP},
		Servers:       []types.ServerSummary{{ID: "server-1", Name: "web-1"}},
		AssignedUser:  &types.UserSummary{ID: "user-1"},
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	activeJSON, err := json.Marshal(active)
	require.NoError(t, err)
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/iam/users/-/":
				return http.StatusOK, `{"id":"user-1"}`
			case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`
			case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/":
				return http.StatusOK, `{"count":0,"results":[]}`
			case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
				body, readErr := io.ReadAll(r.Body)
				require.NoError(t, readErr)
				require.NoError(t, json.Unmarshal(body, create))
				return http.StatusCreated, string(activeJSON)
			case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/new-session/":
				return http.StatusOK, string(activeJSON)
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
				return http.StatusInternalServerError, `{}`
			}
		}),
	}
	originalSelect := selectWorkSession
	originalUpload := uploadCopyObject
	originalDownload := downloadCopyObject
	selectWorkSession = func(gotIOS *utils.IOStreams, gotAC *client.AlpaconClient, _ string, need worksession.Need) (*worksession.Selection, error) {
		assert.Equal(t, worksession.Transfer, need.Operation)
		return worksession.OfferedForTest(gotIOS, gotAC, "old-session", need), nil
	}
	t.Cleanup(func() {
		selectWorkSession = originalSelect
		uploadCopyObject = originalUpload
		downloadCopyObject = originalDownload
	})
	return ac, ios
}

func replaceCpStdin(t *testing.T, input string) *os.File {
	t.Helper()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	_, err = write.WriteString(input)
	require.NoError(t, err)
	require.NoError(t, write.Close())
	original := os.Stdin
	os.Stdin = read
	t.Cleanup(func() {
		os.Stdin = original
		_ = read.Close()
	})
	return read
}
