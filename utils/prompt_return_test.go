package utils

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReturningPromptsPreserveInputAndEOF(t *testing.T) {
	cases := []struct {
		name, input string
		read        func() (any, error)
		want        any
		fail        bool
	}{
		{"default EOF", "", func() (any, error) { return ReadInputWithDefault("Value: ", "saved") }, "", true},
		{"default blank", "\n", func() (any, error) { return ReadInputWithDefault("Value: ", "saved") }, "saved", false},
		{"default value", " chosen ", func() (any, error) { return ReadInputWithDefault("Value: ", "saved") }, "chosen", false},
		{"required integer EOF", "", func() (any, error) { return ReadRequiredIntInput("Value: ") }, 0, true},
		{"required integer invalid then EOF", "bad\n", func() (any, error) { return ReadRequiredIntInput("Value: ") }, 0, true},
		{"required integer zero", "0", func() (any, error) { return ReadRequiredIntInput("Value: ") }, 0, false},
		{"integer EOF", "", func() (any, error) { return ReadIntInput("Value: ", 7) }, 0, true},
		{"integer blank", "\n", func() (any, error) { return ReadIntInput("Value: ", 7) }, 7, false},
		{"integer invalid", "bad", func() (any, error) { return ReadIntInput("Value: ", 7) }, 7, false},
		{"integer negative", "-2", func() (any, error) { return ReadIntInput("Value: ", 7) }, -2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "stdin")
			require.NoError(t, err)
			_, err = f.WriteString(tc.input)
			require.NoError(t, err)
			_, err = f.Seek(0, 0)
			require.NoError(t, err)
			saved := os.Stdin
			os.Stdin = f
			t.Cleanup(func() { os.Stdin = saved; _ = f.Close() })
			got, err := tc.read()
			assert.Equal(t, tc.want, got)
			if tc.fail {
				var exitErr *ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Equal(t, 1, exitErr.Code)
				require.EqualError(t, err, "No input received.")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMFAFailureReturnsBeforeRetryAndRunsCleanup(t *testing.T) {
	t.Parallel()
	cause := errors.New("step-up unavailable")
	retried, cleaned := false, false
	run := func() error {
		defer func() { cleaned = true }()
		return HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "my-server", ErrorHandlerCallbacks{OnMFARequired: func(string) error { return cause }, RetryOperation: func() error { retried = true; return nil }})
	}
	err := run()
	var exitErr *ExitError
	require.ErrorAs(t, err, &exitErr)
	require.ErrorIs(t, err, cause)
	require.EqualError(t, err, "MFA authentication failed: step-up unavailable")
	assert.Equal(t, 1, exitErr.Code)
	assert.True(t, cleaned, "deferred cleanup must run after MFA fails")
	assert.False(t, retried, "a failed step-up must not retry the operation")
}
