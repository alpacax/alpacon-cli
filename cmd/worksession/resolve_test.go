package worksession_test

import (
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

// ResolveFor reads the active slot of the workspace it is given, so a client
// built for one workspace never takes another's session.
func TestResolveFor_ReadsTheNamedWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(worksession.WorkSessionEnvVar, "")
	require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "", "", "", 0, false))
	require.NoError(t, config.SetActiveWorkSession("uuid-ws"))

	got, err := worksession.ResolveFor("", "ws")
	require.NoError(t, err)
	assert.Equal(t, "uuid-ws", got)

	got, err = worksession.ResolveFor("", "other")
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = worksession.ResolveFor("uuid-flag", "other")
	require.NoError(t, err)
	assert.Equal(t, "uuid-flag", got)
}
