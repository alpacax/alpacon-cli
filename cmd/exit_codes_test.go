package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

const (
	exitHelperMarker = "exit-helper"
	exitHelperEnv    = "GO_WANT_EXIT_HELPER"
	exitUnreachable  = "http://127.0.0.1:1"
	exitStubHostEnv  = "GO_EXIT_STUB"
)

type exitCase struct {
	name       string
	args       []string
	stub       bool // start the child's stub server and point the saved workspace at it
	wantExit   int
	wantStderr string
}

// runExitCases runs each case through the real Execute() in a child process
// with a saved login, so the exit code is the one a script would see.
func runExitCases(t *testing.T, cases []exitCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := []string{exitHelperEnv + "=1", homeEnvVar() + "=" + t.TempDir()}
			if tt.stub {
				env = append(env, exitStubHostEnv+"=1")
			}

			stdout, stderr, exitCode := runHelperProcess(t, "TestExitCodeHelperProcess", exitHelperMarker, tt.args, env...)

			assert.Equal(t, tt.wantExit, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
			if tt.wantStderr != "" {
				assert.Contains(t, stderr, tt.wantStderr)
			}
		})
	}
}

// exitStubServer answers every request with an empty JSON list, enough for a
// command that only needs the server to be reachable before it refuses.
func exitStubServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
	}))
}

// Serial: the child reaches an os.Exit, and the parent's own run returns at once.
func TestExitCodeHelperProcess(t *testing.T) {
	if os.Getenv(exitHelperEnv) != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, exitHelperMarker)
	if !ok {
		os.Exit(99)
	}
	workspaceURL := exitUnreachable
	if os.Getenv(exitStubHostEnv) == "1" {
		ts := exitStubServer()
		defer ts.Close()
		workspaceURL = ts.URL
	}
	if err := config.CreateConfig(workspaceURL, "ws", "token", "", "", "", "", 0, false); err != nil {
		os.Exit(99)
	}
	RootCmd.SetArgs(args)
	Execute()
	os.Exit(0)
}

func TestCsrCreateExitsTwoOnABadCommandLine(t *testing.T) {
	runExitCases(t, []exitCase{
		{name: "valid days without a domain or ip", args: []string{"csr", "create", "--valid-days", "5"}, wantExit: utils.ExitCodeUsageError, wantStderr: "require --domain or --ip"},
		{name: "valid days of zero", args: []string{"csr", "create", "--domain", "a.com", "--valid-days", "0"}, wantExit: utils.ExitCodeUsageError, wantStderr: "--valid-days must be a positive integer"},
		{name: "no usable domain or ip", args: []string{"csr", "create", "--domain", ","}, wantExit: utils.ExitCodeUsageError, wantStderr: "at least one valid domain or IP"},
	})
}
