package exec

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRemoteExecArgs_FileFromServer(t *testing.T) {
	t.Parallel()
	result := ParseRemoteExecArgs([]string{"--file", "/opt/deploy.sh", "--from-server", "root@prod-web", "--", "--fast"})
	require.Empty(t, result.Err)
	assert.Equal(t, "root", result.Username)
	assert.Equal(t, "prod-web", result.Server)
	assert.Equal(t, &FileExecArgs{Path: "/opt/deploy.sh", Args: []string{"--fast"}, FromServer: true}, result.File)
}

func TestParseRemoteExecArgs_FileFromServerErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		expectedErr string
	}{
		{
			name:        "from-server without file",
			args:        []string{"--from-server", "prod-web", "uptime"},
			expectedErr: "--from-server requires --file; it reads the script from the --file path on the server",
		},
		{
			name:        "from-server with upload",
			args:        []string{"--file", "/opt/deploy.sh", "--from-server", "--upload", "prod-web"},
			expectedErr: "--from-server cannot be combined with --upload; --from-server submits the file already on the server, --upload writes a local copy there",
		},
		{
			name:        "from-server with file-from",
			args:        []string{"--file", "/opt/deploy.sh", "--file-from", "./deploy.sh", "--from-server", "prod-web"},
			expectedErr: "--from-server cannot be combined with --file-from; the content is read from the --file path on the server, not from a local file",
		},
		{
			name:        "from-server with a value",
			args:        []string{"--file", "/opt/deploy.sh", "--from-server=true", "prod-web"},
			expectedErr: "--from-server does not accept a value; use --from-server alone",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ParseRemoteExecArgs(tt.args)
			assert.Equal(t, tt.expectedErr, result.Err)
			assert.Nil(t, result.File)
		})
	}
}

// The re-run keeps --from-server: the requester may have no local copy, and
// without the flag the re-run would read the --file path locally.
func TestReRunHint_FileFromServerIsRepeated(t *testing.T) {
	t.Parallel()
	hint := reRunHint(RemoteExecArgs{
		Username: "root",
		Server:   "prod-web",
		File:     &FileExecArgs{Path: "/opt/deploy.sh", Args: []string{"--fast"}, FromServer: true},
	})
	assert.Equal(t, "alpacon exec -u root --file /opt/deploy.sh --from-server prod-web -- --fast", hint.Command)
}

func TestFileSuggestionAction(t *testing.T) {
	t.Parallel()
	generic := RemoteExecArgs{Username: "root", Server: "prod-web", Command: "bash /opt/deploy.sh --fast"}
	tests := []struct {
		name       string
		parsed     RemoteExecArgs
		suggestion *event.FileExecutionSuggestion
		want       string
		wantDesc   string
	}{
		{
			name:       "no suggestion",
			parsed:     generic,
			suggestion: nil,
		},
		{
			name:       "default interpreter is left out",
			parsed:     generic,
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/deploy.sh", Interpreter: "/bin/bash", Args: []string{"--fast"}},
			want:       "alpacon exec -u root --file /opt/deploy.sh --from-server prod-web -- --fast",
		},
		{
			name: "other interpreter group and work session are kept",
			parsed: RemoteExecArgs{
				Groupname: "ops", WorkSessionID: "ws-1", Server: "prod-web", Command: "python3 /opt/x.py",
			},
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/x.py", Interpreter: "/usr/bin/python3"},
			want:       "alpacon exec -g ops --work-session ws-1 --file /opt/x.py --from-server --interpreter /usr/bin/python3 prod-web",
		},
		{
			name:       "arguments are quoted for the local shell",
			parsed:     generic,
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/deploy.sh", Interpreter: "/bin/bash", Args: []string{"a;b", "$(id)"}},
			want:       "alpacon exec -u root --file /opt/deploy.sh --from-server prod-web -- 'a;b' '$(id)'",
		},
		{
			name:       "bare interpreter name gets a placeholder",
			parsed:     generic,
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/deploy.sh", InterpreterName: "bash", Args: []string{"--fast"}},
			want:       "alpacon exec -u root --file /opt/deploy.sh --from-server --interpreter '<absolute-path-of-bash>' prod-web -- --fast",
			wantDesc:   "command -v bash",
		},
		{
			name:       "neither interpreter nor name",
			parsed:     generic,
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/deploy.sh"},
		},
		{
			name:       "interpreter name with a path separator",
			parsed:     generic,
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/deploy.sh", InterpreterName: "bin/bash"},
		},
		{
			name:       "interpreter name that needs quoting",
			parsed:     generic,
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/deploy.sh", InterpreterName: "bash;id"},
		},
		{
			name:       "relative path",
			parsed:     generic,
			suggestion: &event.FileExecutionSuggestion{Path: "deploy.sh"},
		},
		{
			name:       "relative interpreter",
			parsed:     generic,
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/deploy.sh", Interpreter: "bash"},
		},
		{
			name:       "already on the file lane",
			parsed:     RemoteExecArgs{Server: "prod-web", File: &FileExecArgs{Path: "/opt/deploy.sh"}},
			suggestion: &event.FileExecutionSuggestion{Path: "/opt/deploy.sh"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			action, ok := fileSuggestionAction(tt.parsed, tt.suggestion)
			if tt.want == "" {
				assert.False(t, ok, "no suggestion expected, got %q", action.Command)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tt.want, action.Command)
			assert.NotEmpty(t, action.Description)
			if tt.wantDesc != "" {
				assert.Contains(t, action.Description, tt.wantDesc)
			}
		})
	}
}

