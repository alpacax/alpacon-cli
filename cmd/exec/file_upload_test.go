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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRemoteExecArgs_FileUpload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		expected RemoteExecArgs
	}{
		{
			name: "upload from another local path",
			args: []string{"--upload", "--file", "/opt/deploy.sh", "--file-from", "./deploy.sh", "prod-web"},
			expected: RemoteExecArgs{
				Server: "prod-web",
				File:   &FileExecArgs{Path: "/opt/deploy.sh", From: "./deploy.sh", Upload: true},
			},
		},
		{
			name: "upload with overwrite and script arguments",
			args: []string{"--file", "/opt/deploy.sh", "--upload", "--overwrite", "root@prod-web", "--", "--fast"},
			expected: RemoteExecArgs{
				Username: "root",
				Server:   "prod-web",
				File: &FileExecArgs{
					Path: "/opt/deploy.sh", Args: []string{"--fast"}, Upload: true, Overwrite: true,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ParseRemoteExecArgs(tt.args)
			require.Empty(t, result.Err)
			assert.Equal(t, tt.expected.Username, result.Username)
			assert.Equal(t, tt.expected.Server, result.Server)
			assert.Equal(t, tt.expected.File, result.File)
		})
	}
}

func TestParseRemoteExecArgs_FileUploadErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		expectedErr string
	}{
		{
			name:        "upload without file",
			args:        []string{"--upload", "prod-web", "uptime"},
			expectedErr: "--upload requires --file; it writes the script to the --file path on the server",
		},
		{
			name:        "overwrite without upload",
			args:        []string{"--file", "/opt/deploy.sh", "--overwrite", "prod-web"},
			expectedErr: "--overwrite requires --upload; it only applies to the file --upload writes",
		},
		{
			name:        "upload with a value",
			args:        []string{"--file", "/opt/deploy.sh", "--upload=true", "prod-web"},
			expectedErr: "--upload does not accept a value; use --upload alone",
		},
		{
			name:        "overwrite with a value",
			args:        []string{"--file", "/opt/deploy.sh", "--upload", "--overwrite=yes", "prod-web"},
			expectedErr: "--overwrite does not accept a value; use --overwrite alone",
		},
		{
			name:        "upload to a path with dot-dot",
			args:        []string{"--file", "/opt/link/../deploy.sh", "--upload", "prod-web"},
			expectedErr: "--upload needs a clean --file path, without '.', '..', or repeated or trailing slashes: /opt/link/../deploy.sh",
		},
		{
			name:        "upload to a path with a repeated slash",
			args:        []string{"--file", "/opt//deploy.sh", "--upload", "prod-web"},
			expectedErr: "--upload needs a clean --file path, without '.', '..', or repeated or trailing slashes: /opt//deploy.sh",
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

// The first run already wrote the file, so the re-run verifies it in place:
// repeating --upload would fail on the existing file, and adding --overwrite
// would rewrite a file a reviewer may be looking at.
func TestReRunHint_FileUploadIsNotRepeated(t *testing.T) {
	t.Parallel()
	hint := reRunHint(RemoteExecArgs{
		Username: "root",
		Server:   "prod-web",
		File: &FileExecArgs{
			Path: "/opt/deploy.sh", From: "./deploy.sh", Upload: true, Overwrite: true,
		},
	})
	assert.Equal(t, "alpacon exec -u root --file /opt/deploy.sh --file-from ./deploy.sh prod-web", hint.Command)
}

// fileUploadServer answers a one-step --file run: the server lookup, the
// upload create, the object PUT, the transfer trigger and status, and the
// command submission. It records the order of the steps and what each carried.
type fileUploadServer struct {
	mu            sync.Mutex
	steps         []string
	uploadRequest map[string]any
	putBody       []byte
	commandBody   []byte
}

func (s *fileUploadServer) record(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

func newFileUploadServer(s *fileUploadServer, status string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/":
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			_ = json.Unmarshal(body, &s.uploadRequest)
			s.mu.Unlock()
			s.record("upload-create")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"upload-1","upload_url":"http://` + r.Host + `/s3/deploy.sh"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/s3/deploy.sh":
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.putBody = body
			s.mu.Unlock()
			s.record("upload-put")
		case r.Method == http.MethodGet && r.URL.Path == "/api/webftp/uploads/upload-1/upload/":
			s.record("upload-trigger")
		case r.Method == http.MethodGet && r.URL.Path == "/api/webftp/uploads/upload-1/status/":
			_, _ = w.Write([]byte(status))
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

// TestExecFileUploadWritesThenSubmitsTheSameBytes drives the one-step form end
// to end: the script is written to the --file path as the run-as account
// before the submission, without overwrite unless asked, and the bytes written
// are the bytes submitted for review.
func TestExecFileUploadWritesThenSubmitsTheSameBytes(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "deploy.sh")
	content := "#!/bin/bash\nset -euo pipefail\necho deploy\n"
	require.NoError(t, os.WriteFile(script, []byte(content), 0o600))

	for name, overwrite := range map[string]bool{"keep_existing": false, "overwrite": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var s fileUploadServer
			ts := newFileUploadServer(&s, `{"success":true}`)
			defer ts.Close()

			args := []string{"--detach", "--upload", "--file", "/opt/app/deploy.sh", "--file-from", script}
			if overwrite {
				args = append(args, "--overwrite")
			}
			args = append(args, "root@prod", "--", "--fast")
			stdout, stderr, exitCode := runExecHelper(t, ts.URL, args...)
			assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
			assert.Equal(t, "Job submitted: cmd-file-1\n", strings.TrimSuffix(stdout, "PASS\n"))

			s.mu.Lock()
			defer s.mu.Unlock()
			assert.Equal(t, []string{"upload-create", "upload-put", "upload-trigger", "command-submit"}, s.steps)
			assert.Equal(t, "deploy.sh", s.uploadRequest["name"])
			assert.Equal(t, "/opt/app/", s.uploadRequest["path"])
			assert.Equal(t, "root", s.uploadRequest["username"])
			assert.Equal(t, overwrite, s.uploadRequest["allow_overwrite"])
			assert.Equal(t, content, string(s.putBody))

			var payload struct {
				File struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				} `json:"file"`
			}
			require.NoError(t, json.Unmarshal(s.commandBody, &payload), "body: %s", s.commandBody)
			assert.Equal(t, "/opt/app/deploy.sh", payload.File.Path)
			assert.Equal(t, string(s.putBody), payload.File.Content)
		})
	}
}

// TestExecFileUploadRefusedExistingFileNeverSubmits pins that a failed write
// stops the run before the submission, and that a refusal of an existing file
// names --overwrite as the way to replace it.
func TestExecFileUploadRefusedExistingFileNeverSubmits(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "deploy.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/bash\necho hi\n"), 0o600))

	var s fileUploadServer
	ts := newFileUploadServer(&s, `{"success":false,"message":"/opt/deploy.sh already exists."}`)
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL,
		"--upload", "--file", "/opt/deploy.sh", "--file-from", script, "prod")
	assert.Equal(t, 1, exitCode)
	assert.Empty(t, strings.TrimSuffix(stdout, "PASS\n"))
	assert.Contains(t, stderr, "failed to upload the script to 'prod:/opt/deploy.sh': /opt/deploy.sh already exists.")
	assert.Contains(t, stderr, "--overwrite")

	s.mu.Lock()
	defer s.mu.Unlock()
	assert.NotContains(t, s.steps, "command-submit")
}
