package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseRemoteExecArgs_File covers the --file lane: the flags, the -- that
// separates the script's arguments, and the generic-lane fields staying
// untouched. Command is empty on this lane; Args are the words as typed.
func TestParseRemoteExecArgs_File(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		expected RemoteExecArgs
	}{
		{
			name: "file with no arguments",
			args: []string{"--file", "/opt/deploy.sh", "prod-web"},
			expected: RemoteExecArgs{
				Server: "prod-web",
				File:   &FileExecArgs{Path: "/opt/deploy.sh"},
			},
		},
		{
			name: "arguments after the separator, dashes included",
			args: []string{"--file", "/opt/deploy.sh", "prod-web", "--", "--fast", "-v", "two words"},
			expected: RemoteExecArgs{
				Server: "prod-web",
				File:   &FileExecArgs{Path: "/opt/deploy.sh", Args: []string{"--fast", "-v", "two words"}},
			},
		},
		{
			name: "separator with nothing after it",
			args: []string{"--file", "/opt/deploy.sh", "prod-web", "--"},
			expected: RemoteExecArgs{
				Server: "prod-web",
				File:   &FileExecArgs{Path: "/opt/deploy.sh", Args: []string{}},
			},
		},
		{
			name: "separator before the server also separates the arguments",
			args: []string{"--file", "/opt/deploy.sh", "--", "prod-web", "--fast"},
			expected: RemoteExecArgs{
				Server: "prod-web",
				File:   &FileExecArgs{Path: "/opt/deploy.sh", Args: []string{"--fast"}},
			},
		},
		{
			name: "equals forms and user@host",
			args: []string{"--file=/opt/deploy.sh", "--file-from=./deploy.sh", "--interpreter=/bin/sh", "root@prod-web"},
			expected: RemoteExecArgs{
				Username: "root",
				Server:   "prod-web",
				File:     &FileExecArgs{Path: "/opt/deploy.sh", From: "./deploy.sh", Interpreter: "/bin/sh"},
			},
		},
		{
			name: "combines with the generic-lane flags",
			args: []string{"-u", "deploy", "-g", "ops", "--work-session", "ses-1", "--purpose", "rollout", "--wait", "--file", "/opt/deploy.sh", "prod-web", "--", "--fast"},
			expected: RemoteExecArgs{
				Username:      "deploy",
				Groupname:     "ops",
				WorkSessionID: "ses-1",
				Purpose:       "rollout",
				Wait:          true,
				Server:        "prod-web",
				File:          &FileExecArgs{Path: "/opt/deploy.sh", Args: []string{"--fast"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.expected.Env = map[string]string{}
			assert.Equal(t, tt.expected, ParseRemoteExecArgs(tt.args))
		})
	}
}

// TestParseRemoteExecArgs_FileErrors pins the local refusals: a relative path or
// interpreter, a command line mixed with --file, --env on the file lane, and the
// two companion flags given without --file.
func TestParseRemoteExecArgs_FileErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		expectedErr string
	}{
		{
			name:        "relative file path",
			args:        []string{"--file", "opt/deploy.sh", "prod-web"},
			expectedErr: "--file must be an absolute path on the target server (starting with /): opt/deploy.sh",
		},
		{
			name:        "empty file path",
			args:        []string{"--file=", "prod-web"},
			expectedErr: "--file requires the script's absolute path on the target server",
		},
		{
			name:        "file flag with no value",
			args:        []string{"--file"},
			expectedErr: "flag needs an argument: --file",
		},
		{
			name:        "bare interpreter name",
			args:        []string{"--file", "/opt/deploy.sh", "--interpreter", "bash", "prod-web"},
			expectedErr: "--interpreter must be an absolute path (starting with /): bash",
		},
		{
			name:        "command line mixed with file",
			args:        []string{"--file", "/opt/deploy.sh", "prod-web", "bash", "/opt/other.sh"},
			expectedErr: "--file cannot be combined with a command line; put the script's arguments after --",
		},
		{
			name:        "env on the file lane",
			args:        []string{"--env=KEY=value", "--file", "/opt/deploy.sh", "prod-web"},
			expectedErr: "--env cannot be combined with --file; a verified file takes no environment—set the variables inside the script",
		},
		{
			name:        "file-from without file",
			args:        []string{"--file-from", "./deploy.sh", "prod-web", "uptime"},
			expectedErr: "--file-from requires --file",
		},
		{
			name:        "empty file-from",
			args:        []string{"--file", "/opt/deploy.sh", "--file-from=", "prod-web"},
			expectedErr: "--file-from requires a local path to read the script from",
		},
		{
			name:        "interpreter without file",
			args:        []string{"--interpreter", "/bin/sh", "prod-web", "uptime"},
			expectedErr: "--interpreter requires --file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)
			assert.Equal(t, tt.expectedErr, result.Err)
			assert.Empty(t, result.Server)
			assert.Nil(t, result.File)
		})
	}
}