// fileDownloadServer answers a --from-server run: the server lookup, the
// download create, the download command poll, the object fetch, the transfer
// status, and the command submission. It records what each request carried.
type fileDownloadServer struct {
	mu              sync.Mutex
	steps           []string
	downloadRequest map[string]any
	commandBody     []byte
}

func (s *fileDownloadServer) record(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

// newFileDownloadServer serves content as the file on the server. A non-empty
// failure makes the download command fail with that result instead.
func newFileDownloadServer(s *fileDownloadServer, content []byte, failure string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/downloads/":
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			_ = json.Unmarshal(body, &s.downloadRequest)
			s.mu.Unlock()
			s.record("download-create")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"dl-1","command":"cmd-dl-1","download_url":"http://` + r.Host + `/s3/deploy.sh"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-dl-1/":
			if failure != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "cmd-dl-1", "status": "failed", "result": failure})
				return
			}
			_, _ = w.Write([]byte(`{"id":"cmd-dl-1","status":"completed","success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/s3/deploy.sh":
			s.record("download-fetch")
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(content)
		case r.Method == http.MethodGet && r.URL.Path == "/api/webftp/downloads/dl-1/status/":
			_, _ = w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.commandBody = body
			s.mu.Unlock()
			s.record("command-submit")
			_, _ = w.Write([]byte(`[{"id":"cmd-file-1","shell":"file"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestExecFileFromServerSubmitsTheDownloadedBytes drives --from-server end to
// end: the script is read from the --file path as the run-as account, with no
// local copy, and the bytes read are the bytes submitted for review.
func TestExecFileFromServerSubmitsTheDownloadedBytes(t *testing.T) {
	t.Parallel()
	content := "#!/bin/bash\nset -euo pipefail\necho deploy\n"
	var s fileDownloadServer
	ts := newFileDownloadServer(&s, []byte(content), "")
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL,
		"--detach", "--file", "/opt/app/deploy.sh", "--from-server", "-g", "ops", "root@prod", "--", "--fast")
	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Equal(t, "Job submitted: cmd-file-1\n", strings.TrimSuffix(stdout, "PASS\n"))

	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Equal(t, []string{"download-create", "download-fetch", "command-submit"}, s.steps)
	assert.Equal(t, "/opt/app/deploy.sh", s.downloadRequest["path"])
	assert.Equal(t, "root", s.downloadRequest["username"])
	assert.Equal(t, "ops", s.downloadRequest["groupname"])

	var payload struct {
		File struct {
			Path    string   `json:"path"`
			Content string   `json:"content"`
			Args    []string `json:"args"`
		} `json:"file"`
	}
	require.NoError(t, json.Unmarshal(s.commandBody, &payload), "body: %s", s.commandBody)
	assert.Equal(t, "/opt/app/deploy.sh", payload.File.Path)
	assert.Equal(t, content, payload.File.Content)
	assert.Equal(t, []string{"--fast"}, payload.File.Args)
}

// TestExecFileFromServerRefusalsNeverSubmit pins that a file that cannot be
// read, is empty, or exceeds the content ceiling stops the run before the
// submission.
func TestExecFileFromServerRefusalsNeverSubmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content []byte
		failure string
		want    string
	}{
		{
			name:    "unreadable",
			failure: "/opt/deploy.sh: No such file or directory",
			want:    "cannot read the script from 'prod:/opt/deploy.sh' on the server: /opt/deploy.sh: No such file or directory",
		},
		{
			name:    "empty",
			content: []byte{},
			want:    "'prod:/opt/deploy.sh' is empty; a verified file needs content to hash",
		},
		{
			name:    "over the ceiling",
			content: []byte(strings.Repeat("#", FileContentMaxBytes+1)),
			want:    "'prod:/opt/deploy.sh' exceeds 65536 bytes (64 KB), the ceiling for a verified file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s fileDownloadServer
			ts := newFileDownloadServer(&s, tt.content, tt.failure)
			defer ts.Close()

			stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--file", "/opt/deploy.sh", "--from-server", "prod")
			assert.Equal(t, 1, exitCode)
			assert.Empty(t, strings.TrimSuffix(stdout, "PASS\n"))
			assert.Contains(t, stderr, tt.want)

			s.mu.Lock()
			defer s.mu.Unlock()
			assert.NotContains(t, s.steps, "command-submit")
		})
	}
}

