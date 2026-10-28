package worksession

import (
	"testing"

	"github.com/alpacax/alpacon-cli/client"
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
			t.Setenv(WorkSessionEnvVar, tt.envUUID)
			require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "", "", "", 0, false))
			if tt.cfgUUID != "" {
				require.NoError(t, config.SetActiveWorkSession(tt.cfgUUID))
			}

			got, err := Resolve(&client.AlpaconClient{WorkspaceName: "ws"}, tt.flag)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// switchConfigToWorkspace plays another shell's 'alpacon ws use': it moves the config to name and files session there.
func switchConfigToWorkspace(t *testing.T, name, session string) {
	t.Helper()
	before, err := config.LoadConfig()
	require.NoError(t, err)
	require.NoError(t, config.SwitchWorkspace(before, "https://"+name+".example.com", name, false))
	require.NoError(t, config.SetActiveWorkSession(session))
}

func TestResolve_UsesClientWorkspaceWhenConfigSwitchedAfterClientBuilt(t *testing.T) {
	setupTmpConfig(t)
	t.Setenv(WorkSessionEnvVar, "")
	require.NoError(t, config.SetActiveWorkSession("ses-a"))
	ac := &client.AlpaconClient{WorkspaceName: "ws"}
	switchConfigToWorkspace(t, "other", "ses-other")

	got, err := Resolve(ac, "")

	require.NoError(t, err)
	assert.Equal(t, "ses-a", got)
}

func TestSessionResolvableBeforeClient(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		envUUID string
		cfgUUID string
		want    bool
	}{
		{"nothing set", "", "", "", false},
		{"flag", "uuid-flag", "", "", true},
		{"env", "", "uuid-env", "", true},
		{"config", "", "", "uuid-cfg", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTmpConfig(t)
			t.Setenv(WorkSessionEnvVar, tt.envUUID)
			if tt.cfgUUID != "" {
				require.NoError(t, config.SetActiveWorkSession(tt.cfgUUID))
			}

			assert.Equal(t, tt.want, sessionResolvableBeforeClient(tt.flag))
		})
	}
}
