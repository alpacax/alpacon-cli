package worksession

import (
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestWorkSessionApproveAndRejectFailWithoutARequest(t *testing.T) {
	for _, subcommand := range []string{"approve", "reject"} {
		t.Run(subcommand+"_table", func(t *testing.T) {
			ts := testutil.NewNoRequestServer(t)

			stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL, subcommand, "ses-1")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "Error: "+approveRejectExcludedMessage)
		})
		t.Run(subcommand+"_json", func(t *testing.T) {
			ts := testutil.NewNoRequestServer(t)

			stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL, subcommand, "ses-1")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assert.Empty(t, stdout)
			assert.JSONEq(t, `{"ok":false,"exit_code":1,"error_code":"cli_error","context":{"operation":""},
				"message":"Approvals must be done in the Alpacon console (web), not the CLI. The CLI is an execution and request surface only; approval and rejection happen out of band. Use 'alpacon work-session ls' to track a session's status."}`, stderr)
		})
	}
}
