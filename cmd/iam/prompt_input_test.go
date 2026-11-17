package iam

import (
	"errors"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: it swaps os.Stdin and sets HOME.
// An exiting prompt would end the test binary here instead of returning.
func TestPromptPaths_ReturnTheNoInputFailureOnClosedStdin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	savedStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = savedStdin })
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "member add", run: func() error {
			cmd := newMemberAddCmd(cmdtest.NoClient)
			return cmd.RunE(cmd, nil)
		}},
		{name: "member delete", run: func() error {
			cmd := newMemberDeleteCmd(cmdtest.NoClient)
			return cmd.RunE(cmd, nil)
		}},
		{name: "group create", run: func() error { _, err := promptForGroup(nil, nil); return err }},
		{name: "user create", run: func() error { _, err := promptForUser(nil); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin, err := os.Open(os.DevNull)
			require.NoError(t, err)
			t.Cleanup(func() { _ = stdin.Close() })
			os.Stdin = stdin

			err = tt.run()

			exitErr, ok := errors.AsType[*utils.ExitError](err)
			require.True(t, ok, "want an ExitError, got %v", err)
			assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
			assert.Equal(t, "No input received.", exitErr.Error())
		})
	}
}
