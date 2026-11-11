package iam

import (
	"errors"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: it swaps os.Stdin, sets HOME and clears the package-level member requests.
// An exiting prompt would end the test binary here instead of returning.
func TestPromptPaths_ReturnTheNoInputFailureOnClosedStdin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	savedStdin, savedAdd, savedDelete := os.Stdin, memberRequest, memberDeleteRequest
	t.Cleanup(func() { os.Stdin, memberRequest, memberDeleteRequest = savedStdin, savedAdd, savedDelete })
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "member add", run: func() error {
			memberRequest.Group, memberRequest.User, memberRequest.Role = "", "", ""
			return memberAddCmd.RunE(memberAddCmd, nil)
		}},
		{name: "member delete", run: func() error {
			memberDeleteRequest.Group, memberDeleteRequest.User = "", ""
			return memberDeleteCmd.RunE(memberDeleteCmd, nil)
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
