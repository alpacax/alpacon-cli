package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecCommandWorkSessionGateExits3WithJSONDiagnostic(t *testing.T) {
	var sawCommandPost atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			sawCommandPost.Store(true)
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{
				"code": "work_session_required",
				"source": "command",
				"detail": "WorkSession required"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)

	helper := osexec.Command(
		os.Args[0],
		"-test.run=^TestExecCommandWorkSessionGateHelperProcess$",
		"--",
		"exec-worksession-helper",
		"--output",
		"json",
		"prod",
		"id",
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_EXEC_WORKSESSION_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T", err)
	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitErr.ExitCode())
	assert.Empty(t, stdout.String())
	assert.True(t, sawCommandPost.Load(), "expected exec command to submit the remote command request")

	var envelope struct {
		OK        bool   `json:"ok"`
		ExitCode  int    `json:"exit_code"`
		ErrorCode string `json:"error_code"`
		Reason    string `json:"reason"`
		Context   struct {
			AuthMethod    string   `json:"auth_method"`
			RequiredScope string   `json:"required_scope"`
			TargetServers []string `json:"target_servers"`
		} `json:"context"`
	}
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
	assert.False(t, envelope.OK)
	assert.Equal(t, utils.ExitCodeWorkSessionDenied, envelope.ExitCode)
	assert.Equal(t, utils.WorkSessionRequired, envelope.ErrorCode)
	assert.Equal(t, "no WorkSession selected for this shell", envelope.Reason)
	assert.Equal(t, "Browser login", envelope.Context.AuthMethod)
	assert.Equal(t, "command", envelope.Context.RequiredScope)
	assert.Equal(t, []string{"prod"}, envelope.Context.TargetServers)
}

func TestExecCommandWorkSessionGateHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXEC_WORKSESSION_HELPER") != "1" {
		return
	}

	args, ok := execWorkSessionHelperArgs(os.Args)
	if !ok {
		fmt.Fprintln(os.Stderr, "missing exec-worksession-helper marker")
		os.Exit(2)
	}
	ExecCmd.Run(ExecCmd, args)
}

func execWorkSessionHelperArgs(args []string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == "exec-worksession-helper" {
			return args[i+1:], true
		}
	}
	return nil, false
}

func writeExecCommandTestConfig(t *testing.T, home, workspaceURL string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".alpacon")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))

	cfg := map[string]any{
		"workspace_url":           workspaceURL,
		"workspace_name":          "test",
		"access_token":            "access-token",
		"refresh_token":           "refresh-token",
		"access_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"active_work_sessions":    map[string]string{},
		"insecure":                false,
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0600))
}
