package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBusyRefusalKeepsTheServerCodeUnderJSON(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"7d0f2a8e-1c3b-4f5a-9e6d-2b8c4a1f0e3d","name":"my-server"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/actions/"):
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"server_busy_with_user_work","detail":"busy"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	home := t.TempDir()
	writeServerTestConfig(t, home, ts.URL)
	helper := osexec.Command(os.Args[0], "-test.run=^TestBusyRefusalHelperProcess$")
	helper.Env = append(os.Environ(), "GO_WANT_BUSY_HELPER=1", "HOME="+home, "ALPACON_WORK_SESSION=")
	var stdout, stderr bytes.Buffer
	helper.Stdout, helper.Stderr = &stdout, &stderr

	err := helper.Run()

	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodeServerBusy, exitErr.ExitCode(), "stderr: %s", stderr.String())
	assert.Empty(t, stdout.String())
	var envelope struct {
		ExitCode   int    `json:"exit_code"`
		ErrorCode  string `json:"error_code"`
		HTTPStatus int    `json:"http_status"`
		Message    string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
	assert.Equal(t, utils.ExitCodeServerBusy, envelope.ExitCode)
	assert.Equal(t, utils.ServerBusyWithUserWork, envelope.ErrorCode)
	assert.Equal(t, http.StatusConflict, envelope.HTTPStatus)
	assert.Contains(t, envelope.Message, "pass --force to override")
}

// Serial: the child reaches an os.Exit.
func TestBusyRefusalHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_BUSY_HELPER") == "" {
		return
	}
	utils.OutputFormat = utils.OutputFormatJSON
	RunDisruptiveServerAction("my-server", "restart", "Restart %s?", "restarted", "Failed to restart", true, false)
	os.Exit(0)
}

func writeServerTestConfig(t *testing.T, home, workspaceURL string) {
	t.Helper()
	dir := filepath.Join(home, ".alpacon")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	data, err := json.Marshal(map[string]any{
		"workspace_url":           workspaceURL,
		"workspace_name":          "test",
		"access_token":            "access-token",
		"refresh_token":           "refresh-token",
		"access_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600))
}
