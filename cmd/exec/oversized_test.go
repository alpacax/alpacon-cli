package exec

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lineMaxLengthBody    = `{"code":"max_length","field_errors":{"line":[{"code":"max_length","message":"Ensure this field has no more than 2048 characters."}]}}`
	otherFieldMaxLength  = `{"code":"max_length","field_errors":{"purpose":[{"code":"max_length","message":"Ensure this field has no more than 2000 characters."}]}}`
	topLevelOnlyBody     = `{"code":"max_length"}`
	lineOtherCodeBody    = `{"code":"invalid","field_errors":{"line":[{"code":"blank","message":"This field may not be blank."}]}}`
	oversizedNoticeStart = "Command line exceeds the server's limit; running it as a verified file at /tmp/.alpacon-exec-"
)

// refusalsFrom drives the real client against a server that answers one request with status and
// body, and returns the error the detach lane (SubmitCommand) and the streaming lane
// (RunExecWithApprovalWait) each produce.
func refusalsFrom(t *testing.T, status int, body string) (detach, stream error) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/sessions/":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"disabled"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, detach = event.SubmitCommand(ac, "prod", "echo hi", "", "", nil, "", "")
	stream = RunExecWithApprovalWait(ac, "prod", "echo hi", "", "", nil, "", "", 0, io.Discard)
	return detach, stream
}

func TestIsLineTooLong(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"400 with line max_length", http.StatusBadRequest, lineMaxLengthBody, true},
		{"400 with max_length on another field only", http.StatusBadRequest, otherFieldMaxLength, false},
		{"400 with top-level max_length and no field errors", http.StatusBadRequest, topLevelOnlyBody, false},
		{"400 with a different code on line", http.StatusBadRequest, lineOtherCodeBody, false},
		{"403 with the line max_length body", http.StatusForbidden, lineMaxLengthBody, false},
		{"500 with the line max_length body", http.StatusInternalServerError, lineMaxLengthBody, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			detach, stream := refusalsFrom(t, tc.status, tc.body)
			require.Error(t, detach)
			require.Error(t, stream)
			assert.Equal(t, tc.want, isLineTooLong(detach), "detach lane")
			assert.Equal(t, tc.want, isLineTooLong(stream), "streaming lane")
		})
	}

	t.Run("nil error", func(t *testing.T) {
		t.Parallel()
		assert.False(t, isLineTooLong(nil))
	})
}

func TestCommandFilePath(t *testing.T) {
	t.Parallel()
	format := regexp.MustCompile(`^/tmp/\.alpacon-exec-[0-9a-f]{16}\.sh$`)

	t.Run("same content gives the same path", func(t *testing.T) {
		t.Parallel()
		first, second := commandFilePath("echo hi"), commandFilePath("echo hi")
		assert.Equal(t, first, second)
		assert.Regexp(t, format, first)
	})
	t.Run("different content gives a different path", func(t *testing.T) {
		t.Parallel()
		assert.NotEqual(t, commandFilePath("echo hi"), commandFilePath("echo hi "))
	})
	t.Run("pins the first 16 hex characters of the sha256", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "/tmp/.alpacon-exec-"+pinnedEchoHiDigest+".sh", commandFilePath("echo hi"))
	})
}

// pinnedEchoHiDigest is the first 16 hex characters of sha256("echo hi") (printf 'echo hi' | sha256sum).
const pinnedEchoHiDigest = "56a79f3b11544807"