// TestParseRemoteExecArgs_GenericLaneHasNoFile pins that the generic lane is
// untouched by the new field: File stays nil and Command is still joined.
func TestParseRemoteExecArgs_GenericLaneHasNoFile(t *testing.T) {
	t.Parallel()
	result := ParseRemoteExecArgs([]string{"prod-web", "--", "bash", "/opt/deploy.sh"})
	assert.Nil(t, result.File)
	assert.Equal(t, "bash /opt/deploy.sh", result.Command)
}

// TestLoadFileExecution covers the local read: defaults for --file-from and
// --interpreter, bytes sent exactly as read, and the refusals that never reach
// the server—a missing file, an empty one, one over the ceiling, and one that
// is not UTF-8 and so could not survive the JSON body intact.
func TestLoadFileExecution(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, data, 0o600))
		return path
	}
	raw := []byte("#!/bin/sh\r\n  echo ' spaced '  \n\n")
	rawPath := write("raw.sh", raw)
	emptyPath := write("empty.sh", nil)
	atCeiling := write("ceiling.sh", bytes.Repeat([]byte("a"), FileContentMaxBytes))
	overCeiling := write("over.sh", bytes.Repeat([]byte("a"), FileContentMaxBytes+1))
	invalidUTF8 := write("latin1.sh", []byte("#!/bin/sh\necho caf\xe9\n"))

	t.Run("defaults from and interpreter, keeps the bytes", func(t *testing.T) {
		t.Parallel()
		got, msg := loadFileExecution(FileExecArgs{Path: rawPath})
		require.Empty(t, msg)
		assert.Equal(t, event.FileExecution{
			Path:        rawPath,
			Interpreter: DefaultInterpreter,
			Args:        nil,
			Content:     string(raw),
		}, got)
	})

	t.Run("reads from --file-from and keeps the flags as given", func(t *testing.T) {
		t.Parallel()
		got, msg := loadFileExecution(FileExecArgs{
			Path:        "/opt/deploy.sh",
			From:        rawPath,
			Interpreter: "/bin/sh",
			Args:        []string{"--fast", ""},
		})
		require.Empty(t, msg)
		assert.Equal(t, event.FileExecution{
			Path:        "/opt/deploy.sh",
			Interpreter: "/bin/sh",
			Args:        []string{"--fast", ""},
			Content:     string(raw),
		}, got)
	})

	t.Run("accepts a file exactly at the ceiling", func(t *testing.T) {
		t.Parallel()
		got, msg := loadFileExecution(FileExecArgs{Path: atCeiling})
		require.Empty(t, msg)
		assert.Len(t, got.Content, FileContentMaxBytes)
	})

	t.Run("refuses an empty file", func(t *testing.T) {
		t.Parallel()
		_, msg := loadFileExecution(FileExecArgs{Path: emptyPath})
		assert.Equal(t, "'"+emptyPath+"' is empty; a verified file needs content to hash", msg)
	})

	t.Run("refuses a file over the ceiling", func(t *testing.T) {
		t.Parallel()
		_, msg := loadFileExecution(FileExecArgs{Path: overCeiling})
		assert.Equal(t, "'"+overCeiling+"' exceeds 65536 bytes (64 KB), the ceiling for a verified file", msg)
	})

	t.Run("a directory is refused with the path once", func(t *testing.T) {
		t.Parallel()
		_, msg := loadFileExecution(FileExecArgs{Path: dir})
		assert.Equal(t, fmt.Sprintf("cannot read the script from '%s': is a directory", dir), msg)
	})
	t.Run("refuses content that is not UTF-8", func(t *testing.T) {
		t.Parallel()
		_, msg := loadFileExecution(FileExecArgs{Path: invalidUTF8})
		assert.Contains(t, msg, "is not valid UTF-8")
	})

	t.Run("a missing default path names --file-from", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(dir, "missing.sh")
		_, msg := loadFileExecution(FileExecArgs{Path: missing})
		assert.Contains(t, msg, "cannot read the script from '"+missing+"'")
		assert.Contains(t, msg, "--file-from")
		assert.NotContains(t, msg, "open "+missing, "the path is named once, not again inside the OS error")
	})

	t.Run("a missing --file-from path does not suggest --file-from", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(dir, "missing.sh")
		_, msg := loadFileExecution(FileExecArgs{Path: "/opt/deploy.sh", From: missing})
		assert.Contains(t, msg, "cannot read the script from '"+missing+"'")
		assert.NotContains(t, msg, "--file-from")
	})
}

