package websh

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/websh"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type offerGateError struct{}

func (offerGateError) Error() string       { return "work_session_required" }
func (offerGateError) ErrorCode() string   { return utils.WorkSessionRequired }
func (offerGateError) ErrorSource() string { return "server" }

func TestWebshResendsWithCreatedSessionAndWebshScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ALPACON_WORK_SESSION", "")
	require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "", "", "", 0, false))
	input, err := os.CreateTemp(t.TempDir(), "stdin")
	require.NoError(t, err)
	t.Cleanup(func() { _ = input.Close() })
	_, err = input.WriteString("y\nrepair terminal\n1h\n")
	require.NoError(t, err)
	_, err = input.Seek(0, 0)
	require.NoError(t, err)
	oldStdin := os.Stdin
	os.Stdin = input
	t.Cleanup(func() { os.Stdin = oldStdin })
	var created []wsapi.WorkSessionCreateRequest
	session := map[string]any{"id": "new-session", "status": "active", "requester_type": "user", "assigned_user": map[string]string{"id": "user-1"}, "servers": []map[string]string{{"id": "server-1"}}, "scopes": []string{"websh"}, "expires_at": time.Now().Add(time.Hour)}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case "GET /api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
		case "GET /api/work-sessions/sessions/":
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		case "POST /api/work-sessions/sessions/":
			var req wsapi.WorkSessionCreateRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			created = append(created, req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(session)
		case "GET /api/work-sessions/sessions/new-session/":
			_ = json.NewEncoder(w).Encode(session)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{BaseURL: ts.URL, HTTPClient: ts.Client(), WorkspaceName: "ws"}
	oldSelect, oldCreate := selectWorkSession, createWebshSession
	selectWorkSession = func(ios *utils.IOStreams, ac *client.AlpaconClient, id string, need worksession.Need) (*worksession.Selection, error) {
		return worksession.OfferedForTest(ios, ac, "old-session", need), nil
	}
	var sent []string
	sentinel := errors.New("terminal unavailable after resend")
	createWebshSession = func(ac *client.AlpaconClient, server, username, group string, share, readOnly bool, id string) (websh.SessionResponse, error) {
		sent = append(sent, id)
		if len(sent) == 1 {
			return websh.SessionResponse{}, utils.MarkSubmission(offerGateError{})
		}
		return websh.SessionResponse{}, sentinel
	}
	t.Cleanup(func() { selectWorkSession, createWebshSession = oldSelect, oldCreate })
	ios, _, _ := utils.TestIOStreams()
	cmd := NewWebshCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
	err = cmd.RunE(cmd, []string{"web-1"})
	require.ErrorContains(t, err, "terminal unavailable after resend")
	assert.Equal(t, []string{"old-session", "new-session"}, sent)
	require.Len(t, created, 1)
	assert.Equal(t, []string{"websh"}, created[0].Scopes)
	assert.Equal(t, []string{"server-1"}, created[0].Servers)
}
