package exec

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lineMaxLengthBody   = `{"code":"max_length","field_errors":{"line":[{"code":"max_length","message":"Ensure this field has no more than 2048 characters."}]}}`
	otherFieldMaxLength = `{"code":"max_length","field_errors":{"purpose":[{"code":"max_length","message":"Ensure this field has no more than 2000 characters."}]}}`
	topLevelOnlyBody    = `{"code":"max_length"}`
	lineOtherCodeBody   = `{"code":"invalid","field_errors":{"line":[{"code":"blank","message":"This field may not be blank."}]}}`
)

// refusalsFrom returns the errors the detach lane (SubmitCommand) and the streaming lane
// (RunExecWithApprovalWait) produce against a server answering status and body.
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

const uploadCommandTail = "--upload --file /path/to/script.sh --file-from ./script.sh prod"

// lengthRefusalServer answers the server lookup and the inline submission. Any other route records
// an unexpected step, so a test sees an upload, a login lookup or a second submission.
type lengthRefusalServer struct {
	mu       sync.Mutex
	steps    []string
	status   int // 0 accepts the line
	response string
}

func (s *lengthRefusalServer) record(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

func (s *lengthRefusalServer) snapshotSteps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

func (s *lengthRefusalServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
	case r.Method == http.MethodPost && r.URL.Path == "/api/events/sessions/":
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"disabled"}`))
	case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
		s.record("inline-submit")
		if s.status != 0 {
			w.WriteHeader(s.status)
			_, _ = w.Write([]byte(s.response))
			return
		}
		_, _ = w.Write([]byte(`[{"id":"cmd-1"}]`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
		_, _ = w.Write([]byte(`{"id":"cmd-1","status":"completed","success":true,"exit_code":0,"result":"remote output\n"}`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/chunks/":
		_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
	default:
		s.record("unexpected " + r.Method + " " + r.URL.Path)
		http.NotFound(w, r)
	}
}

func newLengthRefusalServer(t *testing.T, s *lengthRefusalServer) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

func refusingLength() *lengthRefusalServer {
	return &lengthRefusalServer{status: http.StatusBadRequest, response: lineMaxLengthBody}
}

func oversizedCommand() string { return "echo " + strings.Repeat("a", 3000) }

// stdoutOf drops the "PASS" line the helper test binary prints after the command.
func stdoutOf(stdout string) string { return strings.TrimSuffix(stdout, "PASS\n") }

func TestLineTooLongGuidance(t *testing.T) {
	t.Parallel()
	const base = "the server refused this command line for its length"
	long := oversizedCommand()
	tests := []struct {
		name        string
		parsed      RemoteExecArgs
		wantMessage string
		wantCommand string
	}{
		{
			name:        "bare command",
			parsed:      RemoteExecArgs{Server: "prod", Command: long},
			wantMessage: base + "; run it as a verified file",
			wantCommand: "alpacon exec --interpreter /bin/sh " + uploadCommandTail,
		},
		{
			name: "account, session, purpose and detach carry over",
			parsed: RemoteExecArgs{
				Username: "deploy", Groupname: "ops", WorkSessionID: "550e8400-e29b-41d4-a716-446655440000",
				Purpose: "roll out; it's urgent", Detach: true, Server: "prod", Command: long,
			},
			wantMessage: base + "; run it as a verified file",
			wantCommand: "alpacon exec -u deploy -g ops --work-session 550e8400-e29b-41d4-a716-446655440000 --purpose 'roll out; it'\\''s urgent' --detach --interpreter /bin/sh " + uploadCommandTail,
		},
		{
			name:        "env cannot follow",
			parsed:      RemoteExecArgs{Server: "prod", Command: long, Env: map[string]string{"K": "v"}},
			wantMessage: base + ", and it cannot run as a verified file as it stands: --env values cannot ride the file lane; set the variables inside the script",
			wantCommand: "alpacon exec --interpreter /bin/sh " + uploadCommandTail,
		},
		{
			name:        "exactly 64 KB fits",
			parsed:      RemoteExecArgs{Server: "prod", Command: strings.Repeat("a", FileContentMaxBytes)},
			wantMessage: base + "; run it as a verified file",
			wantCommand: "alpacon exec --interpreter /bin/sh " + uploadCommandTail,
		},
		{
			name:        "over 64 KB",
			parsed:      RemoteExecArgs{Server: "prod", Command: strings.Repeat("a", FileContentMaxBytes+1)},
			wantMessage: base + ", and it cannot run as a verified file as it stands: 'the command line' exceeds 65536 bytes (64 KB), the ceiling for a verified file",
			wantCommand: "alpacon exec --interpreter /bin/sh " + uploadCommandTail,
		},
		{
			name:        "invalid UTF-8",
			parsed:      RemoteExecArgs{Server: "prod", Command: "echo \xff\xfe"},
			wantMessage: base + ", and it cannot run as a verified file as it stands: 'the command line' is not valid UTF-8; the content travels as JSON text, which would alter the bytes the agent hashes",
			wantCommand: "alpacon exec --interpreter /bin/sh " + uploadCommandTail,
		},
		{
			name:        "blank server falls back to the placeholder",
			parsed:      RemoteExecArgs{Command: long},
			wantMessage: base + "; run it as a verified file",
			wantCommand: "alpacon exec --interpreter /bin/sh --upload --file /path/to/script.sh --file-from ./script.sh SERVER",
		},
		{
			name:        "server stays one sanitized argument",
			parsed:      RemoteExecArgs{Server: "my server\x1b[2J", Command: long},
			wantMessage: base + "; run it as a verified file",
			wantCommand: "alpacon exec --interpreter /bin/sh --upload --file /path/to/script.sh --file-from ./script.sh 'my server'",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			message, hint, action := lineTooLongGuidance(tc.parsed)
			assert.Equal(t, tc.wantMessage, message)
			assert.Equal(t, tc.wantCommand, action.Command)
			assert.Contains(t, hint, "\n  "+tc.wantCommand+"\n")
			assert.Contains(t, action.Description, "with /bin/sh")
		})
	}
}

func TestExecAcceptedLineSendsNoGuidance(t *testing.T) {
	t.Parallel()
	s := &lengthRefusalServer{}
	ts := newLengthRefusalServer(t, s)

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "echo", "hi")

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Equal(t, "remote output\n", stdoutOf(stdout))
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
	assert.NotContains(t, stderr, "verified file")
}

// Both lanes stop on the first refusal: one submission, then no upload and no second submission.
func TestExecLineTooLongExits1WithTheFileLaneCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "streaming lane",
			args: []string{"-u", "deploy", "--purpose", "rollout", "prod"},
			want: "alpacon exec -u deploy --purpose rollout --interpreter /bin/sh " + uploadCommandTail,
		},
		{
			name: "detach lane",
			args: []string{"--detach", "prod"},
			want: "alpacon exec --detach --interpreter /bin/sh " + uploadCommandTail,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := refusingLength()
			ts := newLengthRefusalServer(t, s)
			command := oversizedCommand()

			stdout, stderr, exitCode := runExecHelper(t, ts.URL, append(tc.args, "--", command)...)

			assert.Equal(t, 1, exitCode, "stderr: %s", stderr)
			assert.Empty(t, stdoutOf(stdout))
			assert.Contains(t, stderr, "the server refused this command line for its length; run it as a verified file.\n")
			assert.Contains(t, stderr, "save the command to a local file, then run:\n  "+tc.want+"\n")
			assert.NotContains(t, stderr, command)
			assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
		})
	}
}

func TestExecLineTooLongJSONEnvelopeNamesTheCommand(t *testing.T) {
	t.Parallel()
	s := refusingLength()
	ts := newLengthRefusalServer(t, s)
	command := oversizedCommand()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "-u", "deploy", "prod", "--", command)

	assert.Equal(t, 1, exitCode, "stderr: %s", stderr)
	assert.Empty(t, stdoutOf(stdout))
	var got struct {
		Message     string             `json:"message"`
		ErrorCode   string             `json:"error_code"`
		HTTPStatus  int                `json:"http_status"`
		NextActions []utils.NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(stderr), &got), "stderr must be the envelope alone: %s", stderr)
	assert.Equal(t, "the server refused this command line for its length; run it as a verified file.", got.Message)
	assert.Equal(t, "max_length", got.ErrorCode)
	assert.Equal(t, http.StatusBadRequest, got.HTTPStatus)
	require.Len(t, got.NextActions, 1)
	assert.Equal(t, "alpacon exec -u deploy --interpreter /bin/sh "+uploadCommandTail, got.NextActions[0].Command)
	assert.NotContains(t, stderr, command)
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
}

func TestExecLineTooLongWithEnvNeverPrintsTheValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "table", args: []string{"--env=KEY=s3cr3t-value", "prod"}},
		{name: "json", args: []string{"--output", "json", "--env=KEY=s3cr3t-value", "prod"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := refusingLength()
			ts := newLengthRefusalServer(t, s)

			stdout, stderr, exitCode := runExecHelper(t, ts.URL, append(tc.args, "--", oversizedCommand())...)

			assert.Equal(t, 1, exitCode, "stderr: %s", stderr)
			assert.NotContains(t, stdout+stderr, "s3cr3t-value")
			assert.Contains(t, stderr, "--env values cannot ride the file lane")
			assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
		})
	}
}

func TestExecLineOver64KBSaysItOutgrowsTheFileLane(t *testing.T) {
	t.Parallel()
	s := refusingLength()
	ts := newLengthRefusalServer(t, s)

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "echo "+strings.Repeat("a", FileContentMaxBytes))

	assert.Equal(t, 1, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "exceeds 65536 bytes (64 KB)")
	assert.Contains(t, stderr, "once that is fixed, save the command to a local file and run:")
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
}

func TestExecMaxLengthOnAnotherFieldGetsNoFileLaneGuidance(t *testing.T) {
	t.Parallel()
	s := &lengthRefusalServer{status: http.StatusBadRequest, response: otherFieldMaxLength}
	ts := newLengthRefusalServer(t, s)

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "echo", "hi")

	assert.Equal(t, 1, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "Ensure this field has no more than 2000 characters.")
	assert.NotContains(t, stderr, "verified file")
	assert.Equal(t, []string{"inline-submit"}, s.snapshotSteps())
}

// A script already on the file lane has nowhere else to go, so the guidance stays out of its error.
func TestExecLineTooLongOnTheFileLaneIsReportedAsTheServerSaidIt(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "deploy.sh")
	require.NoError(t, os.WriteFile(script, []byte("echo hi\n"), 0o600))
	s := refusingLength()
	ts := newLengthRefusalServer(t, s)

	_, stderr, exitCode := runExecHelper(t, ts.URL, "--file", "/opt/deploy.sh", "--file-from", script, "prod")

	assert.Equal(t, 1, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "Ensure this field has no more than 2048 characters.")
	assert.NotContains(t, stderr, "run it as a verified file")
}