// TestFileExecRefusal covers the code-to-guidance table: each server code is
// rendered as a sentence rather than the raw code, the two client-bug codes say
// so, and anything else falls through so the generic path answers.
func TestFileExecRefusal(t *testing.T) {
	t.Parallel()
	coded := func(code string) error {
		return errors.New("code: " + code + "; source: command")
	}
	tests := []struct {
		name         string
		err          error
		wantMessage  string
		wantHintPart string
		wantNoHint   bool
		wantOK       bool
	}{
		{
			name:         "unsupported agent names the server and the version",
			err:          coded("file_exec_unsupported_agent"),
			wantMessage:  "the agent on 'prod-web' cannot verify a file digest; Alpamon 2.6.0 or newer is required",
			wantHintPart: "alpacon exec prod-web -- /bin/bash",
			wantOK:       true,
		},
		{
			name:         "assessor disabled",
			err:          coded("file_exec_assessor_disabled"),
			wantMessage:  "this deployment has the command assessor disabled, so 'prod-web' cannot run a verified file",
			wantHintPart: "alpacon exec prod-web -- /bin/bash",
			wantOK:       true,
		},
		{
			name:        "invalid path",
			err:         coded("file_exec_invalid_path"),
			wantMessage: "the server refused the file path or the interpreter: both must be absolute paths starting with /",
			wantNoHint:  true,
			wantOK:      true,
		},
		{
			name:         "content too large",
			err:          coded("file_exec_content_too_large"),
			wantMessage:  "the server refused the file: a verified file is limited to 65536 bytes (64 KB)",
			wantHintPart: "split the script",
			wantOK:       true,
		},
		{
			name:        "empty content",
			err:         coded("file_exec_empty_content"),
			wantMessage: "the server refused the file: it is empty, and a verified file needs content to hash",
			wantNoHint:  true,
			wantOK:      true,
		},
		{
			name:         "line too long",
			err:          coded("file_exec_line_too_long"),
			wantMessage:  "the interpreter, the path and the arguments together exceed the server's command line ceiling",
			wantHintPart: "shorten the arguments",
			wantOK:       true,
		},
		{
			name:         "env not allowed",
			err:          coded("file_exec_env_not_allowed"),
			wantMessage:  "the server refused the request: environment variables are not allowed on the file lane",
			wantHintPart: "inside the script",
			wantOK:       true,
		},
		{
			name:         "line not allowed is a client bug",
			err:          coded("file_exec_line_not_allowed"),
			wantMessage:  "the server refused the request: it carried a command line alongside the file",
			wantHintPart: "bug in alpacon-cli",
			wantOK:       true,
		},
		{
			name:         "data not allowed is a client bug",
			err:          coded("file_exec_data_not_allowed"),
			wantMessage:  "the server refused the request: it carried a data field alongside the file",
			wantHintPart: "bug in alpacon-cli",
			wantOK:       true,
		},
		{
			name:         "wrapped error still answers",
			err:          fmt.Errorf("failed to execute command on 'prod-web' server: %w", coded("file_exec_unsupported_agent")),
			wantMessage:  "the agent on 'prod-web' cannot verify a file digest; Alpamon 2.6.0 or newer is required",
			wantHintPart: "Alpamon",
			wantOK:       true,
		},
		{name: "another code falls through", err: coded(utils.CommandInlineCredential)},
		{name: "a plain error falls through", err: errors.New("boom")},
		{name: "nil falls through"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			message, hint, ok := fileExecRefusal(tt.err, "prod-web")
			assert.Equal(t, tt.wantOK, ok, "message %q", message)
			assert.Equal(t, tt.wantMessage, message)
			if tt.wantNoHint {
				assert.Empty(t, hint)
			}
			if tt.wantHintPart != "" {
				assert.Contains(t, hint, "Hint:")
				assert.Contains(t, hint, tt.wantHintPart)
				assert.True(t, strings.HasSuffix(hint, "\n"), "hint must end with a newline: %q", hint)
			}
			if !tt.wantOK {
				assert.Empty(t, hint)
			}
		})
	}
}

