package event

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileLaneBodyCapture keeps the raw POST body of a file-lane submission so a
// test can assert on the keys the server refuses by presence.
type fileLaneBodyCapture struct {
	mu   sync.Mutex
	body []byte
}

func (c *fileLaneBodyCapture) record(body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = body
}

func (c *fileLaneBodyCapture) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

// newFileLaneCaptureServer resolves one server and records the raw body of the
// command submission, answering a minimal command list.
func newFileLaneCaptureServer(t *testing.T, capture *fileLaneBodyCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/servers/servers/"):
			_ = json.NewEncoder(w).Encode(api.ListResponse[map[string]any]{
				Count:   1,
				Results: []map[string]any{{"id": "srv-1", "name": "server-x"}},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/events/commands/"):
			body, _ := io.ReadAll(r.Body)
			capture.record(body)
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "cmd-1", "shell": "file"}})
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestCommandRequestMarshal_FileLaneOmitsRefusedKeys pins the wire contract of
// ADR 0053: the server refuses a file-lane body carrying line, data or env by
// key presence—an empty string counts—so the keys must be absent, not empty.
func TestCommandRequestMarshal_FileLaneOmitsRefusedKeys(t *testing.T) {
	t.Parallel()
	req := &CommandRequest{
		Username:               "root",
		Groupname:              "root",
		Server:                 "srv-1",
		RunAfter:               []string{},
		WorkSession:            "ses-1",
		Purpose:                "deploy",
		PurposeDemandSupported: true,
		File: &FileExecution{
			Path:        "/opt/deploy.sh",
			Interpreter: "/bin/bash",
			Args:        []string{"--fast"},
			Content:     "#!/bin/bash\nset -euo pipefail\n",
		},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	for _, key := range []string{"line", "data", "env", "shell"} {
		assert.NotContains(t, payload, key)
	}
	assert.JSONEq(t, `{
		"username": "root",
		"groupname": "root",
		"scheduled_at": null,
		"server": "srv-1",
		"run_after": [],
		"work_session": "ses-1",
		"purpose": "deploy",
		"purpose_demand_supported": true,
		"file": {
			"path": "/opt/deploy.sh",
			"interpreter": "/bin/bash",
			"args": ["--fast"],
			"content": "#!/bin/bash\nset -euo pipefail\n"
		}
	}`, string(body))
}

// TestFileLaneRequestMirrorsCommandRequest guards the hand-copied field list:
// a field added to CommandRequest later must either be one the file lane
// refuses (shell, line, env, data) or appear on fileLaneRequest under the same
// name and json tag, or it silently never travels on that lane.
func TestFileLaneRequestMirrorsCommandRequest(t *testing.T) {
	t.Parallel()
	refused := map[string]bool{"Shell": true, "Line": true, "Env": true, "Data": true}
	fileLane := reflect.TypeOf(fileLaneRequest{})
	generic := reflect.TypeOf(CommandRequest{})
	for i := range generic.NumField() {
		f := generic.Field(i)
		if refused[f.Name] {
			_, present := fileLane.FieldByName(f.Name)
			assert.False(t, present, "%s must not travel on the file lane", f.Name)
			continue
		}
		mirror, present := fileLane.FieldByName(f.Name)
		if assert.True(t, present, "CommandRequest.%s is missing from fileLaneRequest", f.Name) {
			// Only the wire name: omitempty is a per-lane choice (File is
			// optional on the generic lane and required on this one).
			assert.Equal(t, strings.Split(f.Tag.Get("json"), ",")[0], strings.Split(mirror.Tag.Get("json"), ",")[0], "json name of %s", f.Name)
			assert.Equal(t, f.Type, mirror.Type, "type of %s", f.Name)
		}
	}
	assert.Equal(t, generic.NumField()-len(refused), fileLane.NumField(), "fileLaneRequest carries a field CommandRequest does not")
}

// TestCommandRequestMarshal_GenericLaneUnchanged pins that a request without
// File keeps its shape: line, data and env are sent as they always were, and no
// file key appears.
func TestCommandRequestMarshal_GenericLaneUnchanged(t *testing.T) {
	t.Parallel()
	req := &CommandRequest{
		Shell:                  "system",
		Line:                   "uptime",
		Env:                    map[string]string{},
		Username:               "root",
		Server:                 "srv-1",
		RunAfter:               []string{},
		PurposeDemandSupported: true,
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"shell": "system",
		"line": "uptime",
		"env": {},
		"data": "",
		"username": "root",
		"groupname": "",
		"scheduled_at": null,
		"server": "srv-1",
		"run_after": [],
		"purpose_demand_supported": true
	}`, string(body))
}

// TestSubmitFileCommand_BodyCarriesFileVerbatim drives the real submission
// against a capturing server: the content arrives byte-for-byte, trailing
// newline and CRLF included, and the keys the file lane refuses are absent.
func TestSubmitFileCommand_BodyCarriesFileVerbatim(t *testing.T) {
	t.Parallel()
	var capture fileLaneBodyCapture
	ts := newFileLaneCaptureServer(t, &capture)
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	content := "#!/bin/sh\r\necho ' spaced '\n\n"
	resp, err := SubmitFileCommand(ac, "server-x", FileExecution{
		Path:        "/opt/deploy.sh",
		Interpreter: "/bin/sh",
		Args:        []string{"--fast", ""},
		Content:     content,
	}, "root", "wheel", "ses-abc", "")
	require.NoError(t, err)
	assert.Equal(t, "cmd-1", resp.ID)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(capture.snapshot(), &payload))
	for _, key := range []string{"line", "data", "env"} {
		assert.NotContains(t, payload, key)
	}
	assert.Equal(t, "srv-1", payload["server"])
	assert.Equal(t, "root", payload["username"])
	assert.Equal(t, "wheel", payload["groupname"])
	assert.Equal(t, "ses-abc", payload["work_session"])
	assert.NotContains(t, payload, "purpose")

	file, ok := payload["file"].(map[string]any)
	require.True(t, ok, "body must carry a file object: %s", capture.snapshot())
	assert.Equal(t, "/opt/deploy.sh", file["path"])
	assert.Equal(t, "/bin/sh", file["interpreter"])
	assert.Equal(t, []any{"--fast", ""}, file["args"])
	assert.Equal(t, content, file["content"])
}

// TestSubmitFileCommand_NilArgsSentAsEmptyList pins the server's default: args
// is an optional list, and a nil slice would marshal as null rather than [].
func TestSubmitFileCommand_NilArgsSentAsEmptyList(t *testing.T) {
	t.Parallel()
	var capture fileLaneBodyCapture
	ts := newFileLaneCaptureServer(t, &capture)
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := SubmitFileCommand(ac, "server-x", FileExecution{
		Path:        "/opt/deploy.sh",
		Interpreter: "/bin/bash",
		Content:     "#!/bin/bash\n",
	}, "", "", "", "")
	require.NoError(t, err)

	var payload struct {
		File struct {
			Args json.RawMessage `json:"args"`
		} `json:"file"`
	}
	require.NoError(t, json.Unmarshal(capture.snapshot(), &payload))
	assert.JSONEq(t, `[]`, string(payload.File.Args))
}
