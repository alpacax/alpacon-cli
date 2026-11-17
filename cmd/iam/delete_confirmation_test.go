package iam

import (
	"bytes"
	"errors"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: it runs package-level commands and sets HOME and OutputFormat.
func TestDeleteCommands_StopForConfirmationBeforeAnyRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // a regression that skips the check must not reach a real login
	savedFormat := utils.OutputFormat
	t.Cleanup(func() { utils.OutputFormat = savedFormat })
	utils.OutputFormat = utils.OutputFormatJSON
	tests := []struct {
		name  string
		cmd   *cobra.Command
		flags []string
		args  []string
	}{
		{name: "group delete", cmd: groupDeleteCmd, args: []string{"my-group"}},
		{name: "user delete", cmd: userDeleteCmd, args: []string{"alice"}},
		{name: "member delete", cmd: newMemberDeleteCmd(cmdtest.NoClient, utils.Stdio), flags: []string{"--group", "my-group", "--user", "alice"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, tt.cmd.ParseFlags(tt.flags))

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
