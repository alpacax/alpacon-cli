package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildWelcomeLines(t *testing.T) {
	t.Run("not logged in (no config file)", func(t *testing.T) {
		setTestHome(t, t.TempDir())
		lines := buildWelcomeLines()
		require.Len(t, lines, 3)
		assert.Contains(t, lines[0], "alpacon")
		assert.Contains(t, lines[1], "Not logged in")
		assert.Contains(t, lines[2], "--help")
	})

	t.Run("logged in via Auth0 — workspace host shown", func(t *testing.T) {
		home := t.TempDir()
		setTestHome(t, home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://myws.us1.alpacon.io","workspace_name":"myws","access_token":"a"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		require.Len(t, lines, 3)
		assert.Equal(t, "myws.us1.alpacon.io", lines[1])
	})

	t.Run("logged in via legacy token — workspace host shown", func(t *testing.T) {
		home := t.TempDir()
		setTestHome(t, home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://myws.alpacon.io","workspace_name":"myws","token":"t"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		assert.Equal(t, "myws.alpacon.io", lines[1])
	})

	t.Run("config malformed — surfaces config error, not 'not logged in'", func(t *testing.T) {
		home := t.TempDir()
		setTestHome(t, home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{not-json"), 0600))

		lines := buildWelcomeLines()
		assert.Contains(t, lines[1], "Config read error")
		assert.NotContains(t, lines[1], "Not logged in")
	})

	t.Run("config exists but no tokens — treated as not logged in", func(t *testing.T) {
		home := t.TempDir()
		setTestHome(t, home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://x.alpacon.io","workspace_name":"x"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		assert.Contains(t, lines[1], "Not logged in")
	})
}

func TestHostFromURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"https with subdomain", "https://myws.us1.alpacon.io", "myws.us1.alpacon.io"},
		{"http", "http://example.com", "example.com"},
		{"with path and query", "https://myws.alpacon.io/some/path?x=1", "myws.alpacon.io"},
		{"with port", "https://localhost:8080", "localhost:8080"},
		{"no scheme falls back to raw input", "myws.alpacon.io", "myws.alpacon.io"},
		{"empty", "", ""},
		{"malformed url with scheme returns input", "https://", "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hostFromURL(tt.input))
		})
	}
}

func TestRootRegistersAPICommand(t *testing.T) {
	command, _, err := RootCmd.Find([]string{"api"})

	require.NoError(t, err)
	assert.Equal(t, "api", command.Name())
}

func TestRootVersionFlagBehavesLikeTheVersionCommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		cmd   []string
		flags map[string][]string
	}{
		{
			name:  "table",
			cmd:   []string{"version"},
			flags: map[string][]string{"long": {"--version"}, "shorthand": {"-v"}},
		},
		{
			name:  "json",
			cmd:   []string{"version", "--output", "json"},
			flags: map[string][]string{"long": {"--version", "--output", "json"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := func(t *testing.T, args []string) (string, string, int) {
				t.Helper()
				return runHelperProcess(t, "TestUpdateCommandHelperProcess", "update-helper", args,
					"GO_WANT_UPDATE_HELPER=1", homeEnvVar()+"="+t.TempDir(),
					"ALPACON_TEST_LATEST_RELEASE=1.4.0", "ALPACON_TEST_CURRENT_VERSION=1.3.0")
			}

			wantOut, wantErr, wantCode := run(t, tc.cmd)
			require.Contains(t, wantOut+wantErr, "1.4.0", "the version command must have reached the stub release")

			for name, flag := range tc.flags {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					gotOut, gotErr, gotCode := run(t, flag)

					assert.Equal(t, wantCode, gotCode)
					assert.Equal(t, wantOut, gotOut)
					assert.Equal(t, wantErr, gotErr)
				})
			}
		})
	}
}