// TestReRunHint_File pins the re-run a pending approval prints on the file
// lane: only the flags the user gave, the arguments after --, and a quoted
// argument that carries a space.
func TestReRunHint_File(t *testing.T) {
	t.Parallel()
	t.Run("defaults are not spelled out", func(t *testing.T) {
		t.Parallel()
		hint := reRunHint(RemoteExecArgs{
			Server: "prod-web",
			File:   &FileExecArgs{Path: "/opt/deploy.sh"},
		})
		assert.Equal(t, "alpacon exec --file /opt/deploy.sh prod-web", hint.Command)
		assert.Empty(t, hint.Description)
	})
	t.Run("every flag and argument the user gave", func(t *testing.T) {
		t.Parallel()
		hint := reRunHint(RemoteExecArgs{
			Username:      "root",
			Groupname:     "wheel",
			WorkSessionID: "ses-1",
			Server:        "prod-web",
			File: &FileExecArgs{
				Path:        "/opt/deploy.sh",
				From:        "./deploy.sh",
				Interpreter: "/bin/sh",
				Args:        []string{"--fast", "two words"},
			},
		})
		assert.Equal(t,
			"alpacon exec -u root -g wheel --work-session ses-1 --file /opt/deploy.sh --file-from ./deploy.sh --interpreter /bin/sh prod-web -- --fast 'two words'",
			hint.Command)
	})
	// The arguments were argv on the first run and reached the server as a
	// JSON list; the hint must not hand a metacharacter to the local shell.
	t.Run("metacharacters in argv are quoted", func(t *testing.T) {
		t.Parallel()
		hint := reRunHint(RemoteExecArgs{
			Server: "prod-web",
			File: &FileExecArgs{
				Path: "/opt/$x.sh",
				Args: []string{"a;b", "$(cat ~/.aws/credentials)", "*", "it's", ""},
			},
		})
		assert.Equal(t,
			`alpacon exec --file '/opt/$x.sh' prod-web -- 'a;b' '$(cat ~/.aws/credentials)' '*' 'it'\''s' ''`,
			hint.Command)
	})
}

// fileLaneHelperCapture records what the fake server saw from the helper process.
type fileLaneHelperCapture struct {
	mu   sync.Mutex
	body []byte
}

func (c *fileLaneHelperCapture) record(body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = body
}

func (c *fileLaneHelperCapture) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

