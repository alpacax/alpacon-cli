package websh

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DisableFlagParsing covers only the parent; a child must still route and parse flags normally.
func TestWebshLsRoutesAndParsesFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		args         []string
		expectedTail int
	}{
		{
			name:         "defaults",
			args:         []string{"ls"},
			expectedTail: 25,
		},
		{
			name:         "long flag",
			args:         []string{"ls", "--tail", "50"},
			expectedTail: 50,
		},
		{
			// The parent's hand-rolled -s and -u never apply: routing to the subcommand comes
			// first.
			name:         "short flag",
			args:         []string{"ls", "-t", "5"},
			expectedTail: 5,
		},
		{
			name:         "list alias",
			args:         []string{"list", "--tail", "7"},
			expectedTail: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd, flags, err := newCommand(noClient).Find(tt.args)
			require.NoError(t, err)
			require.Equal(t, "ls", cmd.Name())

			require.NoError(t, cmd.ParseFlags(flags))

			tail, err := cmd.Flags().GetInt("tail")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedTail, tail)
		})
	}
}
