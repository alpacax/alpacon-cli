package exec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DisableFlagParsing only covers the parent; this pins that a child still routes and parses flags normally.
func TestExecLsRoutesAndParsesFlags(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		expectedTail   int
		expectedServer string
		expectedUser   string
	}{
		{
			name:         "defaults",
			args:         []string{"ls"},
			expectedTail: 25,
		},
		{
			name:           "long flags",
			args:           []string{"ls", "--tail", "10", "--server", "my-server", "--user", "admin"},
			expectedTail:   10,
			expectedServer: "my-server",
			expectedUser:   "admin",
		},
		{
			name:           "short flags",
			args:           []string{"ls", "-t", "5", "-s", "other-server", "-u", "operator"},
			expectedTail:   5,
			expectedServer: "other-server",
			expectedUser:   "operator",
		},
		{
			name:         "list alias",
			args:         []string{"list", "--tail", "7"},
			expectedTail: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, flags, err := ExecCmd.Find(tt.args)
			require.NoError(t, err)
			require.Equal(t, "ls", cmd.Name())

			// Reset from each flag's own default so a prior case cannot leak in and the defaults case still checks what is registered.
			for _, name := range []string{"tail", "server", "user"} {
				require.NoError(t, cmd.Flags().Set(name, cmd.Flags().Lookup(name).DefValue))
			}

			require.NoError(t, cmd.ParseFlags(flags))

			tail, err := cmd.Flags().GetInt("tail")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedTail, tail)

			server, err := cmd.Flags().GetString("server")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedServer, server)

			user, err := cmd.Flags().GetString("user")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedUser, user)
		})
	}
}
