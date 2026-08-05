package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsCommandInlineCredentialError covers the pure code-matching helper: it
// must fire only on utils.CommandInlineCredential, never on other codes, a nil
// err, or a plain error without a code.
func TestIsCommandInlineCredentialError(t *testing.T) {
	t.Run("true when the error carries the inline-credential code", func(t *testing.T) {
		err := errors.New("code: " + utils.CommandInlineCredential + "; source: command")
		assert.True(t, isCommandInlineCredentialError(err))
	})

	t.Run("false for a different code", func(t *testing.T) {
		err := errors.New("code: " + utils.WorkSessionRequired + "; source: command")
		assert.False(t, isCommandInlineCredentialError(err))
	})

	t.Run("false for a plain error without a code", func(t *testing.T) {
		assert.False(t, isCommandInlineCredentialError(errors.New("boom")))
	})

	t.Run("false for nil", func(t *testing.T) {
		assert.False(t, isCommandInlineCredentialError(nil))
	})
}

// TestCredentialInlineHint asserts the hint points at --env, quotes the command
// the caller actually ran, and never echoes a credential value (there is nothing
// to echo—the example is fixed—but this guards against a future edit that starts
// interpolating the rejected line).
func TestCredentialInlineHint(t *testing.T) {
	tests := []struct {
		name      string
		invokedAs Invocation
		want      string
	}{
		{"exec", ExecInvocation, `alpacon exec --env="SECRET_NAME" db-server -- <command>`},
		{"websh takes the command as one quoted argument", WebshInvocation, `alpacon websh --env="SECRET_NAME" db-server '<command>'`},
		{"unset falls back to exec", "", `alpacon exec --env="SECRET_NAME" db-server -- <command>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := credentialInlineHint(tt.invokedAs)
			assert.Contains(t, hint, "--env")
			assert.Contains(t, hint, "Hint:")
			assert.Contains(t, hint, tt.want)
			assert.NotContains(t, hint, "hunter2", "hint must never echo a credential value")
		})
	}
}

// newInlineCredentialDenialServer returns a test server that resolves one
// server and rejects the command submission with the alpacon-server
// inline-credential gate (command_inline_credential, ADR 0037).
func newInlineCredentialDenialServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code": "command_inline_credential", "source": "command"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestExecInlineCredentialDenialExits1WithJSONErrorCode drives the real exec
// command through the inline-credential gate under --output json and asserts
// the envelope carries the server's error_code with a plain (non-special)
// exit code 1—this refusal is not a WorkSession gate or a pending approval.
func TestExecInlineCredentialDenialExits1WithJSONErrorCode(t *testing.T) {
	ts := newInlineCredentialDenialServer()
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
		"--",
		"mysql",
		"-pSecret",
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_EXEC_WORKSESSION_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var stdout, stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T", err)
	assert.Equal(t, 1, exitErr.ExitCode(), "inline-credential refusal uses the general failure exit code, not a dedicated one")
	assert.Empty(t, stdout.String())

	var envelope struct {
		OK        bool   `json:"ok"`
		ExitCode  int    `json:"exit_code"`
		ErrorCode string `json:"error_code"`
		Message   string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
	assert.False(t, envelope.OK)
	assert.Equal(t, 1, envelope.ExitCode)
	assert.Equal(t, utils.CommandInlineCredential, envelope.ErrorCode)
	assert.NotContains(t, stderr.String(), "-pSecret", "the rejected command line must never be echoed back")
}

// TestExecInlineCredentialDenialTablePrintsHint drives the same denial in the
// default table output and asserts the human-facing error + hint, and that the
// rejected command line is never echoed.
func TestExecInlineCredentialDenialTablePrintsHint(t *testing.T) {
	ts := newInlineCredentialDenialServer()
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)

	helper := osexec.Command(
		os.Args[0],
		"-test.run=^TestExecCommandWorkSessionGateHelperProcess$",
		"--",
		"exec-worksession-helper",
		"prod",
		"--",
		"mysql",
		"-pSecret",
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_EXEC_WORKSESSION_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var stdout, stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T", err)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Empty(t, stdout.String())

	out := stderr.String()
	assert.Contains(t, out, "credential", "should explain the refusal reason")
	assert.Contains(t, out, "Hint:")
	assert.Contains(t, out, "--env")
	assert.NotContains(t, out, "-pSecret", "the rejected command line must never be echoed back")
}

// TestExecNonInlineCredentialErrorFallsThroughUnchanged is a non-regression
// check: a submission failure with a different (or no) error code must still
// hit the pre-existing generic failure path (plain "Error: ..." line, no
// credential hint), not the new branch.
func TestExecNonInlineCredentialErrorFallsThroughUnchanged(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail": "some other rejection"}`))
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
		"prod",
		"id",
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_EXEC_WORKSESSION_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var stdout, stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T", err)
	assert.Equal(t, 1, exitErr.ExitCode())

	out := stderr.String()
	assert.Contains(t, out, "some other rejection")
	assert.NotContains(t, out, "Hint:", "unrelated errors must not surface the credential hint")
	assert.NotContains(t, out, "--env")
}