// newFileLaneServer resolves one server and answers the command submission
// with respond, recording the raw body.
func newFileLaneServer(capture *fileLaneHelperCapture, respond func(w http.ResponseWriter)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			body, _ := io.ReadAll(r.Body)
			capture.record(body)
			respond(w)
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestExecFileDetachSubmitsFileLane drives `exec --detach --file` end to end:
// the body carries the file object with the local bytes verbatim and none of
// the keys the server refuses, and the job id is printed as for any detach.
func TestExecFileDetachSubmitsFileLane(t *testing.T) {
	t.Parallel()
	var capture fileLaneHelperCapture
	ts := newFileLaneServer(&capture, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`[{"id":"cmd-file-1","shell":"file"}]`))
	})
	defer ts.Close()

	script := filepath.Join(t.TempDir(), "deploy.sh")
	content := "#!/bin/bash\nset -euo pipefail\necho deploy\n"
	require.NoError(t, os.WriteFile(script, []byte(content), 0o600))

	stdout, stderr, exitCode := runExecHelper(t, ts.URL,
		"--detach", "--file", "/opt/deploy.sh", "--file-from", script, "root@prod", "--", "--fast")
	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	// The helper process is a go test binary, so it appends its own PASS line.
	assert.Equal(t, "Job submitted: cmd-file-1\n", strings.TrimSuffix(stdout, "PASS\n"))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(capture.snapshot(), &payload), "body: %s", capture.snapshot())
	for _, key := range []string{"line", "data", "env"} {
		assert.NotContains(t, payload, key)
	}
	assert.Equal(t, "root", payload["username"])
	assert.Equal(t, map[string]any{
		"path":        "/opt/deploy.sh",
		"interpreter": "/bin/bash",
		"args":        []any{"--fast"},
		"content":     content,
	}, payload["file"])
}

// TestExecFileRefusalPrintsGuidance drives a file-lane refusal from the server
// through the real exec command: table mode prints the sentence and the hint
// rather than the raw code, and JSON mode carries the code in the envelope.
func TestExecFileRefusalPrintsGuidance(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "deploy.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/bash\necho hi\n"), 0o600))

	t.Run("table", func(t *testing.T) {
		t.Parallel()
		var capture fileLaneHelperCapture
		ts := newFileLaneServer(&capture, func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code": "file_exec_unsupported_agent", "source": "command"}`))
		})
		defer ts.Close()

		stdout, stderr, exitCode := runExecHelper(t, ts.URL,
			"--file", "/opt/deploy.sh", "--file-from", script, "prod")
		assert.Equal(t, 1, exitCode)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "the agent on 'prod' cannot verify a file digest; Alpamon 2.6.0 or newer is required")
		assert.Contains(t, stderr, "Hint:")
		assert.NotContains(t, stderr, "file_exec_unsupported_agent", "the code is rendered as guidance, not echoed")
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		var capture fileLaneHelperCapture
		ts := newFileLaneServer(&capture, func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code": "file_exec_assessor_disabled", "source": "command"}`))
		})
		defer ts.Close()

		stdout, stderr, exitCode := runExecHelper(t, ts.URL,
			"--output", "json", "--file", "/opt/deploy.sh", "--file-from", script, "prod")
		assert.Equal(t, 1, exitCode)
		assert.Empty(t, stdout)
		var envelope struct {
			OK        bool   `json:"ok"`
			ExitCode  int    `json:"exit_code"`
			ErrorCode string `json:"error_code"`
			Message   string `json:"message"`
		}
		require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), "stderr: %s", stderr)
		assert.False(t, envelope.OK)
		assert.Equal(t, 1, envelope.ExitCode)
		assert.Equal(t, "file_exec_assessor_disabled", envelope.ErrorCode)
		assert.Contains(t, envelope.Message, "command assessor disabled")
	})
}

// TestExecFileLocalRefusalNeverReachesServer pins that a local refusal—an
// empty script here—exits before any request is made.
func TestExecFileLocalRefusalNeverReachesServer(t *testing.T) {
	t.Parallel()
	var capture fileLaneHelperCapture
	ts := newFileLaneServer(&capture, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`[{"id":"cmd-file-1"}]`))
	})
	defer ts.Close()

	script := filepath.Join(t.TempDir(), "empty.sh")
	require.NoError(t, os.WriteFile(script, nil, 0o600))

	stdout, stderr, exitCode := runExecHelper(t, ts.URL,
		"--file", "/opt/deploy.sh", "--file-from", script, "prod")
	assert.Equal(t, 1, exitCode)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "is empty; a verified file needs content to hash")
	assert.Nil(t, capture.snapshot(), "no submission may reach the server")
	assert.NotContains(t, stderr, "failed to submit")
}
