package removed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/cmd/agent"
	"github.com/alpacax/alpacon-cli/cmd/removed"
	"github.com/alpacax/alpacon-cli/cmd/server"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const helperMarker = "removed-helper"

func TestMessageNamesTheServer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no args keeps the placeholder", args: nil, want: "run: alpacon exec <server> -- sudo reboot"},
		{name: "positional server", args: []string{"my-server"}, want: "run: alpacon exec my-server -- sudo reboot"},
		{name: "flags before the server are skipped", args: []string{"-y", "--force", "my-server"}, want: "run: alpacon exec my-server -- sudo reboot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := removed.Message("alpacon server reboot", "run: alpacon exec <server> -- sudo reboot", tt.args)
			assert.Equal(t, `"alpacon server reboot" was removed. `+tt.want, msg)
		})
	}
}

// Each stub runs in a child process, since it ends in os.Exit. The child is
// logged in to a workspace that counts every request, so a stub that reached
// the API would show up as a hit.
func TestRemovedCommandsPrintGuidanceWithoutCallingTheAPI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		contains []string
	}{
		{
			name: "server reboot",
			args: []string{"server", "reboot", "my-server", "-y", "--force"},
			contains: []string{
				`"alpacon server reboot" was removed.`,
				"alpacon exec my-server -- sudo reboot",
				"Mute the server's alerts before a planned reboot.",
			},
		},
		{
			name: "server shutdown",
			args: []string{"server", "shutdown", "-y", "my-server"},
			contains: []string{
				`"alpacon server shutdown" was removed.`,
				"alpacon exec my-server -- sudo shutdown -h now",
				"Mute the server's alerts before a planned shutdown.",
			},
		},
		{
			name: "server upgrade",
			args: []string{"server", "upgrade", "my-server"},
			contains: []string{
				`"alpacon server upgrade" was removed.`,
				"alpacon exec my-server -- sudo apt-get upgrade -y",
			},
		},
		{
			name: "server reboot help",
			args: []string{"server", "reboot", "--help"},
			contains: []string{
				`"alpacon server reboot" was removed.`,
				"alpacon exec <server> -- sudo reboot",
			},
		},
		{
			name: "agent shutdown",
			args: []string{"agent", "shutdown", "my-server"},
			contains: []string{
				`"alpacon agent shutdown" was removed.`,
				"The agent can no longer be stopped from the CLI.",
				"alpacon agent restart my-server",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer ts.Close()

			home := t.TempDir()
			writeTestConfig(t, home, ts.URL)

			stdout, stderr, exitCode := runHelper(t, tt.args, "GO_WANT_REMOVED_HELPER=1", homeEnvVar()+"="+home)

			assert.Equal(t, utils.ExitCodeCommandRemoved, exitCode)
			assert.Empty(t, stdout)
			for _, want := range tt.contains {
				assert.Contains(t, stderr, want)
			}
			assert.Zero(t, hits.Load())
		})
	}
}

func TestRemovedCommandsHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_REMOVED_HELPER") != "1" {
		return
	}
	i := slices.Index(os.Args, helperMarker)
	if i < 0 {
		os.Exit(2)
	}
	root := &cobra.Command{Use: "alpacon", SilenceUsage: true}
	root.AddCommand(server.ServerCmd, agent.AgentCmd)
	root.SetArgs(os.Args[i+1:])
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func runHelper(t *testing.T, args []string, env ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	helperArgs := append([]string{"-test.run=^TestRemovedCommandsHelperProcess$", "--", helperMarker}, args...)
	helper := osexec.CommandContext(ctx, os.Args[0], helperArgs...)
	helper.Env = append(os.Environ(), env...)

	var stdoutBuf, stderrBuf bytes.Buffer
	helper.Stdout = &stdoutBuf
	helper.Stderr = &stderrBuf

	if err := helper.Run(); err != nil {
		require.NoError(t, ctx.Err(), "helper did not finish in time")
		var exitErr *osexec.ExitError
		require.ErrorAs(t, err, &exitErr)
		exitCode = exitErr.ExitCode()
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

func homeEnvVar() string {
	if runtime.GOOS == "windows" {
		return "USERPROFILE"
	}
	return "HOME"
}

func writeTestConfig(t *testing.T, home, workspaceURL string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".alpacon")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))

	cfg := map[string]any{
		"workspace_url":           workspaceURL,
		"workspace_name":          "test",
		"access_token":            "access-token",
		"refresh_token":           "refresh-token",
		"access_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"insecure":                false,
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0600))
}
