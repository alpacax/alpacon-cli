package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBusyGuardMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		force       bool
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "without force suggests --force",
			force:       false,
			wantContain: "pass --force to override",
			wantAbsent:  "despite --force",
		},
		{
			name:        "with force does not re-suggest --force",
			force:       true,
			wantContain: "despite --force",
			wantAbsent:  "pass --force to override",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := busyGuardMessage("my-server", tt.force)
			assert.Contains(t, msg, "my-server")
			assert.Contains(t, msg, tt.wantContain)
			assert.NotContains(t, msg, tt.wantAbsent)
		})
	}
}

// Serial: Find lazily mutates the package-global ServerCmd.
func TestHostPowerCommandsAreHiddenStubs(t *testing.T) {
	for _, name := range []string{"reboot", "shutdown", "upgrade"} {
		t.Run(name, func(t *testing.T) {
			cmd, _, err := ServerCmd.Find([]string{name})
			require.NoError(t, err)
			assert.Equal(t, name, cmd.Name())
			assert.True(t, cmd.Hidden, "server %s must stay out of --help", name)
			assert.True(t, cmd.DisableFlagParsing, "old flags such as -y and --force must still parse")
			assert.Contains(t, cmd.Long, "was removed")
		})
	}
}
