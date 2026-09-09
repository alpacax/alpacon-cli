package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTunnelValidatesPortsBeforeRefresh(t *testing.T) {
	for _, tc := range []struct{ name, local, remote, want string }{
		{"local", "invalid", "22", "invalid local port"},
		{"remote", "0", "invalid", "invalid remote port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".alpacon")
			require.NoError(t, os.Mkdir(dir, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"workspace_url":"http://127.0.0.1:1","workspace_name":"test","access_token":"expired","refresh_token":"test"}`), 0600))
			stdout, stderr, code := runHelperProcess(t, "TestTunnelValidationHelperProcess", "tunnel-validation-helper",
				[]string{"tunnel", "server", "-l", tc.local, "-r", tc.remote, "--work-session", "session"}, "GO_WANT_TUNNEL_VALIDATION_HELPER=1", homeEnvVar()+"="+home)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.want)
			assert.NotContains(t, stderr, "Refreshing access token")
		})
	}
}
func TestTunnelValidationHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_TUNNEL_VALIDATION_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, "tunnel-validation-helper")
	require.True(t, ok)
	RootCmd.SetArgs(args)
	Execute()
	os.Exit(0)
}