// newHeldScriptServer parks a command line at awaiting_approval; suggestion,
// when non-empty, is the raw file_execution_suggestion on the command detail.
func newHeldScriptServer(suggestion string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = w.Write([]byte(`[{"id":"cmd-1"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			detail := `{"id":"cmd-1","status":"awaiting_approval"`
			if suggestion != "" {
				detail += `,"file_execution_suggestion":` + suggestion
			}
			_, _ = w.Write([]byte(detail + "}"))
		default:
			http.NotFound(w, r)
		}
	}))
}

const heldScriptSuggestionLine = "alpacon exec -u root --file /opt/deploy.sh --from-server prod -- --fast"

// TestExecHeldScriptPrintsTheFromServerSuggestion pins that a held command line
// the server reads as a script gets the --file --from-server form of it under
// the pending message, in both output formats.
func TestExecHeldScriptPrintsTheFromServerSuggestion(t *testing.T) {
	t.Parallel()
	ts := newHeldScriptServer(`{"path":"/opt/deploy.sh","interpreter":"/bin/bash","args":["--fast"]}`)
	// The subtests run in parallel, after this function returns.
	t.Cleanup(ts.Close)

	t.Run("table", func(t *testing.T) {
		t.Parallel()
		_, stderr, exitCode := runExecHelper(t, ts.URL, "root@prod", "--", "bash", "/opt/deploy.sh", "--fast")
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		assert.Contains(t, stderr, "alpacon exec logs cmd-1")
		assert.Contains(t, stderr, "  "+heldScriptSuggestionLine+"  # ")
	})
	t.Run("json", func(t *testing.T) {
		t.Parallel()
		stdout, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "root@prod", "--", "bash", "/opt/deploy.sh", "--fast")
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		var got pendingApprovalSignal
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(stdout, "PASS\n")), &got), "stdout: %s", stdout)
		require.NotEmpty(t, got.NextActions)
		assert.Equal(t, "alpacon exec logs cmd-1", got.NextActions[0].Command)
		assert.Equal(t, heldScriptSuggestionLine, got.NextActions[len(got.NextActions)-1].Command)
	})
}

// TestExecHeldScriptWithBareInterpreterPrintsAPlaceholder pins that a held
// command line whose interpreter was a bare name gets the --from-server line
// with an --interpreter placeholder and the 'command -v' hint to fill it.
func TestExecHeldScriptWithBareInterpreterPrintsAPlaceholder(t *testing.T) {
	t.Parallel()
	ts := newHeldScriptServer(`{"path":"/opt/deploy.sh","interpreter":null,"interpreter_name":"bash","args":["--fast"]}`)
	defer ts.Close()

	_, stderr, exitCode := runExecHelper(t, ts.URL, "root@prod", "--", "bash", "/opt/deploy.sh", "--fast")
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
	assert.Contains(t, stderr, "  alpacon exec -u root --file /opt/deploy.sh --from-server --interpreter '<absolute-path-of-bash>' prod -- --fast  # ")
	assert.Contains(t, stderr, "'command -v bash'")
}

// TestExecHeldCommandWithoutSuggestionKeepsTodaysHint pins that a held command
// the server made no suggestion for prints no --from-server line.
func TestExecHeldCommandWithoutSuggestionKeepsTodaysHint(t *testing.T) {
	t.Parallel()
	ts := newHeldScriptServer("")
	defer ts.Close()

	_, stderr, exitCode := runExecHelper(t, ts.URL, "root@prod", "--", "bash", "/opt/deploy.sh", "--fast")
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
	assert.Contains(t, stderr, "alpacon exec logs cmd-1")
	assert.NotContains(t, stderr, "--from-server")
}