func TestFileLaneForCommand(t *testing.T) {
	t.Parallel()
	long := "echo " + strings.Repeat("a", 3000)

	t.Run("carries the run-as account, session, purpose, detach and reuse days", func(t *testing.T) {
		t.Parallel()
		parsed := RemoteExecArgs{
			Username: "deploy", Groupname: "ops", WorkSessionID: "ses-1", Purpose: "rollout",
			Detach: true, Wait: false, WaitApproval: 0, Server: "prod", Command: long, ReuseDays: 30,
			InvokedAs: ExecInvocation, OutputFormat: "json",
		}
		got, refusal := fileLaneForCommand(parsed)
		assert.Nil(t, refusal)
		assert.Equal(t, RemoteExecArgs{
			Username: "deploy", Groupname: "ops", WorkSessionID: "ses-1", Purpose: "rollout",
			Detach: true, Server: "prod", InvokedAs: ExecInvocation, OutputFormat: "json",
			File: &FileExecArgs{Path: commandFilePath(long), Upload: true, Overwrite: true, ReuseDays: 30, FromCommand: true},
		}, got)
	})

	t.Run("accepts exactly 64 KB", func(t *testing.T) {
		t.Parallel()
		command := strings.Repeat("a", FileContentMaxBytes)
		got, refusal := fileLaneForCommand(RemoteExecArgs{Server: "prod", Command: command})
		assert.Nil(t, refusal)
		require.NotNil(t, got.File)
		assert.Equal(t, commandFilePath(command), got.File.Path)
		assert.True(t, got.File.FromCommand)
	})

	refused := []struct {
		name    string
		parsed  RemoteExecArgs
		message string
	}{
		{"env", RemoteExecArgs{Server: "prod", Command: long, Env: map[string]string{"K": "v"}}, "--env values cannot ride the file lane"},
		{"over 64 KB", RemoteExecArgs{Server: "prod", Command: strings.Repeat("a", FileContentMaxBytes+1)}, "exceeds 65536 bytes (64 KB)"},
		{"invalid UTF-8", RemoteExecArgs{Server: "prod", Command: "echo \xff\xfe"}, "not valid UTF-8"},
	}
	for _, tc := range refused {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			t.Parallel()
			tc.parsed.Username = "root"
			got, refusal := fileLaneForCommand(tc.parsed)
			assert.Nil(t, got.File)
			require.NotNil(t, refusal)
			assert.Contains(t, refusal.message, tc.message)
			assert.Equal(t, "alpacon exec -u root --upload --file /path/to/script.sh --file-from ./script.sh prod", refusal.action.Command)
		})
	}

	t.Run("refusal repeats purpose, detach and reuse days", func(t *testing.T) {
		t.Parallel()
		_, refusal := fileLaneForCommand(RemoteExecArgs{
			Server: "prod", Command: long, Env: map[string]string{"K": "v"},
			Purpose: "roll out; it's urgent", Detach: true, ReuseDays: 30,
		})
		require.NotNil(t, refusal)
		assert.Equal(t, "alpacon exec --purpose 'roll out; it'\\''s urgent' --detach --reuse-days 30 --upload --file /path/to/script.sh --file-from ./script.sh prod", refusal.action.Command)
	})

	t.Run("refusal keeps the server one argument and falls back when it is blank", func(t *testing.T) {
		t.Parallel()
		_, spaced := fileLaneForCommand(RemoteExecArgs{Server: "my server\x1b[2J", Command: long, Env: map[string]string{"K": "v"}})
		require.NotNil(t, spaced)
		assert.NotContains(t, spaced.action.Command, "\x1b")
		assert.Contains(t, spaced.action.Command, "'my server")

		_, blank := fileLaneForCommand(RemoteExecArgs{Command: long, Env: map[string]string{"K": "v"}})
		require.NotNil(t, blank)
		assert.Equal(t, "alpacon exec --upload --file /path/to/script.sh --file-from ./script.sh SERVER", blank.action.Command)
	})
}

// oversizedServer refuses an inline exec submission for length, then accepts the file-lane
// submission after the upload.
type oversizedServer struct {
	fileUploadServer
	inlineBodies   [][]byte
	fileBody       []byte
	inlineStatus   int    // 0 accepts the inline line
	inlineResponse string // body for a refused inline line
	fileStatus     int    // 0 accepts the file submission
	fileResponse   string
	result         string
	success        bool
	exitCode       int
}

func (s *oversizedServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.serveUpload(w, r, `{"success":true}`) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/sessions/":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"disabled"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			s.handleSubmit(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "cmd-1", "status": "completed", "success": s.success, "exit_code": s.exitCode, "result": s.result,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/chunks/":
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *oversizedServer) handleSubmit(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]json.RawMessage
	_ = json.Unmarshal(body, &req)
	_, isFile := req["file"]
	status, response := s.inlineStatus, s.inlineResponse
	step := "inline-submit"
	if isFile {
		status, response = s.fileStatus, s.fileResponse
		step = "file-submit"
	}
	s.mu.Lock()
	if isFile {
		s.fileBody = body
	} else {
		s.inlineBodies = append(s.inlineBodies, body)
	}
	s.mu.Unlock()
	s.record(step)
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
		return
	}
	_, _ = w.Write([]byte(`[{"id":"cmd-1"}]`))
}

