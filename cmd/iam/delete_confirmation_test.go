package iam

import (
	"bytes"
	"errors"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: it runs package-level commands, sets HOME and OutputFormat, and fills memberDeleteRequest.
func TestDeleteCommands_StopForConfirmationBeforeAnyRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // a regression that skips the check must not reach a real login
	saved, savedFormat := memberDeleteRequest, utils.OutputFormat
	t.Cleanup(func() { memberDeleteRequest, utils.OutputFormat = saved, savedFormat })
	utils.OutputFormat = utils.OutputFormatJSON
	memberDeleteRequest.Group, memberDeleteRequest.User = "my-group", "alice"
	tests := []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{
		{name: "group delete", cmd: groupDeleteCmd, args: []string{"my-group"}},
		{name: "user delete", cmd: userDeleteCmd, args: []string{"alice"}},
		{name: "member delete", cmd: memberDeleteCmd, args: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.RunE(tt.cmd, tt.args)

			exitErr, ok := errors.AsType[*utils.ExitError](err)
			require.True(t, ok, "want an ExitError, got %v", err)
			assert.Equal(t, utils.ExitCodeUsageError, exitErr.Code)
			var out bytes.Buffer
			utils.FprintExitError(&out, exitErr)
			assert.JSONEq(t, `{"ok":false,"exit_code":2,"error_code":"confirmation_required",`+
				`"message":"This operation requires confirmation. Use --yes (-y) to skip the prompt in non-interactive mode.",`+
				`"context":{"operation":""}}`, out.String())
		})
	}
}
