package event

import (
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEventTargetAllowsOmittedTarget(t *testing.T) {
	t.Parallel()

	target, err := validateEventTarget("")

	require.NoError(t, err)
	assert.Empty(t, target)
}

func TestValidateEventTargetRejectsInvalidID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target string
	}{
		{name: "whitespace only", target: "   "},
		{name: "parent traversal", target: "../x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := validateEventTarget(tc.target)

			require.ErrorIs(t, err, utils.ErrInvalidID)
			assert.Empty(t, got)
		})
	}
}

func TestValidateEventTargetReturnsTrimmedID(t *testing.T) {
	t.Parallel()

	target, err := validateEventTarget("  session-id  ")

	require.NoError(t, err)
	assert.Equal(t, "session-id", target)
}
