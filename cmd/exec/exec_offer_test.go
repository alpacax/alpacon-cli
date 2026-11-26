package exec

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errExecOfferRetryObserved = errors.New("retry observed")

type execOfferStream struct{ ids []string }

func (s *execOfferStream) RunCommand(_, _, _, _ string, _ map[string]string, id, _ string, _ io.Writer) error {
	s.ids = append(s.ids, id)
	if len(s.ids) == 1 {
		return utils.MarkSubmission(execUserCodedErr{code: utils.WorkSessionRequired})
	}
	return errExecOfferRetryObserved
}

func (s *execOfferStream) RunFileCommand(string, event.FileExecution, string, string, string, string, io.Writer) error {
	return errors.New("unexpected file command")
}

func (*execOfferStream) Close() {}

func execOfferServer(t *testing.T, createdScopes *[]string) *httptest.Server {
	t.Helper()
	return execOfferServerWithStatus(t, createdScopes, "active")
}

func execOfferServerWithStatus(t *testing.T, createdScopes *[]string, status string) *httptest.Server {
	t.Helper()
	active := func(scopes []string) map[string]any {
		return map[string]any{
			"id": "new-session", "status": status, "requester_type": "user", "scopes": scopes,
			"servers": []map[string]string{{"id": "server-1", "name": "prod"}}, "clusters": []any{},
			"assigned_user": map[string]string{"id": "user-1"}, "expires_at": time.Now().Add(time.Hour).UTC(),
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"prod"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			var body struct {
				Scopes []string `json:"scopes"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				return
			}
			*createdScopes = slices.Clone(body.Scopes)
			assert.NoError(t, json.NewEncoder(w).Encode(active(body.Scopes)))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/new-session/":
			assert.NoError(t, json.NewEncoder(w).Encode(active(*createdScopes)))
		default:
			http.NotFound(w, r)
		}
	}))
}

func enableExecOffer(t *testing.T) {
	t.Helper()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	_, err = write.WriteString("y\npurpose\n1h\n")
	require.NoError(t, err)
	require.NoError(t, write.Close())
	original := os.Stdin
	os.Stdin = read
	t.Cleanup(func() { os.Stdin = original; _ = read.Close() })
}

func installExecOfferSelection(t *testing.T, want worksession.Operation) {
	t.Helper()
	original := selectWorkSession
	t.Cleanup(func() { selectWorkSession = original })
	selectWorkSession = func(ios *utils.IOStreams, ac *client.AlpaconClient, _ string, need worksession.Need) (*worksession.Selection, error) {
		assert.Equal(t, want, need.Operation)
		assert.Equal(t, "prod", need.Server)
		return worksession.OfferedForTest(ios, ac, "old-session", need), nil
	}
}

func TestExecWaitOfferResendsWithCreatedCommandSession(t *testing.T) {
	var scopes []string
	server := execOfferServer(t, &scopes)
	defer server.Close()
	enableExecOffer(t)
	installExecOfferSelection(t, worksession.Command)
	stream := &execOfferStream{}
	original := newCommandStream
	newCommandStream = func(*client.AlpaconClient) commandStream { return stream }
	t.Cleanup(func() { newCommandStream = original })

	_, _, exitCode := runInProcess(t, server.URL, "", "", []string{"prod", "id"})

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Equal(t, []string{"old-session", "new-session"}, stream.ids)
	assert.Equal(t, []string{utils.OperationCommand}, scopes)
}

func TestExecDetachOfferResendsWithCreatedCommandSession(t *testing.T) {
	var scopes []string
	server := execOfferServer(t, &scopes)
	defer server.Close()
	enableExecOffer(t)
	installExecOfferSelection(t, worksession.Command)
	original := submitCommand
	t.Cleanup(func() { submitCommand = original })
	var ids []string
	submitCommand = func(_ *client.AlpaconClient, _, _ string, _, _ string, _ map[string]string, id, _ string) (event.CommandResponse, error) {
		ids = append(ids, id)
		if len(ids) == 1 {
			return event.CommandResponse{}, utils.MarkSubmission(execUserCodedErr{code: utils.WorkSessionRequired})
		}
		return event.CommandResponse{}, errExecOfferRetryObserved
	}

	_, _, exitCode := runInProcess(t, server.URL, "", "", []string{"--detach", "prod", "id"})

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Equal(t, []string{"old-session", "new-session"}, ids)
	assert.Equal(t, []string{utils.OperationCommand}, scopes)
}

func TestExecUploadOfferResendsWithCreatedFileCommandSession(t *testing.T) {
	var scopes []string
	server := execOfferServer(t, &scopes)
	defer server.Close()
	enableExecOffer(t)
	installExecOfferSelection(t, worksession.FileCommand)
	original := uploadContentAs
	t.Cleanup(func() { uploadContentAs = original })
	var ids []string
	uploadContentAs = func(_ *client.AlpaconClient, _, _ string, _ []byte, _, _, id string, _ bool) error {
		ids = append(ids, id)
		if len(ids) == 1 {
			return utils.MarkSubmission(execUserCodedErr{code: utils.WorkSessionRequired})
		}
		return errExecOfferRetryObserved
	}
	local := t.TempDir() + "/deploy.sh"
	require.NoError(t, os.WriteFile(local, []byte("echo ok\n"), 0600))

	_, _, exitCode := runInProcess(t, server.URL, "", "", []string{"--detach", "--upload", "--file", "/tmp/deploy.sh", "--file-from", local, "prod"})

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Equal(t, []string{"old-session", "new-session"}, ids)
	assert.ElementsMatch(t, []string{utils.OperationCommand, utils.OperationWebFTP}, scopes)
}

func TestExecOfferAfterUploadSaysNotToUploadAgain(t *testing.T) {
	var scopes []string
	server := execOfferServerWithStatus(t, &scopes, "rejected")
	defer server.Close()
	enableExecOffer(t)
	installExecOfferSelection(t, worksession.FileCommand)
	originalUpload, originalSubmit := uploadContentAs, submitFileCommand
	t.Cleanup(func() { uploadContentAs, submitFileCommand = originalUpload, originalSubmit })
	uploadContentAs = func(*client.AlpaconClient, string, string, []byte, string, string, string, bool) error { return nil }
	submitFileCommand = func(*client.AlpaconClient, string, event.FileExecution, string, string, string, string) (event.CommandResponse, error) {
		return event.CommandResponse{}, utils.MarkSubmission(execUserCodedErr{code: utils.WorkSessionRequired})
	}
	local := t.TempDir() + "/deploy.sh"
	require.NoError(t, os.WriteFile(local, []byte("echo ok\n"), 0600))

	_, stderr, exitCode := runInProcess(t, server.URL, "", "", []string{"--detach", "--upload", "--file", "/tmp/deploy.sh", "--file-from", local, "-u", "root", "prod"})

	assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
	assert.Contains(t, stderr, "then re-run this command without --upload and --overwrite, since the script is already on the server (run exec with -u root, the account the session was created for).\n")
}

func TestExecFromServerOfferResendsWithCreatedFileCommandSession(t *testing.T) {
	var scopes []string
	server := execOfferServer(t, &scopes)
	defer server.Close()
	enableExecOffer(t)
	installExecOfferSelection(t, worksession.FileCommand)
	original := downloadFileContent
	t.Cleanup(func() { downloadFileContent = original })
	var ids []string
	downloadFileContent = func(_ *client.AlpaconClient, _, _ string, _, _, id string, _ int64) ([]byte, error) {
		ids = append(ids, id)
		if len(ids) == 1 {
			return nil, utils.MarkSubmission(execUserCodedErr{code: utils.WorkSessionRequired})
		}
		return nil, errExecOfferRetryObserved
	}

	_, _, exitCode := runInProcess(t, server.URL, "", "", []string{"--detach", "--from-server", "--file", "/tmp/deploy.sh", "prod"})

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Equal(t, []string{"old-session", "new-session"}, ids)
	assert.ElementsMatch(t, []string{utils.OperationCommand, utils.OperationWebFTP}, scopes)
}

func TestRemoteExecNamesTheCommandTheUserRanInTheWorkSessionNeed(t *testing.T) {
	original := selectWorkSession
	t.Cleanup(func() { selectWorkSession = original })
	errStop := errors.New("stop after the selection")
	tests := []struct {
		name      string
		invokedAs Invocation
		want      string
	}{
		{name: "exec", want: ""},
		{name: "websh_command_mode", invokedAs: WebshInvocation, want: "websh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got worksession.Need
			selectWorkSession = func(_ *utils.IOStreams, _ *client.AlpaconClient, _ string, need worksession.Need) (*worksession.Selection, error) {
				got = need
				return nil, errStop
			}
			ios, _, _ := utils.TestIOStreams()

			err := RemoteExec(ios, testClientFactory("https://ws.example.com"), io.Discard, io.Discard, RemoteExecArgs{Server: "prod", Command: "id", InvokedAs: tt.invokedAs})

			require.Error(t, err)
			assert.Equal(t, tt.want, got.Command)
		})
	}
}
