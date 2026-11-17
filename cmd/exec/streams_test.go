package exec

import (
	"encoding/json"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecAppliesItsOwnOutputFlagToTheInjectedStreams(t *testing.T) {
	t.Parallel()
	build := func(ios *utils.IOStreams) *cobra.Command { return newCommand(cmdtest.NoClient, ios) }

	_, stderr, code := cmdtest.RunCaptured(build, "--output", "json", "--bogus")

	assert.Equal(t, utils.ExitCodeUsageError, code)
	var envelope struct {
		ErrorCode string `json:"error_code"`
		Message   string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), stderr)
	assert.Equal(t, utils.ErrorCodeUsage, envelope.ErrorCode)
	assert.NotContains(t, envelope.Message, "--help", "json mode carries no table-mode help pointer")
}
