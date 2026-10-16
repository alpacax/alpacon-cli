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

func TestBusyGuardHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_BUSY_HELPER") != "1" {
		return
	}
	utils.OutputFormat = os.Getenv("ALPACON_TEST_OUTPUT_FORMAT")
	RunDisruptiveServerAction("prod", "restart", "", "done", "Failed", true, false)
}

func runBusyHelper(t *testing.T, workspaceURL, format string) (stderr string, exitCode int) {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".alpacon")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	data, err := json.Marshal(map[string]any{
		"workspace_url": workspaceURL, "workspace_name": "test",
		"access_token": "access-token", "refresh_token": "refresh-token",
		"access_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"active_work_sessions":    map[string]string{},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0600))

	helper := osexec.Command(os.Args[0], "-test.run=^TestBusyGuardHelperProcess$")
	helper.Env = append(os.Environ(), "GO_WANT_BUSY_HELPER=1", "ALPACON_TEST_OUTPUT_FORMAT="+format, "HOME="+home, "ALPACON_WORK_SESSION=")
	var errBuf bytes.Buffer
	helper.Stderr = &errBuf
	err = helper.Run()
	if err == nil {
		return errBuf.String(), 0
	}
	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return errBuf.String(), exitErr.ExitCode()
}

func TestBusyGuardUnderJSONCarriesServerBusyCode(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"server_busy_with_user_work","message":"busy"}`))
	}))
	defer ts.Close()

	t.Run("json", func(t *testing.T) {
		stderr, exitCode := runBusyHelper(t, ts.URL, utils.OutputFormatJSON)
		assert.Equal(t, utils.ExitCodeServerBusy, exitCode)
		var env struct {
			ExitCode  int    `json:"exit_code"`
			ErrorCode string `json:"error_code"`
		}
		lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
		require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &env), "stderr: %s", stderr)
		assert.Equal(t, utils.ExitCodeServerBusy, env.ExitCode)
		assert.Equal(t, utils.ServerBusyWithUserWork, env.ErrorCode)
	})

	t.Run("table", func(t *testing.T) {
		stderr, exitCode := runBusyHelper(t, ts.URL, utils.OutputFormatTable)
		assert.Equal(t, utils.ExitCodeServerBusy, exitCode)
		assert.Contains(t, stderr, "is busy with active user work")
		assert.NotContains(t, stderr, "report")
	})
}
