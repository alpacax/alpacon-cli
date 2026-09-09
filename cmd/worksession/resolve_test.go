package worksession_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_Priority(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		envUUID  string
		cfgUUID  string
		expected string
	}{
		{"all empty", "", "", "", ""},
		{"only config", "", "", "uuid-cfg", "uuid-cfg"},
		{"only env", "", "uuid-env", "", "uuid-env"},
		{"only flag", "uuid-flag", "", "", "uuid-flag"},
		{"env wins over config", "", "uuid-env", "uuid-cfg", "uuid-env"},
		{"flag wins over env", "uuid-flag", "uuid-env", "", "uuid-flag"},
		{"flag wins over env and config", "uuid-flag", "uuid-env", "uuid-cfg", "uuid-flag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)
			t.Setenv(worksession.WorkSessionEnvVar, tt.envUUID)
			require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "", "", "", 0, false))
			if tt.cfgUUID != "" {
				require.NoError(t, config.SetActiveWorkSession(tt.cfgUUID))
			}

			got, err := worksession.Resolve(tt.flag)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestResolveForPinnedWorkspace(t *testing.T) {
	for _, tc := range []struct{ name, workspace, flag, env, want string }{
		{"pinned workspace", "a", "", "", "session-a"},
		{"other workspace", "b", "", "", "session-b"},
		{"absent workspace", "missing", "", "", ""},
		{"empty workspace", "", "", "", ""},
		{"environment override", "a", "", "session-env", "session-env"},
		{"flag override", "a", "session-flag", "session-env", "session-flag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(worksession.WorkSessionEnvVar, tc.env)
			require.NoError(t, config.CreateConfig("https://a.example.com", "a", "token", "", "", "", "", 0, false))
			require.NoError(t, config.SetActiveWorkSessionFor("a", "session-a"))
			require.NoError(t, config.SetActiveWorkSessionFor("b", "session-b"))
			require.NoError(t, config.SwitchWorkspace("https://b.example.com", "b"))
			got, err := worksession.ResolveFor(tc.workspace, tc.flag)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveForMissingAndInvalidConfig(t *testing.T) {
	for _, tc := range []struct {
		name, flag, env, content, want string
		invalid, wantErr               bool
	}{
		{name: "missing"},
		{name: "invalid", invalid: true, content: "{", wantErr: true},
		{name: "flag bypasses invalid", invalid: true, content: "{", flag: "flag", want: "flag"},
		{name: "environment bypasses invalid", invalid: true, content: "{", env: "env", want: "env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv(worksession.WorkSessionEnvVar, tc.env)
			if tc.invalid {
				dir := filepath.Join(home, config.ConfigFileDir)
				require.NoError(t, os.MkdirAll(dir, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(tc.content), 0600))
			}
			got, err := worksession.ResolveFor("a", tc.flag)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