func (s *oversizedServer) snapshotSteps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

func newOversizedServer(t *testing.T, s *oversizedServer) *httptest.Server {
	t.Helper()
	if s.inlineStatus == 0 && s.inlineResponse == "" {
		s.inlineStatus, s.inlineResponse = http.StatusBadRequest, lineMaxLengthBody
	}
	if s.result == "" {
		s.result, s.success = "remote output\n", true
	}
	ts := httptest.NewServer(s.handler())
	t.Cleanup(ts.Close)
	return ts
}

func oversizedCommand() string { return "echo " + strings.Repeat("a", 3000) }

// stdoutOf drops the "PASS" line the helper test binary prints after the command.
func stdoutOf(stdout string) string { return strings.TrimSuffix(stdout, "PASS\n") }

func TestExecAcceptedInlineSendsNoUploadAndNoNotice(t *testing.T) {
	t.Parallel()
	s := &oversizedServer{inlineStatus: 0, inlineResponse: "ok"}
	ts := newOversizedServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "echo", "hi")

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Equal(t, "remote output\n", stdoutOf(stdout))
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
	assert.NotContains(t, stderr, "exceeds the server's limit")
}

func TestExecOversizedCommandRunsAsAVerifiedFile(t *testing.T) {
	t.Parallel()
	command := oversizedCommand()
	path := commandFilePath(command)
	s := &oversizedServer{result: "remote output\n", success: true}
	ts := newOversizedServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "-u", "deploy", "-g", "ops", "--purpose", "rollout", "--work-session", "ses-1", "--reuse-days", "9", "prod", "--", command)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Equal(t, "remote output\n", stdoutOf(stdout))
	assert.Equal(t, []string{"inline-submit", "upload-create", "upload-put", "upload-trigger", "file-submit"}, s.snapshotSteps())
	assert.Equal(t, 1, strings.Count(stderr, oversizedNoticeStart))
	assert.Contains(t, stderr, path+" with /bin/bash (the file stays on the server)")
	assert.NotContains(t, stdout, "exceeds the server's limit")

	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.inlineBodies, 1)
	var inline struct {
		Line string `json:"line"`
	}
	require.NoError(t, json.Unmarshal(s.inlineBodies[0], &inline))
	assert.Equal(t, command, inline.Line)
	assert.Equal(t, command, string(s.putBody))
	assert.Equal(t, strings.TrimPrefix(path, "/tmp/"), s.uploadRequest["name"])
	assert.Equal(t, "/tmp/", s.uploadRequest["path"])
	assert.Equal(t, "deploy", s.uploadRequest["username"])
	assert.Equal(t, "ops", s.uploadRequest["groupname"])
	assert.Equal(t, true, s.uploadRequest["allow_overwrite"])

	var payload struct {
		File struct {
			Path        string   `json:"path"`
			Interpreter string   `json:"interpreter"`
			Args        []string `json:"args"`
			Content     string   `json:"content"`
			ReuseDays   int      `json:"reuse_days"`
		} `json:"file"`
		Username    string `json:"username"`
		Groupname   string `json:"groupname"`
		Purpose     string `json:"purpose"`
		WorkSession string `json:"work_session"`
	}
	require.NoError(t, json.Unmarshal(s.fileBody, &payload))
	assert.Equal(t, path, payload.File.Path)
	assert.Equal(t, "/bin/bash", payload.File.Interpreter)
	assert.Equal(t, command, payload.File.Content)
	assert.Equal(t, 9, payload.File.ReuseDays)
	assert.Equal(t, "deploy", payload.Username)
	assert.Equal(t, "ops", payload.Groupname)
	assert.Equal(t, "rollout", payload.Purpose)
	assert.Equal(t, "ses-1", payload.WorkSession)
}

func TestExecOversizedCommandKeepsTheRemoteExitCode(t *testing.T) {
	t.Parallel()
	s := &oversizedServer{result: "partial\n", success: false, exitCode: 3}
	ts := newOversizedServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", oversizedCommand())

	assert.Equal(t, 3, exitCode, "stderr: %s", stderr)
	assert.Equal(t, "partial\n", stdoutOf(stdout))
	assert.Contains(t, stderr, oversizedNoticeStart)
}

func TestExecOversizedCommandDetachUploadsThenPrintsTheJobID(t *testing.T) {
	t.Parallel()
	s := &oversizedServer{}
	ts := newOversizedServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--detach", "prod", "--", oversizedCommand())

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Equal(t, "Job submitted: cmd-1\n", stdoutOf(stdout))
	assert.Equal(t, []string{"inline-submit", "upload-create", "upload-put", "upload-trigger", "file-submit"}, s.snapshotSteps())
	assert.Contains(t, stderr, oversizedNoticeStart)
}

func TestExecOversizedCommandStillExits3OnAGateDenial(t *testing.T) {
	t.Parallel()
	s := &oversizedServer{
		fileStatus:   http.StatusForbidden,
		fileResponse: `{"code":"work_session_required","source":"command","detail":"WorkSession required"}`,
	}
	ts := newOversizedServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", oversizedCommand())

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitCode, "stdout: %s", stdout)
	assert.Contains(t, stderr, oversizedNoticeStart)
	assert.Equal(t, []string{"inline-submit", "upload-create", "upload-put", "upload-trigger", "file-submit"}, s.snapshotSteps())
}

func TestExecOversizedCommandWithEnvIsRefusedBeforeAnyUpload(t *testing.T) {
	t.Parallel()
	s := &oversizedServer{}
	ts := newOversizedServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--env=KEY=s3cr3t-value", "-u", "root", "prod", "--", oversizedCommand())

	assert.Equal(t, 1, exitCode, "stdout: %s", stdout)
	assert.NotContains(t, stdout+stderr, "s3cr3t-value")
	assert.Contains(t, stderr, "--env values cannot ride the file lane")
	assert.Contains(t, stderr, "alpacon exec -u root --upload --file /path/to/script.sh --file-from ./script.sh prod")
	assert.NotContains(t, stderr, "running it as a verified file")
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
}

func TestExecOversizedCommandWithEnvNamesTheAlternativeInJSON(t *testing.T) {
	t.Parallel()
	s := &oversizedServer{}
	ts := newOversizedServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "--env=KEY=s3cr3t-value", "prod", "--", oversizedCommand())

	assert.Equal(t, 1, exitCode)
	assert.NotContains(t, stdout+stderr, "s3cr3t-value")
	var got struct {
		ErrorCode   string             `json:"error_code"`
		HTTPStatus  int                `json:"http_status"`
		NextActions []utils.NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(stderr), &got), "stderr: %s", stderr)
	assert.Equal(t, "max_length", got.ErrorCode)
	assert.Equal(t, http.StatusBadRequest, got.HTTPStatus)
	require.Len(t, got.NextActions, 1)
	assert.Equal(t, "alpacon exec --upload --file /path/to/script.sh --file-from ./script.sh prod", got.NextActions[0].Command)
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
}

func TestExecOversizedCommandOver64KBIsRefusedBeforeAnyUpload(t *testing.T) {
	t.Parallel()
	s := &oversizedServer{}
	ts := newOversizedServer(t, s)

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "echo "+strings.Repeat("a", FileContentMaxBytes))

	assert.Equal(t, 1, exitCode)
	assert.Contains(t, stderr, "exceeds 65536 bytes (64 KB)")
	assert.Contains(t, stderr, "--upload --file /path/to/script.sh")
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
}

func TestExecMaxLengthOnAnotherFieldIsReportedWithoutASwitch(t *testing.T) {
	t.Parallel()
	s := &oversizedServer{inlineStatus: http.StatusBadRequest, inlineResponse: otherFieldMaxLength}
	ts := newOversizedServer(t, s)

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "echo", "hi")

	assert.Equal(t, 1, exitCode)
	assert.Contains(t, stderr, "Ensure this field has no more than 2000 characters.")
	assert.NotContains(t, stderr, "exceeds the server's limit")
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
}

func TestExecOversizedCommandPendingApprovalHintReadsTheServerCopy(t *testing.T) {
	t.Parallel()
	command := oversizedCommand()
	path := commandFilePath(command)
	s := &oversizedServer{result: denialLine("SUDO_APPROVAL_REQUIRED"), success: false, exitCode: 1}
	ts := newOversizedServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", command)

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "stderr: %s", stderr)
	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec --file "+path+" --from-server prod", got.NextActions[0].Command)
	assert.NotContains(t, stdout+stderr, command)
}
